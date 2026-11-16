package backends

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testDailyCreditCap(t *testing.T, credits int) (*firecrawlDailyCreditCap, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 7, 23, 0, 0, 0, time.UTC)
	return &firecrawlDailyCreditCap{credits: credits, ledgerPath: filepath.Join(t.TempDir(), "state", "spent"), now: func() time.Time { return now }}, &now
}

func TestFirecrawlDailyCreditCapRefusesPastTheCapAndResetsNextDay(t *testing.T) {
	ledger, now := testDailyCreditCap(t, 3)
	for i := 0; i < 3; i++ {
		if err := ledger.reserve(1); err != nil {
			t.Fatalf("credit %d: %v", i+1, err)
		}
	}
	if err := ledger.reserve(1); !errors.Is(err, ErrFirecrawlDailyCreditCapSpent) {
		t.Errorf("want the cap refused, got %v", err)
	}
	*now = now.Add(2 * time.Hour) // past midnight UTC
	if err := ledger.reserve(1); err != nil {
		t.Errorf("a new day should have a new cap: %v", err)
	}
	if spent, _ := ledger.spentToday(); spent != 1 {
		t.Errorf("want 1 spent on the new day, got %d", spent)
	}
}

func TestFirecrawlDailyCreditCapCountsPDFPagesEvenPastTheCap(t *testing.T) {
	ledger, _ := testDailyCreditCap(t, 5)
	ledger.reserve(1)
	ledger.add(10)
	if spent, _ := ledger.spentToday(); spent != 11 {
		t.Errorf("want 11, got %d", spent)
	}
	if err := ledger.reserve(1); !errors.Is(err, ErrFirecrawlDailyCreditCapSpent) {
		t.Errorf("want refused once over, got %v", err)
	}
}

// Research runs start child processes, each with its own backend, all
// counting in one file; goroutines with their own ledgers stand in.
func TestFirecrawlDailyCreditCapHoldsAcrossConcurrentCounters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spent")
	var granted int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ledger := &firecrawlDailyCreditCap{credits: 25, ledgerPath: path, now: time.Now}
			if ledger.reserve(1) == nil {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if granted != 25 {
		t.Errorf("want exactly 25 credits granted, got %d", granted)
	}
}

func TestFirecrawlDailyCreditCapThatCannotBeKeptRefuses(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "a-file")
	os.WriteFile(blocker, nil, 0o600)
	ledger := &firecrawlDailyCreditCap{credits: 25, ledgerPath: filepath.Join(blocker, "spent"), now: time.Now}
	if err := ledger.reserve(1); err == nil {
		t.Error("an uncounted Firecrawl is an uncapped one: want a refusal")
	}
}

func TestFirecrawlScrapeStopsAtTheCapWithoutCallingFirecrawl(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"success":true,"data":{"rawHtml":"<p>x</p>","metadata":{"url":"https://a.example/","statusCode":200,"contentType":"application/pdf","numPages":4}}}`))
	}))
	defer server.Close()
	SetRateLimit("firecrawl", 1000, 10)
	path := filepath.Join(t.TempDir(), "spent")
	firecrawl := NewFirecrawl(NewClient(), "fc-key").WithBaseURLForTests(server.URL).WithDailyCreditCap(5, path)
	if _, err := firecrawl.Scrape(context.Background(), "https://a.example/one.pdf"); err != nil {
		t.Fatal(err)
	}
	// One credit for the scrape and four for the PDF's pages: five, the cap.
	if _, err := firecrawl.Scrape(context.Background(), "https://a.example/two.pdf"); !errors.Is(err, ErrFirecrawlDailyCreditCapSpent) {
		t.Errorf("want the cap refused, got %v", err)
	}
	if calls != 1 {
		t.Errorf("want Firecrawl called once, got %d", calls)
	}
	if health := firecrawl.HealthCheck(context.Background()); health.Message != "key configured (not probed: every scrape spends credits); 5 of today's 5-credit cap spent (UTC)" {
		t.Errorf("unexpected health %q", health.Message)
	}
}

// A credit count that cannot be kept refuses the scrape before anything
// is sent, so the failure must not be logged as a spent credit (a
// read-only state directory once logged one that was never spent).
func TestFirecrawlScrapeRefusedForWantOfACreditCountSpendsNoCredit(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
	}))
	defer server.Close()
	SetRateLimit("firecrawl", 1000, 10)
	blocker := filepath.Join(t.TempDir(), "a-file")
	os.WriteFile(blocker, nil, 0o600)
	firecrawl := NewFirecrawl(NewClient(), "fc-key").WithBaseURLForTests(server.URL).WithDailyCreditCap(25, filepath.Join(blocker, "state", "spent"))
	_, err := firecrawl.Scrape(context.Background(), "https://a.example/page")
	if err == nil {
		t.Fatal("want the scrape refused")
	}
	if calls != 0 {
		t.Errorf("want Firecrawl not called, got %d calls", calls)
	}
	if FirecrawlScrapeFailureSpentACredit(err) {
		t.Errorf("a scrape refused before sending spent no credit, but was counted as one: %v", err)
	}
}

func TestFirecrawlScrapeFailureSpentACreditOnlyForFailuresAfterSending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false,"error":"blocked"}`))
	}))
	defer server.Close()
	SetRateLimit("firecrawl", 1000, 10)
	path := filepath.Join(t.TempDir(), "spent")
	firecrawl := NewFirecrawl(NewClient(), "fc-key").WithBaseURLForTests(server.URL).WithDailyCreditCap(1, path)
	_, sentAndFailed := firecrawl.Scrape(context.Background(), "https://a.example/page")
	if !FirecrawlScrapeFailureSpentACredit(sentAndFailed) {
		t.Errorf("a scrape Firecrawl answered with a failure keeps its credit: %v", sentAndFailed)
	}
	_, capSpent := firecrawl.Scrape(context.Background(), "https://a.example/other")
	if !errors.Is(capSpent, ErrFirecrawlDailyCreditCapSpent) || FirecrawlScrapeFailureSpentACredit(capSpent) {
		t.Errorf("want the cap refused and no credit counted, got %v", capSpent)
	}
	_, notConfigured := NewFirecrawl(NewClient(), "").Scrape(context.Background(), "https://a.example/")
	if !errors.Is(notConfigured, ErrFirecrawlNotConfigured) || FirecrawlScrapeFailureSpentACredit(notConfigured) {
		t.Errorf("want not configured and no credit counted, got %v", notConfigured)
	}
	_, notPublic := firecrawl.Scrape(context.Background(), "http://127.0.0.1/")
	if !errors.Is(notPublic, ErrAddressNotPublic) || FirecrawlScrapeFailureSpentACredit(notPublic) {
		t.Errorf("want the address refused and no credit counted, got %v", notPublic)
	}
}
