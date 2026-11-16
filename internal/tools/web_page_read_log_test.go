package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readWebPageReadLog(t *testing.T, directory string) []webPageReadLogEntry {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(directory, "*.jsonl"))
	var entries []webPageReadLogEntry
	for _, file := range files {
		contents, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
			var entry webPageReadLogEntry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("a log line is not JSON: %v\n%s", err, line)
			}
			entries = append(entries, entry)
		}
	}
	return entries
}

func TestPageReadLogRecordsTheClimbAndTheBeginningOfWhatWasRead(t *testing.T) {
	directory := t.TempDir()
	setUp := &ladderSetUp{site: servesHTML(botCheckPagesServedInPlaceOfThePage["pubmed-central"]), crawl4aiResult: crawl4aiReadTheArticle}
	readers := setUp.readers(t)
	readers.readLogDirectory = directory
	if _, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL}); err != nil {
		t.Fatal(err)
	}
	entries := readWebPageReadLog(t, directory)
	if len(entries) != 1 {
		t.Fatalf("want one line, got %d", len(entries))
	}
	entry := entries[0]
	if entry.Tool != "web_fetch" || entry.URL != ladderTestPageURL || entry.RendererAsked != "auto" || entry.ReadBy != "crawl4ai" {
		t.Errorf("unexpected entry %+v", entry)
	}
	if len(entry.Rungs) != 2 || entry.Rungs[0].Renderer != "plain" || entry.Rungs[0].Outcome != "blocked" ||
		!strings.Contains(entry.Rungs[0].Reason, "checking your browser") ||
		entry.Rungs[1].Renderer != "crawl4ai" || entry.Rungs[1].Outcome != "read" {
		t.Errorf("unexpected rungs %+v", entry.Rungs)
	}
	if !strings.Contains(entry.Beginning, "means-ends analysis") || len([]rune(entry.Beginning)) > webPageReadLogBeginningRunes+1 || entry.Characters == 0 {
		t.Errorf("want the first characters read and the count, got %q (%d)", entry.Beginning, entry.Characters)
	}
	if len(entry.Notes) == 0 || entry.FirecrawlCredits != 0 {
		t.Errorf("want the climb note and no credits: %+v", entry)
	}
}

func TestPageReadLogRecordsARungNotTried(t *testing.T) {
	directory := t.TempDir()
	setUp := &ladderSetUp{site: answersStatus(http.StatusForbidden)}
	readers := setUp.readers(t)
	readers.readLogDirectory = directory
	readers.fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL})
	rungs := readWebPageReadLog(t, directory)[0].Rungs
	if len(rungs) != 3 || rungs[1].Outcome != "not-tried" || rungs[2].Renderer != "firecrawl" || rungs[2].Reason != "not set up" {
		t.Errorf("want the passed-over rungs said: %+v", rungs)
	}
}

func TestPageReadLogCountsFirecrawlCredits(t *testing.T) {
	directory := t.TempDir()
	setUp := &ladderSetUp{site: answersStatus(http.StatusForbidden), firecrawlData: firecrawlReadTheArticle}
	readers := setUp.readers(t)
	readers.readLogDirectory = directory
	if _, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL}); err != nil {
		t.Fatal(err)
	}
	entry := readWebPageReadLog(t, directory)[0]
	if entry.ReadBy != "firecrawl" || entry.FirecrawlCredits != 1 {
		t.Errorf("want one credit, read by Firecrawl: %+v", entry)
	}
}

func TestPageReadLogRecordsAFailureWithItsError(t *testing.T) {
	directory := t.TempDir()
	setUp := &ladderSetUp{site: answersStatus(http.StatusNotFound)}
	readers := setUp.readers(t)
	readers.readLogDirectory = directory
	if _, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL, Renderer: WebFetchRendererPlain}); err == nil {
		t.Fatal("want an error")
	}
	entry := readWebPageReadLog(t, directory)[0]
	if entry.ReadBy != "" || !strings.Contains(entry.Error, "404") || entry.RendererAsked != "plain" ||
		len(entry.Rungs) != 1 || entry.Rungs[0].Outcome != "stopped" {
		t.Errorf("unexpected failure entry %+v", entry)
	}
}

func TestPageReadLogThatCannotBeWrittenDoesNotFailTheRead(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "a-file")
	os.WriteFile(blocker, nil, 0o600)
	setUp := &ladderSetUp{site: servesHTML(articleHTML)}
	readers := setUp.readers(t)
	readers.readLogDirectory = filepath.Join(blocker, "page-reads")
	if _, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL}); err != nil {
		t.Errorf("the read must not fail for its log: %v", err)
	}
}

func TestPageReadLogDeletesDaysPastTheRetention(t *testing.T) {
	directory := t.TempDir()
	for _, day := range []string{"2026-07-01", "2026-08-09", "2026-08-10"} {
		os.WriteFile(filepath.Join(directory, day+".jsonl"), []byte("{}\n"), 0o600)
	}
	os.WriteFile(filepath.Join(directory, "notes.txt"), nil, 0o600)
	entry := newWebPageReadLogEntry("web_fetch", "https://a.example/", "", time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC))
	if err := writeWebPageReadLogEntry(directory, 90*24*time.Hour, entry); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"2026-07-01.jsonl": false, "2026-08-09.jsonl": false, "2026-08-10.jsonl": true, "2026-11-08.jsonl": true, "notes.txt": true} {
		if _, err := os.Stat(filepath.Join(directory, name)); (err == nil) != want {
			t.Errorf("%s: want kept=%v", name, want)
		}
	}
}
