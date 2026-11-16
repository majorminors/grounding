package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/backends"
)

func TestAFailuresWarningKeepsTheReasonNotTheURL(t *testing.T) {
	warnings := warningsFromDiagnostics([]backends.Diagnostic{{Backend: "crossref",
		Error: `crossref search error: executing request: Get "https://api.crossref.org/works?query=the+new+monogamy&rows=5": dial tcp 100.51.192.124:443: i/o timeout`}})
	if len(warnings) != 1 || strings.Contains(warnings[0], "api.crossref.org") || !strings.Contains(warnings[0], "i/o timeout") {
		t.Errorf("warning = %q", warnings)
	}
}

func TestWarningsNameEverySourceThatFailedRestedOrCouldNotRun(t *testing.T) {
	warnings := warningsFromDiagnostics([]backends.Diagnostic{
		{Backend: "openalex", RawCount: 5, FilteredCount: 3},
		{Backend: "crossref", RawCount: 0},
		{Backend: "semantic_scholar", Skipped: true, SkippedReason: "cooldown (41s remaining)"},
		{Backend: "europepmc", Error: "europepmc: " + strings.Repeat("x", 400)},
		{Backend: "consensus", Skipped: true, SkippedReason: "CONSENSUS_APP_KEY not configured"},
	})
	if len(warnings) != 3 {
		t.Fatalf("warnings = %q, want three (a source that found nothing is not a warning)", warnings)
	}
	for i, want := range []string{"semantic_scholar is resting", "europepmc failed", "consensus was not searched: CONSENSUS_APP_KEY"} {
		if !strings.HasPrefix(warnings[i], want) {
			t.Errorf("warning %d = %q, want it to begin %q", i, warnings[i], want)
		}
	}
	if len([]rune(warnings[1])) > warningMaxErrorRunes+80 {
		t.Errorf("a failure's warning is not one line: %d runes", len([]rune(warnings[1])))
	}
}

// The diagnostics a caller sees only with debug are always in the log.
func TestEveryCatalogueSearchLeavesALineInTheDaysLog(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("GROUNDING_RESEARCH_RUN_RECORDS_DIR", directory)
	registerFakeAddOnForThisTest(t, PaperSearchSource{Name: "fake_add_on"})
	t.Setenv("CONSENSUS_APP_KEY", "")

	ctx := auth.WithTier(context.Background(), auth.TierFull)
	if _, err := SearchCatalogue(ctx, "tribe", "academic", 3, []string{"fake_add_on", "consensus"}, backends.SearchFilters{YearMin: 1990}); err != nil {
		t.Fatal(err)
	}
	if _, err := SearchCatalogue(ctx, "tribe", "academic", 3, []string{"no_such_backend"}, backends.SearchFilters{}); err == nil {
		t.Fatal("an unknown backend gave no error")
	}

	file, err := os.Open(filepath.Join(directory, catalogueSearchLogSubdirectory, time.Now().UTC().Format(time.DateOnly)+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var entries []catalogueSearchLogEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry catalogueSearchLogEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("line %q: %v", scanner.Text(), err)
		}
		entries = append(entries, entry)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d lines, want 2", len(entries))
	}
	found := entries[0]
	if found.Query != "tribe" || found.Tier != "full" || found.Papers != 1 || found.Filters.YearMin != 1990 || len(found.Diagnostics) != 2 || len(found.Warnings) != 1 {
		t.Errorf("first line = %+v", found)
	}
	if entries[1].Error == "" {
		t.Errorf("the failed search's line has no error: %+v", entries[1])
	}
}
