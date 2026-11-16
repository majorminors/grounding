package tools

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Each page here is what a site really served on 2026-11-07 in place of
// the page (LOG "Page-reading bake-off"), wrapped in enough markup for
// the plain renderer to read it.
var botCheckPagesServedInPlaceOfThePage = map[string]string{
	"pubmed-central": `<html><head><title>Preparing to load PMC</title></head><body><p>Checking your browser before accessing pmc.ncbi.nlm.nih.gov ...</p><p>Click <a href="/articles/PMC11270031/">here</a> if you are not automatically redirected after 5 seconds.</p></body></html>`,
	"nature":         `<html><head><title>Nature</title></head><body><p>A required part of this site couldn’t load. This may be due to a browser extension, network issues, or browser settings. Please check your connection, disable any ad blockers, or try using a different browser.</p></body></html>`,
	"quora":          `<html><head><title>Quora</title></head><body><a href="/register">Sign In</a><h1>Blocked by network security</h1><p>If you think you have been blocked in error, click the link below to contact us.</p></body></html>`,
}

func TestWebFetchRefusesABotCheckServedAsThePage(t *testing.T) {
	for name, page := range botCheckPagesServedInPlaceOfThePage {
		readers, base := testPageReaders(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(page))
		})
		_, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: base + "/article", Renderer: WebFetchRendererPlain})
		if err == nil || !strings.Contains(err.Error(), "bot check or block page") || !strings.Contains(err.Error(), `renderer "auto"`) {
			t.Errorf("%s: want a refusal naming the bot check and the auto renderer, got %v", name, err)
		}
		// With no browser set up, auto has nowhere to climb, and says so.
		_, err = readers.fetchWebPage(context.Background(), WebFetchOptions{URL: base + "/article"})
		if err == nil || !strings.Contains(err.Error(), "no renderer could read the page") || !strings.Contains(err.Error(), "bot check") {
			t.Errorf("%s: want auto to say no renderer could read it, and why, got %v", name, err)
		}
	}
}

func TestWebFetchFirecrawlRefusesABlockPageItWasServed(t *testing.T) {
	readers := firecrawlPageReaders(t, map[string]any{
		"rawHtml": botCheckPagesServedInPlaceOfThePage["quora"],
		"metadata": map[string]any{
			"url": "https://www.quora.com/What-is-the-meaning-of-life", "statusCode": 200,
			"contentType": "text/html; charset=utf-8",
		},
	})
	_, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: "https://www.quora.com/What-is-the-meaning-of-life", Renderer: WebFetchRendererFirecrawl})
	if err == nil || !strings.Contains(err.Error(), "served Firecrawl a bot check") || !strings.Contains(err.Error(), "no renderer here gets further") {
		t.Errorf("want a refusal saying Firecrawl was blocked too, got %v", err)
	}
}

func TestWebFetchKeepsAnArticleThatQuotesABotCheck(t *testing.T) {
	article := `<html><head><title>Why scrapers fail</title></head><body><article><h1>Why scrapers fail</h1>` +
		strings.Repeat(`<p>Many sites answer an automated reader with a page that says "Checking your browser before accessing", and a reader that does not notice will take that for the article. This paragraph is long enough to be the main content of a real article on the subject.</p>`, 12) +
		`</article></body></html>`
	readers, base := testPageReaders(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(article))
	})
	out, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: base + "/essay"})
	if err != nil {
		t.Fatalf("a long article quoting a bot check should be read, got %v", err)
	}
	if strings.Contains(out, "almost no text outside its links") {
		t.Errorf("an article of prose should not carry the links note:\n%s", out)
	}
}

func TestWebFetchNotesAPageThatIsMostlyLinks(t *testing.T) {
	// YouTube's watch page read without JavaScript: its footer.
	footer := `<html><head><title>YouTube</title></head><body><div>` +
		`<a href="/about/">About</a> <a href="/about/press/">Press</a> <a href="/about/copyright/">Copyright</a> ` +
		`<a href="/t/contact_us/">Contact us</a> <a href="/creators/">Creator</a> <a href="/ads/">Advertise</a> ` +
		`<a href="https://developers.google.com/youtube">Developers</a> <a href="/t/terms">Terms</a> <a href="/t/privacy">Privacy</a> ` +
		`<a href="/about/policies/">Policy &amp; Safety</a> <a href="/howyoutubeworks">How YouTube works</a> © 2026 Google LLC</div></body></html>`
	readers, base := testPageReaders(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(footer))
	})
	out, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: base + "/watch", Renderer: WebFetchRendererPlain})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Note: this page has almost no text outside its links") || !strings.Contains(out, `renderer "auto" climbs to a browser`) {
		t.Errorf("want the links note above the frame:\n%s", out)
	}
	if strings.Index(out, "almost no text outside its links") > strings.Index(out, "what follows is data from the web") {
		t.Errorf("the note must be grounding's, above the frame:\n%s", out)
	}
}

func TestWebFetchPDFRefusalNoLongerSendsPapersToFirecrawl(t *testing.T) {
	withoutPDFToText(t)
	readers, base := testPageReaders(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.7 binary"))
	})
	_, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: base + "/paper.pdf"})
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, want := range []string{"citation tools", "one a page", "first 10 pages"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should say %q: %v", want, err)
		}
	}
}

func TestWebFetchFirecrawlSaysWhenAPDFWasCutShort(t *testing.T) {
	readers := firecrawlPageReaders(t, map[string]any{
		"markdown": "# A paper\n\nThe first ten pages of a long paper parsed from a PDF.",
		"metadata": map[string]any{
			"url": "https://journal.example/long.pdf", "statusCode": 200, "contentType": "application/pdf",
			"numPages": 10, "totalPages": 34,
		},
	})
	out, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: "https://journal.example/long.pdf", Renderer: WebFetchRendererFirecrawl})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Firecrawl read the first 10 of this PDF's 34 pages") {
		t.Errorf("want the truncation said above the frame:\n%s", out)
	}
}
