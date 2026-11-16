package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

// Every catalogue search leaves one line in a log, whoever called it
// (the CLI, an MCP client, a research run), so use shows which sources
// work: the diagnostics a caller sees only with debug are always here.
// A day is one file, catalogue-searches/YYYY-MM-DD.jsonl, beside the
// page-read log and the research run records, sharing their privacy
// and retention. The server's access log covers MCP calls only; this
// is the CLI's record too. A line that cannot be written never fails
// the search; it is said on stderr.

const catalogueSearchLogSubdirectory = "catalogue-searches"

// catalogueSearchLogEntry is one line of the log.
type catalogueSearchLogEntry struct {
	Time            time.Time              `json:"time"`
	Tier            string                 `json:"tier,omitempty"`
	ResearchSession string                 `json:"research_session,omitempty"`
	Query           string                 `json:"query"`
	SourceType      string                 `json:"source_type,omitempty"`
	BackendsNamed   []string               `json:"backends_named,omitempty"`
	Limit           int                    `json:"limit"`
	Filters         backends.SearchFilters `json:"filters"`
	Papers          int                    `json:"papers"`
	Books           int                    `json:"books"`
	Articles        int                    `json:"articles"`
	WithOpenAccess  int                    `json:"with_open_access"`
	Diagnostics     []backends.Diagnostic  `json:"diagnostics,omitempty"`
	Warnings        []string               `json:"warnings,omitempty"`
	EmptyExplained  string                 `json:"empty_result_explanation,omitempty"`
	Error           string                 `json:"error,omitempty"`
	Milliseconds    int64                  `json:"ms"`
}

// catalogueSearchLogDirectory is where the log goes, or "" for none.
func catalogueSearchLogDirectory(cfg config.Config) string {
	recordsDirectory := ResearchRunRecordsDirectory(cfg)
	if recordsDirectory == "" {
		return ""
	}
	return filepath.Join(recordsDirectory, catalogueSearchLogSubdirectory)
}

func logCatalogueSearch(ctx context.Context, started time.Time, query, sourceType string, limit int, named []string, filters backends.SearchFilters, result *backends.ContextResult, searchErr error) {
	cfg := config.LoadConfig()
	entry := catalogueSearchLogEntry{
		Time:          started.UTC(),
		Tier:          auth.GetTier(ctx).String(),
		Query:         query,
		SourceType:    sourceType,
		BackendsNamed: named,
		Limit:         limit,
		Filters:       filters,
		Milliseconds:  time.Since(started).Milliseconds(),
	}
	if ledger := ResearchLedgerFromContext(ctx); ledger != nil {
		entry.ResearchSession = ledger.SessionID
	}
	if searchErr != nil {
		entry.Error = searchErr.Error()
	}
	if result != nil {
		entry.Papers, entry.Books, entry.Articles = len(result.Papers), len(result.Books), len(result.Articles)
		for _, record := range append(append([]backends.Paper(nil), result.Papers...), result.Books...) {
			if record.Availability != nil && record.Availability.OpenAccessURL != "" {
				entry.WithOpenAccess++
			}
		}
		entry.Diagnostics, entry.Warnings, entry.EmptyExplained = result.Diagnostics, result.Warnings, result.EmptyResultExplanation
	}
	if err := appendLineToTheDaysLog(catalogueSearchLogDirectory(cfg), researchRunRecordsKeepDuration(cfg), started, entry); err != nil {
		fmt.Fprintf(os.Stderr, "catalogue search log: %v\n", err)
	}
}
