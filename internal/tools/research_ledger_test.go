package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"btr.mt/grounding/internal/backends"
)

func newTestResearchLedger() *ResearchLedger {
	return NewResearchLedger(time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))
}

func TestResearchLedgerSessionIDsAreDistinct(t *testing.T) {
	first, second := newTestResearchLedger(), newTestResearchLedger()
	if !strings.HasPrefix(first.SessionID, "research-") || first.SessionID == second.SessionID {
		t.Errorf("session IDs %q and %q", first.SessionID, second.SessionID)
	}
}

func TestResearchFetchTakesOnlyURLsThatTheRunsResultsProduced(t *testing.T) {
	ledger := newTestResearchLedger()
	ledger.RecordWebResults("web_search", []backends.WebResult{
		{URL: "https://example.org/article", Title: "An article", Text: "Some text."},
	})
	ledger.RecordPapers("search_catalogue", []backends.Paper{
		{Title: "A paper", DOI: "10.1037/0022-3514.52.3.511", URL: "https://journals.example.com/paper/1"},
	})
	ledger.Record("download_handles", ResearchLedgerEntry{
		DOI: "10.1037/0022-3514.52.3.511", OpenAccessURL: "https://repository.example.edu/oa/1.pdf",
	})

	allowed := []string{
		"https://example.org/article",
		"https://journals.example.com/paper/1",
		"https://repository.example.edu/oa/1.pdf",
		"https://doi.org/10.1037/0022-3514.52.3.511",
		"https://dx.doi.org/10.1037/0022-3514.52.3.511",
	}
	for _, pageURL := range allowed {
		if err := ledger.URLMayBeFetched(pageURL); err != nil {
			t.Errorf("%s refused: %v", pageURL, err)
		}
	}

	refused := []string{
		// A page's link, never returned by a search.
		"https://attacker.example.net/collect?q=the+caller%27s+question",
		// The same host as a result, but a different page.
		"https://example.org/other-page",
		// A result's page with a query string added: where a leak rides.
		"https://example.org/article?q=the+caller%27s+question",
	}
	for _, pageURL := range refused {
		if err := ledger.URLMayBeFetched(pageURL); !errors.Is(err, ErrURLNotFromResearchResults) {
			t.Errorf("%s: got %v, want the not-from-results refusal", pageURL, err)
		}
	}
}

func TestResearchFetchTreatsHarmlessSpellingsOfAResultAsTheSamePage(t *testing.T) {
	ledger := newTestResearchLedger()
	ledger.RecordWebResults("web_search", []backends.WebResult{{URL: "https://Example.org/article/"}})
	for _, pageURL := range []string{
		"https://example.org/article",
		"http://example.org/article",
		"https://example.org:443/article",
		"https://example.org/article#section-2",
		"https://EXAMPLE.ORG./article/",
	} {
		if err := ledger.URLMayBeFetched(pageURL); err != nil {
			t.Errorf("%s refused: %v", pageURL, err)
		}
	}
	if err := ledger.URLMayBeFetched("https://example.org:8443/article"); err == nil {
		t.Error("a different port should be a different page")
	}
}

func TestResearchFetchJudgesADOIResolverURLByItsDOIWhateverItsCase(t *testing.T) {
	ledger := newTestResearchLedger()
	ledger.RecordPapers("search_catalogue", []backends.Paper{{DOI: "10.1525/CALIFORNIA/9780520239333.001.0001"}})
	if err := ledger.URLMayBeFetched("https://doi.org/10.1525/california/9780520239333.001.0001"); err != nil {
		t.Errorf("lowercased DOI refused: %v", err)
	}
	if err := ledger.URLMayBeFetched("https://doi.org/10.1525/another.0001"); !errors.Is(err, ErrURLNotFromResearchResults) {
		t.Errorf("a DOI not in the ledger: got %v", err)
	}
}

func TestResearchFetchStillRefusesPrivateAddressesEvenIfAToolReturnedThem(t *testing.T) {
	ledger := newTestResearchLedger()
	ledger.RecordWebResults("web_search", []backends.WebResult{{URL: "http://localhost/admin"}, {URL: "http://100.64.0.2/"}})
	for _, pageURL := range []string{"http://localhost/admin", "http://100.64.0.2/"} {
		err := ledger.URLMayBeFetched(pageURL)
		if err == nil || errors.Is(err, ErrURLNotFromResearchResults) {
			t.Errorf("%s: got %v, want the address guard's refusal", pageURL, err)
		}
	}
}

func TestResearchLedgerMergesASourceFoundByDOIAndByURL(t *testing.T) {
	ledger := newTestResearchLedger()
	ledger.RecordPapers("search_catalogue", []backends.Paper{
		{Title: "Doing Gender", DOI: "https://doi.org/10.1177/0891243287001002002", Abstract: "Gender is an accomplishment."},
	})
	ledger.RecordPapers("check_citation", []backends.Paper{
		{DOI: "10.1177/0891243287001002002", URL: "https://journals.sagepub.com/doi/10.1177/0891243287001002002"},
	})
	sources := ledger.Sources()
	if len(sources) != 1 {
		t.Fatalf("got %d sources, want 1: %+v", len(sources), sources)
	}
	source := sources[0]
	if source.DOI != "10.1177/0891243287001002002" {
		t.Errorf("DOI %q, want it normalised", source.DOI)
	}
	if strings.Join(source.RetrievedBy, ",") != "search_catalogue,check_citation" {
		t.Errorf("retrieved by %v", source.RetrievedBy)
	}
	if found, ok := ledger.SourceByURLOrDOI("https://journals.sagepub.com/doi/10.1177/0891243287001002002", ""); !ok || found.Title != "Doing Gender" {
		t.Errorf("lookup by URL: %+v, %v", found, ok)
	}
	if _, ok := ledger.SourceByURLOrDOI("https://dx.doi.org/10.1177/0891243287001002002", ""); !ok {
		t.Error("a resolver URL should find the source by its DOI")
	}
}

func TestResearchLedgerFindsAFetchedPageByItsRedirectedAddressWithoutMakingThatFetchable(t *testing.T) {
	ledger := newTestResearchLedger()
	ledger.RecordWebResults("web_search", []backends.WebResult{{URL: "https://short.example/abc"}})
	ledger.RecordFetchedPage(&WebPageText{
		RequestedURL: "https://short.example/abc", FinalURL: "https://long.example.org/the-article",
		Title: "The article", Text: "The full text of the article.",
	})
	source, ok := ledger.SourceByURLOrDOI("https://long.example.org/the-article", "")
	if !ok || len(source.TextShownToResearcher) != 1 {
		t.Fatalf("lookup by final URL: %+v, %v", source, ok)
	}
	if err := ledger.URLMayBeFetched("https://long.example.org/the-article"); err == nil {
		t.Error("a redirect's target should not become fetchable")
	}
}

func TestResearchLedgerKeepsARepeatedTextOnce(t *testing.T) {
	ledger := newTestResearchLedger()
	page := &WebPageText{RequestedURL: "https://example.org/a", FinalURL: "https://example.org/a", Text: "the page"}
	ledger.RecordFetchedPage(page)
	ledger.RecordFetchedPage(page)
	if texts := ledger.Sources()[0].TextShownToResearcher; len(texts) != 1 {
		t.Errorf("got %d texts", len(texts))
	}
}

func TestResearchLedgerIgnoresEntriesWithNoAddress(t *testing.T) {
	ledger := newTestResearchLedger()
	ledger.Record("search_catalogue", ResearchLedgerEntry{Title: "No URL or DOI", Text: "text"})
	ledger.Record("search_catalogue", ResearchLedgerEntry{URL: "javascript:alert(1)"})
	if sources := ledger.Sources(); len(sources) != 0 {
		t.Errorf("got %+v", sources)
	}
}

func TestResearchLedgerIsSafeForConcurrentTools(t *testing.T) {
	ledger := newTestResearchLedger()
	var group sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for index := 0; index < 50; index++ {
				text := fmt.Sprintf("text %d-%d", worker, index)
				ledger.RecordWebResults("web_search", []backends.WebResult{{URL: "https://example.org/shared", Text: text}})
				_ = ledger.URLMayBeFetched("https://example.org/shared")
			}
		}(worker)
	}
	group.Wait()
	if sources := ledger.Sources(); len(sources) != 1 || len(sources[0].TextShownToResearcher) != 400 {
		t.Errorf("got %d sources", len(sources))
	}
}

func TestResearchLedgerSnapshotRoundTripKeepsWhatVerificationAndFetchingNeed(t *testing.T) {
	ledger := newTestResearchLedger()
	ledger.RecordPapers("search_catalogue", []backends.Paper{{Title: "A paper", DOI: "10.1/ABC", URL: "https://journal.example/a", Abstract: "An abstract."}})
	ledger.RecordPapers("check_citation", []backends.Paper{{DOI: "10.1/abc"}})
	ledger.RecordWebResults("web_search", []backends.WebResult{{URL: "https://short.example/x", Text: "snippet"}})
	ledger.RecordFetchedPage(&WebPageText{RequestedURL: "https://short.example/x", FinalURL: "https://long.example/article", Text: "full text"})
	ledger.CountToolCall("web_fetch", "")
	ledger.CountToolCall("web_fetch", "")

	path := t.TempDir() + "/ledger.json"
	if err := ledger.WriteSnapshotFile(path); err != nil {
		t.Fatal(err)
	}
	restored, err := ReadResearchLedgerSnapshotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if restored.SessionID != ledger.SessionID || restored.ToolCalls() != 2 {
		t.Errorf("session %q, tool calls %d", restored.SessionID, restored.ToolCalls())
	}
	original, _ := json.Marshal(ledger.Sources())
	roundTripped, _ := json.Marshal(restored.Sources())
	if string(original) != string(roundTripped) {
		t.Errorf("sources changed:\n got %s\nwant %s", roundTripped, original)
	}
	if _, found := restored.SourceByURLOrDOI("https://long.example/article", ""); !found {
		t.Error("a redirect's target should still find its source")
	}
	if err := restored.URLMayBeFetched("https://doi.org/10.1/abc"); err != nil {
		t.Errorf("a DOI's resolver URL should stay fetchable: %v", err)
	}
	if err := restored.URLMayBeFetched("https://long.example/article"); err == nil {
		t.Error("a redirect's target should stay unfetchable")
	}
}

func TestURLsInTheQuestionAreFoundWithoutTheirPunctuation(t *testing.T) {
	question := `What does https://example.org/report say (see https://example.org/a_(b)), and is http://example.net/x?y=1. right? Not ftp://example.com or "https://example.com/quoted".`
	got := URLsInText(question)
	want := []string{"https://example.org/report", "https://example.org/a_(b)", "http://example.net/x?y=1", "https://example.com/quoted"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestAURLInTheQuestionIsFetchableButNotASource(t *testing.T) {
	ledger := newTestResearchLedger()
	ledger.AllowFetching(URLsInText("What does https://example.org/report/ say?"))
	if err := ledger.URLMayBeFetched("https://example.org/report"); err != nil {
		t.Errorf("the question's URL refused: %v", err)
	}
	if len(ledger.Sources()) != 0 {
		t.Error("a URL in the question is not a source until it is fetched")
	}
	if err := ledger.URLMayBeFetched("https://example.org/report?q=leak"); !errors.Is(err, ErrURLNotFromResearchResults) {
		t.Errorf("the question's URL with a query added must still be refused: %v", err)
	}
}
