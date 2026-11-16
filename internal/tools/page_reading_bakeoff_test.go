//go:build pagereadingbakeoff

package tools

// The page-reading bake-off (LOG "Page-reading bake-off"):
// every URL in urls.json read eleven ways, four fetchers by two
// extractors plus crawl4ai's own markdown, each output written to disk
// beside a results.json of what happened. It calls production's own
// fetcher and markdownFromHTML, so the plain-readability cell is
// exactly what web_fetch returns.
//
// Run from the repository root:
//
//	FIRECRAWL_API_KEY=... CRAWL4AI_API_TOKEN=... \
//	  go test -tags pagereadingbakeoff -run TestPageReadingBakeoff -timeout 60m -v ./internal/tools
//
// A cell whose output file already exists is not fetched again, so a
// rerun spends no Firecrawl credits on what it already has; delete a
// file to fetch that cell afresh. BAKEOFF_SKIP_FIRECRAWL=1 skips the
// Firecrawl cells and BAKEOFF_SKIP_CRAWL4AI=1 the crawl4ai ones; a
// failed cell is kept unless BAKEOFF_RETRY_FAILED=1; BAKEOFF_ONLY=id1,id2
// limits the run to those URLs.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/markusmobius/go-trafilatura"
)

type bakeoffPage struct {
	ID       string `json:"id"`
	Group    string `json:"group"`
	URL      string `json:"url"`
	Question string `json:"question"`
}

type bakeoffCellResult struct {
	PageID         string  `json:"page_id"`
	Cell           string  `json:"cell"`
	OK             bool    `json:"ok"`
	Error          string  `json:"error,omitempty"`
	Characters     int     `json:"characters"`
	FetchSeconds   float64 `json:"fetch_seconds"`
	MainContent    *bool   `json:"main_content_extracted,omitempty"`
	FromCachedFile bool    `json:"from_cached_file,omitempty"`
}

// The eleven cells, in the order the judge's table lists them.
var bakeoffCells = []string{
	"plain-readability", "plain-trafilatura",
	"firecrawl-readability", "firecrawl-trafilatura",
	"c4a-bare-readability", "c4a-bare-trafilatura",
	"c4a-full-readability", "c4a-full-trafilatura",
	"c4a-full-raw-markdown", "c4a-full-fit-markdown",
	"c4a-bm25-markdown",
}

const bakeoffDirectoryFromPackage = "../../evals/page-reading-bakeoff"

func TestPageReadingBakeoff(t *testing.T) {
	pagesJSON, err := os.ReadFile(filepath.Join(bakeoffDirectoryFromPackage, "urls.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pages []bakeoffPage
	if err := json.Unmarshal(pagesJSON, &pages); err != nil {
		t.Fatal(err)
	}
	only := map[string]bool{}
	for _, id := range strings.Split(os.Getenv("BAKEOFF_ONLY"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			only[id] = true
		}
	}

	readers := webPageReadersFromConfig()
	crawl4ai := crawl4aiClient{
		baseURL: envOr("CRAWL4AI_BASE_URL", "http://127.0.0.1:11235"),
		token:   os.Getenv("CRAWL4AI_API_TOKEN"),
		http:    &http.Client{Timeout: 120 * time.Second},
	}
	skipFirecrawl := os.Getenv("BAKEOFF_SKIP_FIRECRAWL") == "1"
	skipCrawl4ai := os.Getenv("BAKEOFF_SKIP_CRAWL4AI") == "1"
	retryFailed := os.Getenv("BAKEOFF_RETRY_FAILED") == "1"

	resultsPath := filepath.Join(bakeoffDirectoryFromPackage, "results.json")
	var results []bakeoffCellResult
	if existing, err := os.ReadFile(resultsPath); err == nil {
		_ = json.Unmarshal(existing, &results)
	}
	resultIndex := map[string]int{}
	for i, r := range results {
		resultIndex[r.PageID+"/"+r.Cell] = i
	}
	record := func(r bakeoffCellResult) {
		key := r.PageID + "/" + r.Cell
		if i, ok := resultIndex[key]; ok {
			if r.FromCachedFile {
				return // keep what the original fetch recorded
			}
			results[i] = r
		} else {
			resultIndex[key] = len(results)
			results = append(results, r)
		}
		encoded, _ := json.MarshalIndent(results, "", "  ")
		_ = os.WriteFile(resultsPath, append(encoded, '\n'), 0o644)
	}

	for _, page := range pages {
		if len(only) > 0 && !only[page.ID] {
			continue
		}
		outputDirectory := filepath.Join(bakeoffDirectoryFromPackage, "outputs", page.ID)
		if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
			t.Fatal(err)
		}
		cellsAlreadyRead := map[string]bool{}
		for _, cell := range bakeoffCells {
			existing, err := os.ReadFile(filepath.Join(outputDirectory, cell+".md"))
			if err != nil {
				continue
			}
			if retryFailed && bytes.HasPrefix(existing, []byte("(failed: ")) {
				continue
			}
			cellsAlreadyRead[cell] = true
		}
		allRead := func(cells ...string) bool {
			for _, c := range cells {
				if !cellsAlreadyRead[c] {
					return false
				}
			}
			return true
		}
		write := func(cell string, started time.Time, text string, mainContent *bool, readErr error) {
			result := bakeoffCellResult{PageID: page.ID, Cell: cell, FetchSeconds: time.Since(started).Seconds(), MainContent: mainContent}
			if readErr == nil && strings.TrimSpace(text) == "" {
				readErr = errors.New("no text")
			}
			if readErr != nil {
				result.Error = readErr.Error()
				text = "(failed: " + readErr.Error() + ")\n"
			} else {
				result.OK = true
				result.Characters = len([]rune(text))
			}
			_ = os.WriteFile(filepath.Join(outputDirectory, cell+".md"), []byte(text), 0o644)
			record(result)
			t.Logf("%-28s %-24s ok=%-5v chars=%-7d %.1fs %s", page.ID, cell, result.OK, result.Characters, result.FetchSeconds, result.Error)
		}
		ctx := context.Background()

		// Plain GET, through production's guarded fetcher.
		if !allRead("plain-readability", "plain-trafilatura") {
			started := time.Now()
			fetched, fetchErr := readers.fetcher.Fetch(ctx, page.URL)
			if fetchErr != nil {
				fetchErr = explainedFetchError(fetchErr)
				write("plain-readability", started, "", nil, fetchErr)
				write("plain-trafilatura", started, "", nil, fetchErr)
			} else {
				text, readErr := webPageTextFromFetchedPage(fetched)
				if readErr != nil {
					write("plain-readability", started, "", nil, readErr)
				} else {
					write("plain-readability", started, text.Text, &text.ExtractedMainContent, nil)
				}
				body, bodyErr := utf8Body(fetched)
				if bodyErr == nil && !(fetched.ContentType == "text/html" || fetched.ContentType == "application/xhtml+xml" || fetched.ContentType == "") {
					bodyErr = fmt.Errorf("serves %s, not HTML", fetched.ContentType)
				}
				if bodyErr != nil {
					write("plain-trafilatura", started, "", nil, bodyErr)
				} else {
					finalURL, _ := url.Parse(fetched.FinalURL)
					markdown, extractErr := markdownFromHTMLWithTrafilatura(body, finalURL)
					write("plain-trafilatura", started, markdown, nil, extractErr)
				}
			}
		}

		// Firecrawl's rendered HTML (a PDF: its own markdown, in both cells).
		if !skipFirecrawl && !allRead("firecrawl-readability", "firecrawl-trafilatura") {
			started := time.Now()
			rendered, scrapeErr := readers.firecrawl.Scrape(ctx, page.URL)
			if scrapeErr != nil {
				write("firecrawl-readability", started, "", nil, scrapeErr)
				write("firecrawl-trafilatura", started, "", nil, scrapeErr)
			} else {
				text, readErr := webPageTextFromFirecrawlPage(rendered, page.URL)
				if readErr != nil {
					write("firecrawl-readability", started, "", nil, readErr)
				} else {
					write("firecrawl-readability", started, text.Text, &text.ExtractedMainContent, nil)
				}
				if rendered.RawHTML == "" || strings.Contains(rendered.ContentType, "pdf") {
					write("firecrawl-trafilatura", started, strings.TrimSpace(rendered.Markdown), nil, nil)
				} else {
					finalURL, _ := url.Parse(firstNonEmpty(rendered.FinalURL, page.URL))
					markdown, extractErr := markdownFromHTMLWithTrafilatura([]byte(rendered.RawHTML), finalURL)
					write("firecrawl-trafilatura", started, markdown, nil, extractErr)
				}
			}
		}

		// crawl4ai, bare render and with its page-interaction features.
		for _, variant := range []string{"bare", "full"} {
			cells := []string{"c4a-" + variant + "-readability", "c4a-" + variant + "-trafilatura"}
			if variant == "full" {
				cells = append(cells, "c4a-full-raw-markdown", "c4a-full-fit-markdown")
			}
			if skipCrawl4ai || allRead(cells...) {
				continue
			}
			started := time.Now()
			crawled, crawlErr := crawl4ai.crawl(ctx, page.URL, variant == "full")
			if crawlErr != nil {
				for _, cell := range cells {
					write(cell, started, "", nil, crawlErr)
				}
				continue
			}
			finalURL, _ := url.Parse(firstNonEmpty(crawled.FinalURL, page.URL))
			_, readabilityMarkdown, extracted, readErr := markdownFromHTML([]byte(crawled.HTML), finalURL)
			write(cells[0], started, readabilityMarkdown, &extracted, readErr)
			trafilaturaMarkdown, extractErr := markdownFromHTMLWithTrafilatura([]byte(crawled.HTML), finalURL)
			write(cells[1], started, trafilaturaMarkdown, nil, extractErr)
			if variant == "full" {
				write("c4a-full-raw-markdown", started, crawled.RawMarkdown, nil, nil)
				write("c4a-full-fit-markdown", started, crawled.FitMarkdown, nil, nil)
			}
		}

		// crawl4ai's query-aware filter, given the page's question.
		if !skipCrawl4ai && !allRead("c4a-bm25-markdown") {
			started := time.Now()
			markdown, mdErr := crawl4ai.bm25Markdown(ctx, page.URL, page.Question)
			write("c4a-bm25-markdown", started, markdown, nil, mdErr)
		}
	}
}

// markdownFromHTMLWithTrafilatura is markdownFromHTML with trafilatura
// in place of readability: same link resolution, same converter, so
// only the choice of main content differs.
func markdownFromHTMLWithTrafilatura(body []byte, pageURL *url.URL) (string, error) {
	result, err := trafilatura.Extract(bytes.NewReader(body), trafilatura.Options{
		OriginalURL:     pageURL,
		EnableFallback:  true,
		IncludeLinks:    true,
		ExcludeComments: true,
	})
	if err != nil {
		return "", fmt.Errorf("trafilatura: %w", err)
	}
	if result == nil || result.ContentNode == nil {
		return "", errors.New("trafilatura found no main content")
	}
	resolveLinksAgainstPageURLAndDropTitles(result.ContentNode, pageURL)
	converted, err := htmltomarkdown.ConvertNode(result.ContentNode)
	if err != nil {
		return "", fmt.Errorf("converting trafilatura's content to markdown: %w", err)
	}
	return strings.TrimSpace(string(converted)), nil
}

type crawl4aiClient struct {
	baseURL string
	token   string
	http    *http.Client
}

type crawl4aiPage struct {
	HTML        string
	FinalURL    string
	RawMarkdown string
	FitMarkdown string
}

func (c crawl4aiClient) post(ctx context.Context, path string, payload any) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("crawl4ai: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("crawl4ai answered HTTP %d: %s", response.StatusCode, firstCharacters(string(body), 300))
	}
	return body, nil
}

// crawl renders pageURL. Bare is crawl4ai's defaults, near what a thin
// headless browser of our own would get; full turns on the features
// that are the case for adopting it.
func (c crawl4aiClient) crawl(ctx context.Context, pageURL string, full bool) (*crawl4aiPage, error) {
	params := map[string]any{"cache_mode": "bypass"}
	if full {
		params["remove_consent_popups"] = true
		params["remove_overlay_elements"] = true
		params["scan_full_page"] = true
		params["flatten_shadow_dom"] = true
		params["process_iframes"] = true
		params["markdown_generator"] = map[string]any{
			"type": "DefaultMarkdownGenerator",
			"params": map[string]any{"content_filter": map[string]any{
				"type":   "PruningContentFilter",
				"params": map[string]any{"threshold": 0.48, "threshold_type": "fixed"},
			}},
		}
	}
	body, err := c.post(ctx, "/crawl", map[string]any{
		"urls":           []string{pageURL},
		"browser_config": map[string]any{"type": "BrowserConfig", "params": map[string]any{"headless": true}},
		"crawler_config": map[string]any{"type": "CrawlerRunConfig", "params": params},
	})
	if err != nil {
		return nil, err
	}
	var response struct {
		Results []struct {
			Success       bool            `json:"success"`
			ErrorMessage  string          `json:"error_message"`
			HTML          string          `json:"html"`
			RedirectedURL string          `json:"redirected_url"`
			StatusCode    int             `json:"status_code"`
			Markdown      json.RawMessage `json:"markdown"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("crawl4ai: unexpected response: %w", err)
	}
	if len(response.Results) == 0 {
		return nil, errors.New("crawl4ai: no result")
	}
	result := response.Results[0]
	if !result.Success {
		return nil, fmt.Errorf("crawl4ai could not crawl (HTTP %d): %s", result.StatusCode, result.ErrorMessage)
	}
	page := &crawl4aiPage{HTML: result.HTML, FinalURL: result.RedirectedURL}
	var markdown struct {
		RawMarkdown string `json:"raw_markdown"`
		FitMarkdown string `json:"fit_markdown"`
	}
	if json.Unmarshal(result.Markdown, &markdown) == nil {
		page.RawMarkdown, page.FitMarkdown = markdown.RawMarkdown, markdown.FitMarkdown
	} else {
		var plain string
		_ = json.Unmarshal(result.Markdown, &plain)
		page.RawMarkdown = plain
	}
	return page, nil
}

func (c crawl4aiClient) bm25Markdown(ctx context.Context, pageURL, question string) (string, error) {
	body, err := c.post(ctx, "/md", map[string]any{"url": pageURL, "f": "bm25", "q": question, "c": "0"})
	if err != nil {
		return "", err
	}
	var response struct {
		Markdown string `json:"markdown"`
		Success  bool   `json:"success"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("crawl4ai /md: unexpected response: %w", err)
	}
	return strings.TrimSpace(response.Markdown), nil
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
