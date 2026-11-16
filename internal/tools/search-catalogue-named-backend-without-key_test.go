package tools

import (
	"context"
	"strings"
	"testing"

	"btr.mt/grounding/internal/backends"
)

// A backend the caller names but the server has no key for is reported
// as skipped, never dropped in silence (2026-11-01).
func TestSearchCatalogueReportsANamedBackendItHasNoKeyFor(t *testing.T) {
	t.Setenv("CONSENSUS_APP_KEY", "")

	result, err := searchCatalogueUnrecorded(context.Background(), "tribe", "academic", 3, []string{"consensus"}, backends.SearchFilters{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{
		"consensus": "CONSENSUS_APP_KEY not configured",
	}
	for _, diagnostic := range result.Diagnostics {
		if reason, ok := want[diagnostic.Backend]; ok && diagnostic.Skipped && diagnostic.SkippedReason == reason {
			delete(want, diagnostic.Backend)
		}
	}
	for backend, reason := range want {
		t.Errorf("no skipped diagnostic for %s with reason %q; got %+v", backend, reason, result.Diagnostics)
	}
}

// Web pages are web_search's (dorian, 2026-11-15): asking the catalogue
// search for them is an error that says where to go instead.
func TestSearchCatalogueSendsAWebSearchToWebSearch(t *testing.T) {
	for _, call := range []struct {
		sourceType string
		backends   []string
	}{
		{sourceType: "web"},
		{sourceType: "auto", backends: []string{"exa"}},
	} {
		_, err := searchCatalogueUnrecorded(context.Background(), "tribe", call.sourceType, 3, call.backends, backends.SearchFilters{})
		if err == nil || !strings.Contains(err.Error(), "web_search") {
			t.Errorf("source_type %q, backends %v: error %v, want one pointing at web_search", call.sourceType, call.backends, err)
		}
	}
}
