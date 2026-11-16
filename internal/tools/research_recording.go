package tools

import (
	"context"
	"time"

	"btr.mt/grounding/internal/backends"
)

// In a research run the ledger travels in the context, so the tools
// record into it, and web_fetch checks it, whichever way they are
// called: by the API runner calling the MCP handlers in-process, or by
// a child `grounding serve --research-session` serving Claude Code.
// Outside a run there is no ledger and the tools behave as ever.

type researchLedgerContextKey struct{}

// WithResearchLedger returns a context carrying ledger.
func WithResearchLedger(ctx context.Context, ledger *ResearchLedger) context.Context {
	return context.WithValue(ctx, researchLedgerContextKey{}, ledger)
}

// ResearchLedgerFromContext returns the run's ledger, or nil outside a
// research run.
func ResearchLedgerFromContext(ctx context.Context) *ResearchLedger {
	ledger, _ := ctx.Value(researchLedgerContextKey{}).(*ResearchLedger)
	return ledger
}

// SearchCatalogue searches the catalogues (searchCatalogueUnrecorded),
// logs the search with its diagnostics (catalogue-search-log.go), and
// records what it returns in a research run's ledger.
func SearchCatalogue(ctx context.Context, query, sourceType string, limit int, specificBackends []string, filters backends.SearchFilters) (*backends.ContextResult, error) {
	started := time.Now()
	result, err := searchCatalogueUnrecorded(ctx, query, sourceType, limit, specificBackends, filters)
	logCatalogueSearch(ctx, started, query, sourceType, limit, specificBackends, filters, result, err)
	if ledger := ResearchLedgerFromContext(ctx); ledger != nil && err == nil && result != nil {
		ledger.RecordPapers("search_catalogue", result.Papers)
		for _, article := range result.Articles {
			ledger.Record("search_catalogue", ResearchLedgerEntry{URL: article.URL, Title: article.Title, Text: article.Extract})
		}
	}
	return result, err
}

// VerifyCitation verifies a citation (verifyCitationUnrecorded),
// recording a matched paper in a research run's ledger.
func VerifyCitation(ctx context.Context, doi, title string, authors []string, year int) (*backends.VerificationResult, error) {
	result, err := verifyCitationUnrecorded(ctx, doi, title, authors, year)
	if ledger := ResearchLedgerFromContext(ctx); ledger != nil && err == nil && result != nil && result.Paper != nil {
		ledger.RecordPapers("check_citation", []backends.Paper{*result.Paper})
	}
	return result, err
}

// FetchCitationGraph fetches a paper's citation graph
// (fetchCitationGraphUnrecorded), recording the seed and every edge in
// a research run's ledger.
func FetchCitationGraph(ctx context.Context, identifier, direction string, limit int, sortBy string, minCitations int) (*backends.CitationGraphResult, error) {
	result, err := fetchCitationGraphUnrecorded(ctx, identifier, direction, limit, sortBy, minCitations)
	if ledger := ResearchLedgerFromContext(ctx); ledger != nil && err == nil && result != nil {
		papers := []backends.Paper{result.Seed}
		for _, edge := range append(append([]backends.CitationEdge(nil), result.References...), result.Citations...) {
			papers = append(papers, edge.Paper)
		}
		ledger.RecordPapers("citation_graph", papers)
	}
	return result, err
}
