package tools

import (
	"context"
	"strings"
	"testing"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

// registerFakeAddOnForThisTest registers an add-on's paper search
// source, enricher, verifier and probe, and removes them when the test
// ends. calls counts each one's use.
type fakeAddOnCalls struct {
	searched, enriched, verifierBuilt, probed int
}

func registerFakeAddOnForThisTest(t *testing.T, source PaperSearchSource) *fakeAddOnCalls {
	t.Helper()
	calls := &fakeAddOnCalls{}
	savedSources := paperSearchSourcesAddedByAddOns
	savedEnrichers := paperSearchResultEnrichersAddedByAddOns
	savedVerifiers := citationVerifiersAddedByAddOns
	savedProbes := healthProbesAddedByAddOns
	t.Cleanup(func() {
		paperSearchSourcesAddedByAddOns = savedSources
		paperSearchResultEnrichersAddedByAddOns = savedEnrichers
		citationVerifiersAddedByAddOns = savedVerifiers
		healthProbesAddedByAddOns = savedProbes
	})

	search := source.Search
	source.Search = func(ctx context.Context, client *backends.Client, cfg config.Config, query string, limit int, filters backends.SearchFilters) ([]backends.Paper, error) {
		calls.searched++
		if search != nil {
			return search(ctx, client, cfg, query, limit, filters)
		}
		return []backends.Paper{{Title: "Fake add-on monograph on " + query, Abstract: "A monograph on " + query + ".", Source: source.Name}}, nil
	}
	RegisterPaperSearchSource(source)
	RegisterPaperSearchResultEnricher(func(ctx context.Context, client *backends.Client, cfg config.Config, namedBackends map[string]bool, papers []backends.Paper) {
		if !namedBackends[source.Name] {
			return
		}
		calls.enriched++
		for i := range papers {
			if papers[i].Source == source.Name {
				papers[i].Abstract = "filled by the add-on"
			}
		}
	})
	RegisterCitationVerifier(CitationVerifierAddedByAddOn{
		Name: source.Name,
		New: func(ctx context.Context, client *backends.Client, cfg config.Config) (backends.CitationBackend, bool) {
			calls.verifierBuilt++
			return nil, false
		},
	})
	RegisterHealthProbe(HealthProbeAddedByAddOn{
		Name:         source.Name,
		FullTierOnly: true,
		Check: func(client *backends.Client, cfg config.Config) func(context.Context) backends.HealthStatus {
			return func(context.Context) backends.HealthStatus {
				calls.probed++
				return backends.HealthStatus{Healthy: true, Message: "fake"}
			}
		},
	})
	return calls
}

func diagnosticFor(t *testing.T, result *backends.ContextResult, backend string) backends.Diagnostic {
	t.Helper()
	for _, d := range result.Diagnostics {
		if d.Backend == backend {
			return d
		}
	}
	t.Fatalf("no diagnostic for %s; got %+v", backend, result.Diagnostics)
	return backends.Diagnostic{}
}

func TestAnAddOnsPaperSearchSourceIsSearchedWhenNamedAndItsEnricherRuns(t *testing.T) {
	calls := registerFakeAddOnForThisTest(t, PaperSearchSource{Name: "fake_add_on", RequiresFullTier: true, CostNote: "free in tests"})

	ctx := auth.WithTier(context.Background(), auth.TierFull)
	result, err := searchCatalogueUnrecorded(ctx, "tribe", "academic", 3, []string{"fake_add_on"}, backends.SearchFilters{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls.searched != 1 || calls.enriched != 1 {
		t.Fatalf("searched %d times and enriched %d, want 1 and 1", calls.searched, calls.enriched)
	}
	if len(result.Papers) != 1 || result.Papers[0].Abstract != "filled by the add-on" {
		t.Fatalf("papers = %+v, want the add-on's one paper with its abstract filled", result.Papers)
	}
	if d := diagnosticFor(t, result, "fake_add_on"); d.RawCount != 1 || d.FilteredCount != 1 || d.CostNote != "free in tests" {
		t.Errorf("diagnostic = %+v", d)
	}
}

func TestAnAddOnsSourceIsNeverInTheDefaultSearchUnlessItSaysSo(t *testing.T) {
	calls := registerFakeAddOnForThisTest(t, PaperSearchSource{Name: "fake_add_on"})
	source, ok := paperSearchSourceNamed("fake_add_on")
	if !ok || source.InDefaultSearch {
		t.Fatalf("fake_add_on registered = %v, in default search = %v", ok, source.InDefaultSearch)
	}
	for _, s := range paperSearchSources() {
		if s.InDefaultSearch && (s.RequiresFullTier || s.ReasonItIsNotConfigured != nil) {
			t.Errorf("%s is in the default search but needs a key or the full tier", s.Name)
		}
	}
	_ = calls
}

func TestANamedFullTierSourceIsSkippedForPublicCallers(t *testing.T) {
	calls := registerFakeAddOnForThisTest(t, PaperSearchSource{Name: "fake_add_on", RequiresFullTier: true})
	t.Setenv("CONSENSUS_APP_KEY", "dummy")

	ctx := auth.WithTier(context.Background(), auth.TierPublic)
	result, err := searchCatalogueUnrecorded(ctx, "tribe", "academic", 3, []string{"fake_add_on", "consensus"}, backends.SearchFilters{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls.searched != 0 {
		t.Fatalf("a public caller's search reached the full-tier source")
	}
	for _, backend := range []string{"fake_add_on", "consensus"} {
		if d := diagnosticFor(t, result, backend); !d.Skipped || d.SkippedReason != "requires authentication" {
			t.Errorf("%s diagnostic = %+v, want skipped: requires authentication", backend, d)
		}
	}
}

func TestANamedSourceThatIsNotConfiguredIsSkippedWithItsReasonBeforeTheTierIsAsked(t *testing.T) {
	calls := registerFakeAddOnForThisTest(t, PaperSearchSource{
		Name:                    "fake_add_on",
		RequiresFullTier:        true,
		ReasonItIsNotConfigured: func(config.Config) string { return "FAKE_KEY not configured" },
	})
	ctx := auth.WithTier(context.Background(), auth.TierPublic)
	result, err := searchCatalogueUnrecorded(ctx, "tribe", "academic", 3, []string{"fake_add_on"}, backends.SearchFilters{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d := diagnosticFor(t, result, "fake_add_on"); !d.Skipped || d.SkippedReason != "FAKE_KEY not configured" || calls.searched != 0 {
		t.Errorf("diagnostic = %+v, searched %d times", d, calls.searched)
	}
}

func TestAnUnknownBackendNamesEveryRegisteredSourceInItsError(t *testing.T) {
	registerFakeAddOnForThisTest(t, PaperSearchSource{Name: "fake_add_on"})
	_, err := searchCatalogueUnrecorded(context.Background(), "tribe", "academic", 3, []string{"no_such_backend"}, backends.SearchFilters{})
	if err == nil {
		t.Fatal("no error for an unknown backend")
	}
	for _, name := range []string{"semantic_scholar", "openalex", "crossref", "europepmc", "consensus", "fake_add_on", "wikipedia"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
}

// Live: check_backends probes core's backends over the network too.
func TestAnAddOnsHealthProbeRunsOnlyOnTheFullTier(t *testing.T) {
	if testing.Short() {
		t.Skip("probes the live backends")
	}
	calls := registerFakeAddOnForThisTest(t, PaperSearchSource{Name: "fake_add_on"})

	public := CheckBackends(auth.WithTier(context.Background(), auth.TierPublic), nil)
	if _, ok := public.Backends["fake_add_on"]; ok || calls.probed != 0 {
		t.Errorf("a public check_backends showed or ran the full-tier probe")
	}
	full := CheckBackends(auth.WithTier(context.Background(), auth.TierFull), nil)
	if status, ok := full.Backends["fake_add_on"]; !ok || status.Message != "fake" || calls.probed != 1 {
		t.Errorf("full-tier check_backends: status %+v, present %v, probed %d", status, ok, calls.probed)
	}
}
