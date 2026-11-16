package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"btr.mt/grounding/internal/backends"
)

// ladderSetUp stands in for the site, crawl4ai and Firecrawl,
// counting the calls each browser gets. A nil crawl4ai or Firecrawl
// answer means that rung is not set up.
type ladderSetUp struct {
	site            http.HandlerFunc
	crawl4aiResult  map[string]any // one entry of crawl4ai's "results"
	firecrawlData   map[string]any
	firecrawlCapDir string // set: cap Firecrawl at one credit, counted here

	crawl4aiCalls, firecrawlCalls int
}

// The page's URL is public-looking, so the browsers' pre-check takes it,
// and the plain fetch is dialled to the local site whatever it says.
const ladderTestPageURL = "http://news.example.com/article"

func (setUp *ladderSetUp) readers(t *testing.T) webPageReaders {
	t.Helper()
	site := httptest.NewServer(setUp.site)
	t.Cleanup(site.Close)
	client := backends.NewClient()
	backends.SetRateLimit("crawl4ai", 1000, 10)
	backends.SetRateLimit("firecrawl", 1000, 10)
	readers := webPageReaders{
		fetcher:   backends.NewWebPageFetcher(client, backends.DialEveryHostAtForTests(strings.TrimPrefix(site.URL, "http://"))),
		crawl4ai:  backends.NewCrawl4AI(client, "", ""),
		firecrawl: backends.NewFirecrawl(client, ""),
	}
	if setUp.crawl4aiResult != nil {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			setUp.crawl4aiCalls++
			json.NewEncoder(w).Encode(map[string]any{"results": []any{setUp.crawl4aiResult}})
		}))
		t.Cleanup(server.Close)
		readers.crawl4ai = backends.NewCrawl4AI(client, server.URL, "token").ResolvingEveryHostToForTests(netip.MustParseAddr("93.184.215.14"))
	}
	if setUp.firecrawlData != nil {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			setUp.firecrawlCalls++
			json.NewEncoder(w).Encode(map[string]any{"success": true, "data": setUp.firecrawlData})
		}))
		t.Cleanup(server.Close)
		readers.firecrawl = backends.NewFirecrawl(client, "fc-key").WithBaseURLForTests(server.URL)
		if setUp.firecrawlCapDir != "" {
			readers.firecrawl.WithDailyCreditCap(1, filepath.Join(setUp.firecrawlCapDir, "spent"))
		}
	}
	return readers
}

func servesHTML(page string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(page))
	}
}

func answersStatus(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }
}

var (
	crawl4aiReadTheArticle  = map[string]any{"success": true, "status_code": 200, "html": articleHTML, "redirected_url": ladderTestPageURL}
	firecrawlReadTheArticle = map[string]any{
		"rawHtml":  articleHTML,
		"metadata": map[string]any{"url": ladderTestPageURL, "statusCode": 200, "contentType": "text/html"},
	}
	pageOfLinksHTML = `<html><head><title>Sign in</title></head><body><a href="/login">Sign in</a> <a href="/join">Join</a> <a href="/help">Help</a></body></html>`
)

func TestAutoStopsAtThePlainFetchWhenItReadsThePage(t *testing.T) {
	setUp := &ladderSetUp{site: servesHTML(articleHTML), crawl4aiResult: crawl4aiReadTheArticle, firecrawlData: firecrawlReadTheArticle}
	out, err := setUp.readers(t).fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Renderer: plain") || strings.Contains(out, "Note: read by") {
		t.Errorf("want the plain fetch's text, no ladder note:\n%s", out)
	}
	if setUp.crawl4aiCalls+setUp.firecrawlCalls != 0 {
		t.Errorf("no browser should run for a page the plain fetch read (crawl4ai %d, Firecrawl %d)", setUp.crawl4aiCalls, setUp.firecrawlCalls)
	}
}

func TestAutoClimbsToCrawl4AIPastABotCheckAndSaysWhy(t *testing.T) {
	setUp := &ladderSetUp{site: servesHTML(botCheckPagesServedInPlaceOfThePage["pubmed-central"]), crawl4aiResult: crawl4aiReadTheArticle, firecrawlData: firecrawlReadTheArticle}
	out, err := setUp.readers(t).fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Renderer: crawl4ai") || !strings.Contains(out, "means-ends analysis") {
		t.Errorf("want crawl4ai's read of the article:\n%s", out)
	}
	if !strings.Contains(out, `Note: read by crawl4ai's browser because the plain fetch was served a bot check or block page (it says "checking your browser before accessing")`) {
		t.Errorf("want the climb explained above the frame:\n%s", out)
	}
	if setUp.firecrawlCalls != 0 {
		t.Errorf("Firecrawl must not be paid for a page crawl4ai read")
	}
}

func TestAutoClimbsToFirecrawlWhenCrawl4AIIsBlockedToo(t *testing.T) {
	setUp := &ladderSetUp{
		site:           answersStatus(http.StatusForbidden),
		crawl4aiResult: map[string]any{"success": false, "status_code": 403, "error_message": "Blocked by anti-bot protection", "html": ""},
		firecrawlData:  firecrawlReadTheArticle,
	}
	out, err := setUp.readers(t).fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Renderer: firecrawl", "Note: read by Firecrawl's browser, for a credit, because the plain fetch was answered HTTP 403; crawl4ai was answered HTTP 403", "means-ends analysis"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q:\n%s", want, out)
		}
	}
}

func TestAutoReturnsCrawl4AIsThinPageRatherThanPayFirecrawl(t *testing.T) {
	setUp := &ladderSetUp{
		site:           servesHTML(pageOfLinksHTML),
		crawl4aiResult: map[string]any{"success": true, "status_code": 200, "html": pageOfLinksHTML},
		firecrawlData:  firecrawlReadTheArticle,
	}
	out, err := setUp.readers(t).fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Renderer: crawl4ai") || !strings.Contains(out, "almost no text outside its links") {
		t.Errorf("want crawl4ai's render, noted as thin:\n%s", out)
	}
	if setUp.firecrawlCalls != 0 {
		t.Errorf("a page our own browser rendered thin is thin: Firecrawl must not be paid for it")
	}
}

func TestAutoWithoutCrawl4AIClimbsToFirecrawlForAPageOfLinks(t *testing.T) {
	// No browser of our own (a CLI run on a laptop): a page that may be
	// built by JavaScript goes to Firecrawl.
	setUp := &ladderSetUp{site: servesHTML(pageOfLinksHTML), firecrawlData: firecrawlReadTheArticle}
	out, err := setUp.readers(t).fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Renderer: firecrawl") || !strings.Contains(out, "crawl4ai is not set up here") {
		t.Errorf("want Firecrawl's read, with crawl4ai's absence said:\n%s", out)
	}
}

func TestAutoStopsWhereNoBrowserWouldHelp(t *testing.T) {
	for name, site := range map[string]http.HandlerFunc{
		"not found": answersStatus(http.StatusNotFound),
		"an image": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("\x89PNG"))
		},
		"a PDF": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/pdf")
			w.Write([]byte("%PDF-1.7"))
		},
	} {
		setUp := &ladderSetUp{site: site, crawl4aiResult: crawl4aiReadTheArticle, firecrawlData: firecrawlReadTheArticle}
		if _, err := setUp.readers(t).fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL}); err == nil {
			t.Errorf("%s: want an error", name)
		}
		if setUp.crawl4aiCalls+setUp.firecrawlCalls != 0 {
			t.Errorf("%s: no browser should run (crawl4ai %d, Firecrawl %d)", name, setUp.crawl4aiCalls, setUp.firecrawlCalls)
		}
	}
}

func TestAutoNeverSendsAPDFToFirecrawlUnasked(t *testing.T) {
	setUp := &ladderSetUp{
		site:           answersStatus(http.StatusForbidden),
		crawl4aiResult: map[string]any{"success": false, "status_code": 403, "html": ""},
		firecrawlData:  firecrawlReadTheArticle,
	}
	_, err := setUp.readers(t).fetchWebPage(context.Background(), WebFetchOptions{URL: "http://journal.example.com/paper.PDF"})
	if err == nil || !strings.Contains(err.Error(), "a PDF is not sent to Firecrawl unasked") {
		t.Errorf("want the PDF kept from Firecrawl, said, got %v", err)
	}
	if setUp.firecrawlCalls != 0 {
		t.Errorf("Firecrawl was called for a PDF")
	}
}

func TestAutoKeepsThePlainTextWhenFirecrawlsCapIsSpent(t *testing.T) {
	setUp := &ladderSetUp{site: servesHTML(pageOfLinksHTML), firecrawlData: firecrawlReadTheArticle, firecrawlCapDir: t.TempDir()}
	readers := setUp.readers(t)
	// The cap is one credit: the first page spends it.
	if _, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL}); err != nil {
		t.Fatal(err)
	}
	out, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: "http://news.example.com/another"})
	if err != nil {
		t.Fatal(err)
	}
	if setUp.firecrawlCalls != 1 {
		t.Errorf("want one Firecrawl call within a one-credit cap, got %d", setUp.firecrawlCalls)
	}
	for _, want := range []string{"Renderer: plain", "this is the plain fetch's text, kept because", "daily credit cap is spent", "almost no text outside its links"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q:\n%s", want, out)
		}
	}
}

func TestAutoRefusesWhatCrawl4AIWasRedirectedToIfNotPublic(t *testing.T) {
	setUp := &ladderSetUp{
		site:           answersStatus(http.StatusForbidden),
		crawl4aiResult: map[string]any{"success": true, "status_code": 200, "html": articleHTML, "redirected_url": "http://169.254.169.254/latest/meta-data/"},
		firecrawlData:  firecrawlReadTheArticle,
	}
	_, err := setUp.readers(t).fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL})
	if err == nil || !strings.Contains(err.Error(), "not on the public internet") {
		t.Errorf("want a refusal of the internal address, got %v", err)
	}
	if setUp.firecrawlCalls != 0 {
		t.Errorf("the ladder must stop at a refused address")
	}
}

func TestCrawl4AIRendererWhenNotSetUpSaysSo(t *testing.T) {
	setUp := &ladderSetUp{site: servesHTML(articleHTML)}
	_, err := setUp.readers(t).fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL, Renderer: WebFetchRendererCrawl4AI})
	if err == nil || !strings.Contains(err.Error(), "CRAWL4AI_BASE_URL") {
		t.Errorf("want an error naming the variable, got %v", err)
	}
}
