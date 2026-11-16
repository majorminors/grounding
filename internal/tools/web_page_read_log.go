package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"btr.mt/grounding/internal/backends"
)

// Every page a web tool reads leaves one line in a log, for quality
// control of page reading: which renderer read it, why the ones before
// it failed, what Firecrawl cost, and the first few hundred characters
// of what came back, so a bot check or a page of script passed off as
// the page can be caught after the fact, as the bake-off caught them
// (LOG "Page-reading bake-off").
//
// A day is one file, page-reads/YYYY-MM-DD.jsonl, beside the research
// run records (ResearchRunRecordsDirectory), so it shares their privacy
// (the server user's alone), their retention and any copy of them
// into backups. A line that cannot be written never fails the
// read; it is said on stderr.

const (
	webPageReadLogSubdirectory     = "page-reads"
	webPageReadLogBeginningRunes   = 300
	webPageReadLogReasonMaxRunes   = 300
	webPageReadOutcomeRead         = "read"
	webPageReadOutcomeThin         = "thin"
	webPageReadOutcomeBlocked      = "blocked"
	webPageReadOutcomeStopped      = "stopped"
	webPageReadOutcomeNotAttempted = "not-tried"
)

// webPageReadLogEntry is one line of the log.
type webPageReadLogEntry struct {
	Time            time.Time `json:"time"`
	Tool            string    `json:"tool"`
	Tier            string    `json:"tier,omitempty"`
	ResearchSession string    `json:"research_session,omitempty"`
	URL             string    `json:"url"`
	RendererAsked   string    `json:"renderer_asked"`
	// ReadBy is the renderer whose text the caller got; empty on failure.
	ReadBy           string            `json:"read_by,omitempty"`
	FinalURL         string            `json:"final_url,omitempty"`
	ContentType      string            `json:"content_type,omitempty"`
	Title            string            `json:"title,omitempty"`
	Characters       int               `json:"characters,omitempty"`
	Beginning        string            `json:"beginning,omitempty"`
	Notes            []string          `json:"notes,omitempty"`
	Error            string            `json:"error,omitempty"`
	FirecrawlCredits int               `json:"firecrawl_credits,omitempty"`
	Milliseconds     int64             `json:"ms"`
	Rungs            []webPageReadRung `json:"rungs"`
	started          time.Time
}

// webPageReadRung is one renderer's attempt at the page.
type webPageReadRung struct {
	Renderer     string `json:"renderer"`
	Outcome      string `json:"outcome"`
	Reason       string `json:"reason,omitempty"`
	Milliseconds int64  `json:"ms,omitempty"`
	FromCache    bool   `json:"from_cache,omitempty"`
}

func newWebPageReadLogEntry(tool, pageURL, rendererAsked string, now time.Time) *webPageReadLogEntry {
	if rendererAsked == "" {
		rendererAsked = WebFetchRendererAuto
	}
	return &webPageReadLogEntry{Time: now.UTC(), Tool: tool, URL: pageURL, RendererAsked: rendererAsked, started: now}
}

// addRung records what one renderer made of the page.
func (entry *webPageReadLogEntry) addRung(renderer string, result webPageRungResult, text *WebPageText, err error, took time.Duration) {
	if entry == nil {
		return
	}
	rung := webPageReadRung{Renderer: renderer, Milliseconds: took.Milliseconds()}
	switch result.outcome {
	case webPageRungRead:
		rung.Outcome = webPageReadOutcomeRead
	case webPageRungDoubtful:
		rung.Outcome, rung.Reason = webPageReadOutcomeThin, result.reason
	case webPageRungBlocked:
		rung.Outcome, rung.Reason = webPageReadOutcomeBlocked, result.reason
	case webPageRungFinal:
		rung.Outcome = webPageReadOutcomeStopped
	}
	if err != nil && rung.Reason == "" {
		rung.Reason = firstCharacters(err.Error(), webPageReadLogReasonMaxRunes)
	}
	if text != nil {
		rung.FromCache = text.fromCache
		entry.FirecrawlCredits += text.firecrawlCreditsSpent
	} else if renderer == WebFetchRendererFirecrawl && backends.FirecrawlScrapeFailureSpentACredit(err) {
		// A failed scrape keeps the credit the cap reserved for it.
		entry.FirecrawlCredits++
	}
	entry.Rungs = append(entry.Rungs, rung)
}

// addRungNotAttempted records a renderer the ladder passed over.
func (entry *webPageReadLogEntry) addRungNotAttempted(renderer, reason string) {
	if entry == nil {
		return
	}
	entry.Rungs = append(entry.Rungs, webPageReadRung{Renderer: renderer, Outcome: webPageReadOutcomeNotAttempted, Reason: reason})
}

// finish records how the read ended.
func (entry *webPageReadLogEntry) finish(text *WebPageText, err error, now time.Time) {
	entry.Milliseconds = now.Sub(entry.started).Milliseconds()
	if err != nil {
		entry.Error = firstCharacters(err.Error(), webPageReadLogReasonMaxRunes*2)
		return
	}
	entry.ReadBy, entry.FinalURL, entry.ContentType, entry.Title = text.Renderer, text.FinalURL, text.ContentType, text.Title
	entry.Characters = len([]rune(text.Text))
	entry.Beginning = firstCharacters(text.Text, webPageReadLogBeginningRunes)
	entry.Notes = text.NotesFromGrounding
}

// writeWebPageReadLogEntry appends entry to its day's file under
// directory (appendLineToTheDaysLog).
func writeWebPageReadLogEntry(directory string, keep time.Duration, entry *webPageReadLogEntry) error {
	return appendLineToTheDaysLog(directory, keep, entry.Time, entry)
}

// appendLineToTheDaysLog appends value as one JSON line to the file of
// day's date under directory, under a lock (research runs' child
// processes write to the same file), and on a day's first line deletes
// days older than keep. An empty directory writes nothing. Every
// day-file log (page reads, catalogue searches) writes through here.
func appendLineToTheDaysLog(directory string, keep time.Duration, day time.Time, value any) error {
	if directory == "" {
		return nil
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	line, err := json.Marshal(value)
	if err != nil {
		return err
	}
	path := filepath.Join(directory, day.UTC().Format(time.DateOnly)+".jsonl")
	_, statErr := os.Stat(path)
	firstLineOfTheDay := os.IsNotExist(statErr)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	if _, err := file.Write(append(line, '\n')); err != nil {
		return err
	}
	if firstLineOfTheDay && keep > 0 {
		return deleteWebPageReadLogsOlderThan(directory, day.Add(-keep))
	}
	return nil
}

// deleteWebPageReadLogsOlderThan deletes days before cutoff's day.
func deleteWebPageReadLogsOlderThan(directory string, cutoff time.Time) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	cutoffDay := cutoff.UTC().Format(time.DateOnly)
	for _, file := range entries {
		day, isLog := strings.CutSuffix(file.Name(), ".jsonl")
		if !isLog || len(day) != len(cutoffDay) || day >= cutoffDay {
			continue
		}
		if err := os.Remove(filepath.Join(directory, file.Name())); err != nil {
			return fmt.Errorf("deleting an old page-read log: %w", err)
		}
	}
	return nil
}
