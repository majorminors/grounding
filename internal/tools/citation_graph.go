package tools

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

// Patterns for identifier resolution.
var (
	doiURLPrefix = "https://doi.org/"
	doiPrefix    = regexp.MustCompile(`(?i)^doi:`)
	bareDOI      = regexp.MustCompile(`^10\.\d{4,}/`)
	arxivPrefix  = regexp.MustCompile(`(?i)^arxiv:`)
	bareArxiv    = regexp.MustCompile(`^\d{4}\.\d{4,}(v\d+)?$`)
	corpusPrefix = regexp.MustCompile(`(?i)^corpusid:`)
	hexID        = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// cleanDOI collapses double slashes in APA-style DOIs
// (e.g. 10.1037//0022-3514.52.3.511 → 10.1037/0022-3514.52.3.511).
func cleanDOI(doi string) string {
	return strings.ReplaceAll(doi, "//", "/")
}

// ResolvePaperID normalises user input into a Semantic Scholar identifier
// string. The second return value is true when the identifier can be
// used directly in a /paper/{id} lookup, false when a search fallback
// is needed.
func ResolvePaperID(input string) (string, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", false
	}

	// DOI URL: https://doi.org/10.xxxx/...
	if strings.HasPrefix(input, doiURLPrefix) {
		return "DOI:" + cleanDOI(strings.TrimPrefix(input, doiURLPrefix)), true
	}

	// doi: prefix
	if doiPrefix.MatchString(input) {
		return "DOI:" + cleanDOI(doiPrefix.ReplaceAllString(input, "")), true
	}

	// Bare DOI: 10.xxxx/...
	if bareDOI.MatchString(input) {
		return "DOI:" + cleanDOI(input), true
	}

	// arxiv: prefix
	if arxivPrefix.MatchString(input) {
		return "ARXIV:" + arxivPrefix.ReplaceAllString(input, ""), true
	}

	// Bare arXiv pattern: 2301.12345 or 2301.12345v2
	if bareArxiv.MatchString(input) {
		return "ARXIV:" + input, true
	}

	// corpusid: prefix
	if corpusPrefix.MatchString(input) {
		return "CorpusId:" + corpusPrefix.ReplaceAllString(input, ""), true
	}

	// 40-char hex: native Semantic Scholar ID
	if hexID.MatchString(input) {
		return input, true
	}

	// Anything else: needs search fallback
	return input, false
}

// graphBackends bundles the services a citation graph can be built
// from, in order of preference: Semantic Scholar (influence markers and
// intents), OpenAlex (server-side sort, batch metadata), OpenCitations
// (bare DOIs, no metadata; used with OpenAlex or Crossref to fill them).
type graphBackends struct {
	semanticScholar *backends.SemanticScholarBackend
	openAlex        *backends.OpenAlexBackend
	openCitations   *backends.OpenCitationsBackend
	crossref        *backends.CrossrefBackend
}

// FetchCitationGraph resolves a seed paper and fetches its citation
// graph. Tries Semantic Scholar first; falls back to OpenAlex when SS
// is rate limited, and to OpenCitations (with metadata from OpenAlex
// or Crossref) when OpenAlex fails too. Direction must be
// "references", "citations", or "both". sortBy is "recency" (default)
// or "citations". minCitations filters edges below the threshold.
func fetchCitationGraphUnrecorded(ctx context.Context, identifier, direction string, limit int, sortBy string, minCitations int) (*backends.CitationGraphResult, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 1000 {
		limit = 1000
	}

	cfg := config.LoadConfig()

	var opts []backends.ClientOption
	if cfg.Email != "" {
		opts = append(opts, backends.WithEmail(cfg.Email))
	}
	client := backends.NewClient(opts...)
	ss := backends.NewSemanticScholar(client, cfg.SemanticScholarKey)
	oa := backends.NewOpenAlex(client, cfg.OpenAlexAPIKey)
	graph := graphBackends{
		semanticScholar: ss,
		openAlex:        oa,
		openCitations:   backends.NewOpenCitations(client, cfg.OpenCitationsAccessToken),
		crossref:        backends.NewCrossref(client),
	}

	// Resolve identifier — try SS, fall back to OA on 429
	resolved, direct := ResolvePaperID(identifier)
	var (
		seed   *backends.Paper
		ssID   string // populated when SS resolves successfully
		oaID   string // populated when OA resolves (fallback or DOI)
		ssDown bool   // set when SS hits 429 during resolution
		err    error
	)

	if direct {
		seed, ssID, err = ss.ResolvePaper(ctx, resolved)
	} else {
		seed, ssID, err = ss.ResolveBySearch(ctx, resolved)
	}

	if err != nil && (backends.IsRateLimited(err) || backends.IsNotFound(err)) {
		// Fall back to OpenAlex for seed resolution. A 404 from Semantic
		// Scholar means SS doesn't index the work under that identifier,
		// not that the work doesn't exist (Bandura 1977's DOI is one);
		// OpenAlex's coverage is broader, so ask it before giving up.
		ssDown = backends.IsRateLimited(err)
		seed, oaID, err = resolveViaOpenAlex(ctx, oa, resolved, direct)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve seed paper: %w", err)
	}

	// If SS resolved but we don't have an OA ID yet, try to get one
	// from the seed DOI so OA fallback can work for individual directions.
	if oaID == "" && seed.DOI != "" {
		if p, id, oaErr := oa.ResolvePaper(ctx, seed.DOI); oaErr == nil {
			oaID = id
			// If SS didn't give us an abstract but OA did, keep it.
			// (OA rarely has abstracts, but just in case.)
			_ = p
		}
	}

	result := &backends.CitationGraphResult{
		Seed:      *seed,
		Direction: direction,
		Limit:     limit,
	}

	// Fetch references and/or citations in parallel.
	// Each direction tries SS first, falls back to OA on 429.
	var wg sync.WaitGroup
	var mu sync.Mutex
	var refErr, citErr error

	if direction == "references" || direction == "both" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			refs, err := fetchRefsWithFallback(ctx, graph, ssID, oaID, seed.DOI, ssDown, limit, sortBy)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				refErr = err
				return
			}
			result.References = refs
			result.TotalReferences = len(refs)
		}()
	}

	if direction == "citations" || direction == "both" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cits, err := fetchCitesWithFallback(ctx, graph, ssID, oaID, seed.DOI, ssDown, limit, sortBy)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				citErr = err
				return
			}
			result.Citations = cits
			result.TotalCitations = len(cits)
		}()
	}

	wg.Wait()

	// Post-processing: re-sort SS results and apply min-citations filter.
	if sortBy == "citations" {
		sortEdgesByCitations(result.References)
		sortEdgesByCitations(result.Citations)
	}
	if minCitations > 0 {
		result.References = filterEdgesByMinCitations(result.References, minCitations)
		result.TotalReferences = len(result.References)
		result.Citations = filterEdgesByMinCitations(result.Citations, minCitations)
		result.TotalCitations = len(result.Citations)
	}

	// Build temporal trend from whichever edges we have.
	result.Trend = buildCitationTrend(result)

	// Return partial results if one direction failed
	if result.References != nil || result.Citations != nil {
		return result, nil
	}

	// Both failed
	if refErr != nil {
		return nil, refErr
	}
	if citErr != nil {
		return nil, citErr
	}

	return result, nil
}

// resolveViaOpenAlex resolves a seed paper using OpenAlex.
// The direct flag indicates whether the identifier is a structured ID
// (DOI, etc.) that can be used for direct lookup, vs a title for search.
func resolveViaOpenAlex(ctx context.Context, oa *backends.OpenAlexBackend, identifier string, direct bool) (*backends.Paper, string, error) {
	if direct {
		// Strip SS-style prefixes to get bare DOI
		doi := identifier
		for _, prefix := range []string{"DOI:", "ARXIV:", "CorpusId:"} {
			doi = strings.TrimPrefix(doi, prefix)
		}
		return oa.ResolvePaper(ctx, doi)
	}
	return oa.ResolveBySearch(ctx, identifier)
}

// fetchRefsWithFallback fetches references via SS, falling back to OA
// on 429, then to OpenCitations.
func fetchRefsWithFallback(ctx context.Context, graph graphBackends, ssID, oaID, seedDOI string, ssDown bool, limit int, sortBy string) ([]backends.CitationEdge, error) {
	if !ssDown && ssID != "" {
		refs, err := graph.semanticScholar.FetchReferences(ctx, ssID, limit)
		if err == nil {
			return refs, nil
		}
		if !backends.IsRateLimited(err) {
			return nil, err
		}
		// SS rate-limited, try OA
	}
	var oaErr error
	if oaID != "" {
		// OA FetchReferences batch-fetches by ID; no API-level sort.
		// Re-sort happens in FetchCitationGraph after return.
		refs, err := graph.openAlex.FetchReferences(ctx, oaID, limit)
		if err == nil {
			return refs, nil
		}
		oaErr = err
	}
	if seedDOI == "" {
		if oaErr != nil {
			return nil, oaErr
		}
		return nil, fmt.Errorf("no OpenAlex ID or DOI available for fallback")
	}
	dois, err := graph.openCitations.FetchReferenceDOIs(ctx, seedDOI, limit)
	if err != nil {
		return nil, fmt.Errorf("opencitations fallback: %w", err)
	}
	return edgesFromDOIs(ctx, graph, dois)
}

// edgesFromDOIs turns bare DOIs into edges with metadata: OpenAlex in
// one batch when it is answering, otherwise Crossref one DOI at a time
// (capped, since that path is rate-limited to a few per second).
func edgesFromDOIs(ctx context.Context, graph graphBackends, dois []string) ([]backends.CitationEdge, error) {
	if len(dois) == 0 {
		return nil, nil
	}
	if papers, err := graph.openAlex.FetchWorksByDOIs(ctx, dois); err == nil && len(papers) > 0 {
		edges := make([]backends.CitationEdge, 0, len(papers))
		for _, p := range papers {
			edges = append(edges, backends.CitationEdge{Paper: p})
		}
		return edges, nil
	}
	const maxCrossrefLookups = 20
	if len(dois) > maxCrossrefLookups {
		dois = dois[:maxCrossrefLookups]
	}
	// All at once; Crossref's limiter paces them, and the order of the
	// DOIs is kept.
	found := make([]*backends.Paper, len(dois))
	var wg sync.WaitGroup
	for i, doi := range dois {
		wg.Add(1)
		go func(i int, doi string) {
			defer wg.Done()
			result, err := graph.crossref.VerifyDOI(ctx, doi)
			if err == nil && result != nil && result.Verified && result.Paper != nil {
				found[i] = result.Paper
			}
		}(i, doi)
	}
	wg.Wait()
	edges := make([]backends.CitationEdge, 0, len(dois))
	for _, paper := range found {
		if paper != nil {
			edges = append(edges, backends.CitationEdge{Paper: *paper})
		}
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("opencitations returned %d DOIs but no metadata source could describe them", len(dois))
	}
	return edges, nil
}

// fetchCitesWithFallback fetches citations via SS, falling back to OA
// on 429, then to OpenCitations. When sortBy is "citations", OA is
// preferred because it supports server-side cited_by_count sort. SS
// only returns recency-ordered results, so post-retrieval sort on SS
// data just re-orders a batch of recent (low-citation) papers.
func fetchCitesWithFallback(ctx context.Context, graph graphBackends, ssID, oaID, seedDOI string, ssDown bool, limit int, sortBy string) ([]backends.CitationEdge, error) {
	var oaErr error
	// For citation-sorted queries, prefer OA (server-side sort) over SS.
	if sortBy == "citations" && oaID != "" {
		cits, err := graph.openAlex.FetchCitations(ctx, oaID, limit, sortBy)
		if err == nil {
			return cits, nil
		}
		oaErr = err
	}

	if oaErr == nil && !ssDown && ssID != "" {
		cits, err := graph.semanticScholar.FetchCitations(ctx, ssID, limit)
		if err == nil {
			return cits, nil
		}
		if !backends.IsRateLimited(err) {
			return nil, err
		}
		// SS rate-limited, try OA
	}
	if oaErr == nil && oaID != "" {
		cits, err := graph.openAlex.FetchCitations(ctx, oaID, limit, sortBy)
		if err == nil {
			return cits, nil
		}
		oaErr = err
	}
	if seedDOI == "" {
		if oaErr != nil {
			return nil, oaErr
		}
		return nil, fmt.Errorf("no OpenAlex ID or DOI available for fallback")
	}
	// OpenCitations lists every citing DOI with no ordering we can use;
	// the caller's post-retrieval sort and filter still apply.
	dois, err := graph.openCitations.FetchCitationDOIs(ctx, seedDOI, limit)
	if err != nil {
		return nil, fmt.Errorf("opencitations fallback: %w", err)
	}
	return edgesFromDOIs(ctx, graph, dois)
}

// sortEdgesByCitations sorts citation edges by citation count descending.
func sortEdgesByCitations(edges []backends.CitationEdge) {
	slices.SortFunc(edges, func(a, b backends.CitationEdge) int {
		return cmp.Compare(b.Paper.Citations, a.Paper.Citations)
	})
}

// filterEdgesByMinCitations removes edges below the citation threshold.
func filterEdgesByMinCitations(edges []backends.CitationEdge, minCitations int) []backends.CitationEdge {
	n := 0
	for _, e := range edges {
		if e.Paper.Citations >= minCitations {
			edges[n] = e
			n++
		}
	}
	return edges[:n]
}

// buildCitationTrend aggregates publication years across all edges in
// the graph result. Returns nil when there are no edges at all.
func buildCitationTrend(r *backends.CitationGraphResult) *backends.CitationTrend {
	edges := make([]backends.CitationEdge, 0, len(r.References)+len(r.Citations))
	edges = append(edges, r.References...)
	edges = append(edges, r.Citations...)
	if len(edges) == 0 {
		return nil
	}

	counts := make(map[int]int)
	minYear, maxYear := 1<<31-1, 0
	missing := 0

	for _, e := range edges {
		y := e.Paper.Year
		if y == 0 {
			missing++
			continue
		}
		counts[y]++
		if y < minYear {
			minYear = y
		}
		if y > maxYear {
			maxYear = y
		}
	}

	if len(counts) == 0 {
		// Every edge was missing a year.
		return &backends.CitationTrend{MissingYear: missing}
	}

	// The trend is a sample when either direction was capped.
	sampled := len(r.References) >= r.Limit || len(r.Citations) >= r.Limit

	return &backends.CitationTrend{
		Counts:      counts,
		YearRange:   [2]int{minYear, maxYear},
		MissingYear: missing,
		Sampled:     sampled,
	}
}
