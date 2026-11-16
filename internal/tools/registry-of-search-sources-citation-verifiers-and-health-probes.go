package tools

import (
	"context"
	"strings"

	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

// This file is the seam an add-on module plugs its backends into
// (PLAN "Core and add-on split", phase 1). Core's own sources are
// listed here in a fixed order; an add-on registers more from its
// init(), and they come after core's. Backends are still built per
// call: what is registered is how to build and when to use each one,
// not a live instance.

// PaperSearchSource is one backend search_catalogue can search for
// papers and books, by name.
type PaperSearchSource struct {
	// Name is what a caller passes in backends, e.g. "openalex".
	Name string

	// InDefaultSearch marks a source queried when the caller names
	// none. A paid or slow source never is (AGENTS "Working Here").
	InDefaultSearch bool

	// RequiresFullTier skips the source for public callers, with the
	// diagnostic "requires authentication".
	RequiresFullTier bool

	// ReasonItIsNotConfigured returns "" when the source can run on
	// this configuration, or the skip reason a caller who named it is
	// shown (e.g. "CONSENSUS_APP_KEY not configured"). Nil means it
	// needs nothing.
	ReasonItIsNotConfigured func(cfg config.Config) string

	// CooldownBackendName is the name the shared HTTP client benches
	// this backend under after a 429, when that differs from Name; a
	// benched source is skipped with the time remaining.
	CooldownBackendName string

	// CostNote is attached to every diagnostic of the source, for one
	// whose API reports no per-call cost.
	CostNote string

	// GetReadyOnTheCommandLine, when set, runs before a command-line
	// search that names the source: the moment to fetch a key by
	// running a command (config.SecretFromEnvironmentOrItsCommand),
	// which the server never does. An error stops the search.
	GetReadyOnTheCommandLine func() error

	// Search runs the query.
	Search func(ctx context.Context, client *backends.Client, cfg config.Config, query string, limit int, filters backends.SearchFilters) ([]backends.Paper, error)
}

// PaperSearchResultEnricher runs on the merged, filtered and trimmed
// papers of a search, and may fill fields in place (an abstract, say).
// namedBackends is what the caller asked for by name; nil when the
// caller named none.
type PaperSearchResultEnricher func(ctx context.Context, client *backends.Client, cfg config.Config, namedBackends map[string]bool, papers []backends.Paper)

// CitationVerifierAddedByAddOn builds one more backend for
// check_citation, after core's. New returns false when the verifier
// cannot run for this caller (no key, or the wrong tier).
type CitationVerifierAddedByAddOn struct {
	Name string
	New  func(ctx context.Context, client *backends.Client, cfg config.Config) (backends.CitationBackend, bool)
}

// HealthProbeAddedByAddOn is one more line in check_backends.
type HealthProbeAddedByAddOn struct {
	Name         string
	FullTierOnly bool
	Check        func(client *backends.Client, cfg config.Config) func(context.Context) backends.HealthStatus
}

// AvailabilityProvider says, for records of a finished search, whether
// and how each can be had. Find returns one entry per record, nil where
// it knows nothing; it must stop when ctx ends (the search gives every
// provider one shared time budget, and keeps what is ready).
type AvailabilityProvider struct {
	Name         string
	FullTierOnly bool
	Find         func(ctx context.Context, client *backends.Client, cfg config.Config, records []backends.Paper) []*backends.Availability
}

var (
	availabilityProvidersAddedByAddOns      []AvailabilityProvider
	paperSearchSourcesAddedByAddOns         []PaperSearchSource
	paperSearchResultEnrichersAddedByAddOns []PaperSearchResultEnricher
	citationVerifiersAddedByAddOns          []CitationVerifierAddedByAddOn
	healthProbesAddedByAddOns               []HealthProbeAddedByAddOn
)

// RegisterPaperSearchSource adds a source after core's. Call it from
// an init().
func RegisterPaperSearchSource(source PaperSearchSource) {
	paperSearchSourcesAddedByAddOns = append(paperSearchSourcesAddedByAddOns, source)
}

// RegisterAvailabilityProvider adds a provider after core's. Call it
// from an init().
func RegisterAvailabilityProvider(provider AvailabilityProvider) {
	availabilityProvidersAddedByAddOns = append(availabilityProvidersAddedByAddOns, provider)
}

// RegisterPaperSearchResultEnricher adds an enricher. Call it from an
// init().
func RegisterPaperSearchResultEnricher(enricher PaperSearchResultEnricher) {
	paperSearchResultEnrichersAddedByAddOns = append(paperSearchResultEnrichersAddedByAddOns, enricher)
}

// RegisterCitationVerifier adds a check_citation backend after core's.
// Call it from an init().
func RegisterCitationVerifier(verifier CitationVerifierAddedByAddOn) {
	citationVerifiersAddedByAddOns = append(citationVerifiersAddedByAddOns, verifier)
}

// RegisterHealthProbe adds a check_backends line. Call it from an
// init().
func RegisterHealthProbe(probe HealthProbeAddedByAddOn) {
	healthProbesAddedByAddOns = append(healthProbesAddedByAddOns, probe)
}

// paperSearchSources is every source search_catalogue can search, core's
// first in their fixed order (Semantic Scholar first, so its abstracts
// win the dedup), then the add-ons'.
func paperSearchSources() []PaperSearchSource {
	return append(corePaperSearchSources(), paperSearchSourcesAddedByAddOns...)
}

func corePaperSearchSources() []PaperSearchSource {
	return []PaperSearchSource{
		{
			Name:                "semantic_scholar",
			InDefaultSearch:     true,
			CooldownBackendName: "semantic-scholar",
			Search: func(ctx context.Context, client *backends.Client, cfg config.Config, query string, limit int, filters backends.SearchFilters) ([]backends.Paper, error) {
				return backends.NewSemanticScholar(client, cfg.SemanticScholarKey).Search(ctx, query, limit, filters)
			},
		},
		{
			Name:            "crossref",
			InDefaultSearch: true,
			Search: func(ctx context.Context, client *backends.Client, cfg config.Config, query string, limit int, filters backends.SearchFilters) ([]backends.Paper, error) {
				return backends.NewCrossref(client).Search(ctx, query, limit, filters)
			},
		},
		{
			Name:            "openalex",
			InDefaultSearch: true,
			Search: func(ctx context.Context, client *backends.Client, cfg config.Config, query string, limit int, filters backends.SearchFilters) ([]backends.Paper, error) {
				return backends.NewOpenAlex(client, cfg.OpenAlexAPIKey).Search(ctx, query, limit, filters)
			},
		},
		{
			Name:            "europepmc",
			InDefaultSearch: true,
			Search: func(ctx context.Context, client *backends.Client, cfg config.Config, query string, limit int, filters backends.SearchFilters) ([]backends.Paper, error) {
				return backends.NewEuropePMC(client).Search(ctx, query, limit, filters)
			},
		},
		{
			// Books, by keyword. Free, like the four above.
			Name:            "openlibrary",
			InDefaultSearch: true,
			Search: func(ctx context.Context, client *backends.Client, cfg config.Config, query string, limit int, filters backends.SearchFilters) ([]backends.Paper, error) {
				return backends.NewOpenLibrary(client).Search(ctx, query, limit, filters)
			},
		},
		{
			Name:             "consensus",
			RequiresFullTier: true,
			ReasonItIsNotConfigured: func(cfg config.Config) string {
				if cfg.ConsensusAppKey == "" {
					return "CONSENSUS_APP_KEY not configured"
				}
				return ""
			},
			CostNote: consensusCostNote,
			Search: func(ctx context.Context, client *backends.Client, cfg config.Config, query string, limit int, filters backends.SearchFilters) ([]backends.Paper, error) {
				return backends.NewConsensus(client, cfg.ConsensusAppKey).Search(ctx, query, limit, filters)
			},
		},
	}
}

// GetNamedSourcesReadyOnTheCommandLine runs GetReadyOnTheCommandLine
// for each source the command line named.
func GetNamedSourcesReadyOnTheCommandLine(named []string) error {
	for _, name := range named {
		if source, ok := paperSearchSourceNamed(name); ok && source.GetReadyOnTheCommandLine != nil {
			if err := source.GetReadyOnTheCommandLine(); err != nil {
				return err
			}
		}
	}
	return nil
}

// paperSearchSourceNamed returns the registered source with that name.
func paperSearchSourceNamed(name string) (PaperSearchSource, bool) {
	for _, source := range paperSearchSources() {
		if source.Name == name {
			return source, true
		}
	}
	return PaperSearchSource{}, false
}

// backendNamesSearchCatalogueAccepts lists every name a caller may pass
// in backends, for the unknown-backend error.
func backendNamesSearchCatalogueAccepts() string {
	var names []string
	for _, source := range paperSearchSources() {
		names = append(names, source.Name)
	}
	names = append(names, "wikipedia")
	return strings.Join(names, ", ")
}
