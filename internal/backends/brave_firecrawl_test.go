package backends

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBraveSearchSendsKeyAndStripsMarkup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "brave-key" {
			t.Errorf("missing key header")
		}
		if r.URL.Path != "/res/v1/web/search" || r.URL.Query().Get("q") != "working memory" || r.URL.Query().Get("count") != "2" {
			t.Errorf("unexpected request %s", r.URL)
		}
		w.Write([]byte(`{"web":{"results":[
			{"title":"<strong>Working memory</strong> &amp; load","url":"https://a.example/","description":"About <strong>working memory</strong>.","page_age":"2024-01-02T00:00:00"},
			{"title":"Second","url":"https://b.example/","description":"","extra_snippets":["more <b>text</b>"]},
			{"title":"Third","url":"https://c.example/","description":"cut"}]}}`))
	}))
	defer server.Close()

	SetRateLimit("brave", 1000, 10)
	brave := NewBrave(NewClient(), "brave-key")
	brave.baseURL = server.URL
	results, err := brave.Search(context.Background(), "working memory", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].Title != "Working memory & load" || results[0].Highlights[0] != "About working memory." {
		t.Errorf("markup not stripped: %+v", results[0])
	}
	if results[1].Highlights[0] != "more text" {
		t.Errorf("extra snippet lost: %+v", results[1])
	}
}

func TestBraveSearchWithoutKeyNamesTheVariable(t *testing.T) {
	_, err := NewBrave(NewClient(), "").Search(context.Background(), "x", 1)
	if err == nil || !strings.Contains(err.Error(), "BRAVE_API_KEY") {
		t.Errorf("want an error naming BRAVE_API_KEY, got %v", err)
	}
}

func TestFirecrawlScrapeAsksForRawHTMLAndReadsIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/scrape" || r.Header.Get("Authorization") != "Bearer fc-key" {
			t.Errorf("unexpected request %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			URL             string   `json:"url"`
			Formats         []string `json:"formats"`
			OnlyMainContent *bool    `json:"onlyMainContent"`
			Parsers         []struct {
				Type     string `json:"type"`
				MaxPages int    `json:"maxPages"`
			} `json:"parsers"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.URL != "https://spa.example/page" || strings.Join(body.Formats, ",") != "rawHtml,markdown" ||
			body.OnlyMainContent == nil || *body.OnlyMainContent {
			t.Errorf("unexpected body %+v", body)
		}
		// Every scrape caps a PDF's pages: each one parsed is a credit.
		if len(body.Parsers) != 1 || body.Parsers[0].Type != "pdf" || body.Parsers[0].MaxPages != FirecrawlPDFMaxPagesParsed {
			t.Errorf("want one pdf parser capped at %d pages, got %+v", FirecrawlPDFMaxPagesParsed, body.Parsers)
		}
		w.Write([]byte(`{"success":true,"data":{"rawHtml":"<html><body><h1>Rendered</h1></body></html>","markdown":"# Rendered","metadata":{"title":"SPA","sourceURL":"https://spa.example/page","url":"https://spa.example/page/","statusCode":200,"contentType":"text/html; charset=utf-8"}}}`))
	}))
	defer server.Close()

	SetRateLimit("firecrawl", 1000, 10)
	firecrawl := NewFirecrawl(NewClient(), "fc-key")
	firecrawl.baseURL = server.URL
	page, err := firecrawl.Scrape(context.Background(), "https://spa.example/page")
	if err != nil {
		t.Fatal(err)
	}
	if page.RawHTML != "<html><body><h1>Rendered</h1></body></html>" || page.Markdown != "# Rendered" ||
		page.FinalURL != "https://spa.example/page/" || page.Title != "SPA" || page.ContentType != "text/html; charset=utf-8" {
		t.Errorf("unexpected page %+v", page)
	}
}

func TestFirecrawlScrapeRefusesAnAnswerWithNeitherHTMLNorMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":{"metadata":{"url":"https://spa.example/empty","statusCode":200}}}`))
	}))
	defer server.Close()

	SetRateLimit("firecrawl", 1000, 10)
	firecrawl := NewFirecrawl(NewClient(), "fc-key")
	firecrawl.baseURL = server.URL
	if _, err := firecrawl.Scrape(context.Background(), "https://spa.example/empty"); err == nil {
		t.Error("want an error for an answer with no content")
	}
}

// Firecrawl dials from its own servers, so the URL pre-check is all
// that stops grounding sending it an internal address.
func TestFirecrawlRefusesNonPublicURLBeforeSendingIt(t *testing.T) {
	firecrawl := NewFirecrawl(NewClient(), "fc-key")
	firecrawl.baseURL = "http://127.0.0.1:1" // would fail loudly if reached
	_, err := firecrawl.Scrape(context.Background(), "http://100.64.0.2:8384/")
	if !errors.Is(err, ErrAddressNotPublic) {
		t.Errorf("want ErrAddressNotPublic, got %v", err)
	}
}
