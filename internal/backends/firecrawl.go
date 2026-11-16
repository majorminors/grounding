package backends

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// Firecrawl renders a page in a browser, so it reads pages built by
// JavaScript that a plain GET sees as an empty shell. Opt-in by name
// (web_fetch's renderer "firecrawl"): every scrape spends credits.
// Request shape ported from an earlier agent,
// which uses it live, then changed to ask for the rendered HTML: on
// 2026-10-03 Firecrawl's own markdown of a PMC article was mostly the
// .gov banner and menus, onlyMainContent notwithstanding, so grounding
// picks out the main content itself (web_fetch's markdownFromHTML),
// the same way for both renderers.
//
// Firecrawl fetches from its own servers, so grounding's dial-time
// address guard does not apply; the URL still passes WebURLOrRefusal
// first, so no private address or internal hostname is sent out.

const (
	firecrawlBaseURL = "https://api.firecrawl.dev"
	firecrawlRPS     = 2.0

	// FirecrawlPDFMaxPagesParsed caps what one PDF can cost. Firecrawl
	// bills a scrape at one credit plus one per PDF page parsed
	// (docs.firecrawl.dev/billing, read 2026-11-07), and with no cap a
	// researcher steered to Firecrawl for every paper spent about twenty
	// credits a scrape (LOG "Firecrawl's spend was PDFs"). Ten pages
	// hold a paper's abstract, introduction and usually its method.
	FirecrawlPDFMaxPagesParsed = 10
)

// FirecrawlBackend scrapes single pages through Firecrawl.
type FirecrawlBackend struct {
	client         *Client
	apiKey         string
	baseURL        string
	dailyCreditCap *firecrawlDailyCreditCap // nil: no cap
}

// NewFirecrawl creates a Firecrawl backend. An empty apiKey is allowed;
// Scrape then fails with a message naming the variable to set.
func NewFirecrawl(client *Client, apiKey string) *FirecrawlBackend {
	ensureRateLimit("firecrawl", firecrawlRPS, 1)
	return &FirecrawlBackend{client: client, apiKey: apiKey, baseURL: firecrawlBaseURL}
}

// WithBaseURLForTests points the backend at a test server standing in
// for Firecrawl's API, for tests outside this package.
func (f *FirecrawlBackend) WithBaseURLForTests(baseURL string) *FirecrawlBackend {
	f.baseURL = baseURL
	return f
}

// Name returns the backend identifier.
func (f *FirecrawlBackend) Name() string {
	return "firecrawl"
}

// Configured reports whether a key is set.
func (f *FirecrawlBackend) Configured() bool {
	return f != nil && f.apiKey != ""
}

// FirecrawlPage is one rendered page. RawHTML is the page as the
// browser left it; Markdown is Firecrawl's own conversion, read only
// for a document that has no HTML (a PDF).
type FirecrawlPage struct {
	RawHTML     string
	Markdown    string
	Title       string
	FinalURL    string
	StatusCode  int
	ContentType string
	// For a PDF: the pages Firecrawl parsed (at most
	// FirecrawlPDFMaxPagesParsed) and the document's true length, zero
	// when Firecrawl does not say.
	PDFPagesParsed int
	PDFPagesInAll  int
	// FromCache is true when grounding's cache answered: no credit spent.
	FromCache bool `json:"-"`
}

// Scrape renders pageURL and returns its HTML, with Firecrawl's
// markdown beside it for documents that have none.
// Successful scrapes are cached like any other answer (when the server
// has the cache on), so paging through a long page pays once.
func (f *FirecrawlBackend) Scrape(ctx context.Context, pageURL string) (*FirecrawlPage, error) {
	if f.apiKey == "" {
		return nil, ErrFirecrawlNotConfigured
	}
	parsedURL, err := url.Parse(pageURL)
	if err != nil {
		return nil, fmt.Errorf("not a URL: %w", err)
	}
	if err := WebURLOrRefusal(parsedURL); err != nil {
		return nil, err
	}

	key := "firecrawl\x00" + parsedURL.String()
	if cache != nil {
		if cached, ok := cache.get(key); ok {
			var page FirecrawlPage
			if json.Unmarshal(cached, &page) == nil {
				page.FromCache = true
				return &page, nil
			}
		}
	}

	// Only a scrape Firecrawl will be asked for counts against the cap:
	// the cache answered above for free.
	if err := f.dailyCreditCap.reserve(1); err != nil {
		return nil, err
	}

	// Formats checked against
	// https://docs.firecrawl.dev/api-reference/endpoint/scrape on
	// 2026-10-03: "rawHtml" is "the exact, unmodified HTML as received
	// from the page"; "html" is Firecrawl's cleaned copy, with <head>
	// removed and with it the title and metadata readability reads. A
	// PDF is parsed to markdown (the default "parsers"), so markdown is
	// asked for too. onlyMainContent is off: it filters what markdown is
	// made from, not rawHtml, and markdown is read only for a document
	// with no HTML to filter.
	requestBody, err := json.Marshal(map[string]any{
		"url":             parsedURL.String(),
		"formats":         []string{"rawHtml", "markdown"},
		"onlyMainContent": false,
		// Checked against the scrape reference on 2026-11-07: a PDF
		// parser object takes maxPages, and the answer's metadata then
		// carries numPages (parsed) and totalPages (the true count).
		"parsers": []map[string]any{{"type": "pdf", "maxPages": FirecrawlPDFMaxPagesParsed}},
	})
	if err != nil {
		return nil, err
	}
	body, err := f.client.Do(ctx, Request{
		Method:  http.MethodPost,
		URL:     f.baseURL + "/v2/scrape",
		Backend: "firecrawl",
		Body:    requestBody,
		Headers: map[string]string{"Authorization": "Bearer " + f.apiKey},
	})
	if err != nil {
		return nil, fmt.Errorf("firecrawl scrape error: %w", err)
	}

	var response firecrawlScrapeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("firecrawl: response is not the expected JSON: %w", err)
	}
	if !response.Success {
		return nil, fmt.Errorf("firecrawl could not scrape the page: %s", response.Error)
	}
	if response.Data.RawHTML == "" && response.Data.Markdown == "" {
		return nil, fmt.Errorf("firecrawl answered without the page's HTML or markdown")
	}
	metadata := response.Data.Metadata
	page := &FirecrawlPage{
		RawHTML:        response.Data.RawHTML,
		Markdown:       response.Data.Markdown,
		Title:          metadata.Title,
		FinalURL:       metadata.URL,
		StatusCode:     metadata.StatusCode,
		ContentType:    metadata.ContentType,
		PDFPagesParsed: metadata.NumPages,
		PDFPagesInAll:  metadata.TotalPages,
	}
	// One line per paid scrape, so the journal shows where credits go.
	// No URL: like the access log, it does not record what was read.
	log.Printf("firecrawl scrape content_type=%q status=%d pdf_pages_parsed=%d pdf_pages_in_all=%d provider_credits_cost=%d",
		metadata.ContentType, metadata.StatusCode, metadata.NumPages, metadata.TotalPages, metadata.Provider.CreditsCost)
	if err := f.dailyCreditCap.add(metadata.NumPages); err != nil {
		log.Printf("firecrawl: could not count %d PDF pages against the daily cap: %v", metadata.NumPages, err)
	}
	if page.FinalURL == "" {
		page.FinalURL = response.Data.Metadata.SourceURL
	}
	if cache != nil {
		if encoded, err := json.Marshal(page); err == nil {
			cache.put(key, encoded)
		}
	}
	return page, nil
}

// ErrFirecrawlNotConfigured is returned when no key is set.
var ErrFirecrawlNotConfigured = errors.New("firecrawl: FIRECRAWL_API_KEY not configured")

// FirecrawlScrapeFailureSpentACredit reports whether a failed Scrape
// had already counted a credit against the daily cap: every failure
// after the request was about to be sent, but not one refused before it
// (no key, a URL refused, the cap spent).
func FirecrawlScrapeFailureSpentACredit(err error) bool {
	return err != nil &&
		!errors.Is(err, ErrFirecrawlNotConfigured) &&
		!errors.Is(err, ErrAddressNotPublic) &&
		!errors.Is(err, ErrFirecrawlDailyCreditCapSpent) &&
		!strings.HasPrefix(err.Error(), "not a URL")
}

// HealthCheck reports whether a key is configured. It makes no call:
// every scrape spends credits.
func (f *FirecrawlBackend) HealthCheck(ctx context.Context) HealthStatus {
	if f.apiKey == "" {
		return HealthStatus{Healthy: true, Message: "available (no key configured—set FIRECRAWL_API_KEY)"}
	}
	message := "key configured (not probed: every scrape spends credits)"
	if f.dailyCreditCap != nil && f.dailyCreditCap.credits > 0 {
		spent, err := f.dailyCreditCap.spentToday()
		if err != nil {
			return HealthStatus{Healthy: false, Message: err.Error()}
		}
		message += fmt.Sprintf("; %d of today's %d-credit cap spent (UTC)", spent, f.dailyCreditCap.credits)
	}
	return HealthStatus{Healthy: true, Message: message}
}

type firecrawlScrapeResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Data    struct {
		RawHTML  string `json:"rawHtml"`
		Markdown string `json:"markdown"`
		Metadata struct {
			Title       string `json:"title"`
			SourceURL   string `json:"sourceURL"`
			URL         string `json:"url"`
			StatusCode  int    `json:"statusCode"`
			ContentType string `json:"contentType"`
			NumPages    int    `json:"numPages"`
			TotalPages  int    `json:"totalPages"`
			Provider    struct {
				CreditsCost int `json:"creditsCost"`
			} `json:"provider"`
		} `json:"metadata"`
	} `json:"data"`
}
