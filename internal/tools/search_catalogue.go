package tools

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

// punctRe matches all non-alphanumeric, non-space characters.
var punctRe = regexp.MustCompile(`[^\p{L}\p{N}\s]`)

// normTitle normalises a title for dedup: strips punctuation,
// lowercases, and collapses whitespace. This catches encoding
// variants (smart quotes vs ASCII), punctuation differences,
// and identical-title series with different DOIs.
func normTitle(title string) string {
	t := punctRe.ReplaceAllString(title, "")
	t = strings.ToLower(t)
	// Collapse runs of whitespace.
	var b strings.Builder
	inSpace := false
	for _, r := range t {
		if unicode.IsSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
		} else {
			b.WriteRune(r)
			inSpace = false
		}
	}
	return strings.TrimSpace(b.String())
}

// SearchCatalogue aggregates search results from multiple backends based on source type.
// It's the shared business logic called by both the MCP handler and CLI.
//
// When specificBackends is non-empty, it overrides sourceType and queries only the
// named backends: any registered paper search source (core's and an add-on's,
// registry-of-search-sources-citation-verifiers-and-health-probes.go) or
// "wikipedia".
//
// A source that is not InDefaultSearch is opt-in by name: paid or slow ones
// (Consensus, an add-on's), so the caller has to ask for them by name. Web pages
// are web_search's, not the catalogue's (dorian, 2026-11-15).
func searchCatalogueUnrecorded(ctx context.Context, query, sourceType string, limit int, specificBackends []string, filters backends.SearchFilters) (*backends.ContextResult, error) {
	cfg := config.LoadConfig()

	// Build HTTP client
	var opts []backends.ClientOption
	if cfg.Email != "" {
		opts = append(opts, backends.WithEmail(cfg.Email))
	}
	client := backends.NewClient(opts...)

	result := &backends.ContextResult{
		Query: query,
	}

	// Determine which backends to query.
	// If specificBackends is set, it overrides sourceType entirely.
	searchAcademic := false
	searchWikipedia := false
	var academicFilter map[string]bool

	if len(specificBackends) > 0 {
		academicFilter = make(map[string]bool)
		for _, b := range specificBackends {
			switch b {
			case "exa":
				return nil, fmt.Errorf("exa searches web pages, which search_catalogue no longer does: use web_search with mode \"semantic\"")
			case "wikipedia":
				searchWikipedia = true
			default:
				if _, ok := paperSearchSourceNamed(b); !ok {
					return nil, fmt.Errorf("unknown backend: %q (valid: %s)", b, backendNamesSearchCatalogueAccepts())
				}
				searchAcademic = true
				academicFilter[b] = true
			}
		}
	} else {
		switch sourceType {
		case "academic":
			searchAcademic = true
		case "general":
			searchWikipedia = true
		case "web":
			return nil, fmt.Errorf("source_type \"web\" is gone: search_catalogue searches catalogues of papers, books and encyclopaedia articles; for web pages use web_search")
		case "auto":
			searchAcademic = true
			searchWikipedia = true
		default:
			return nil, fmt.Errorf("invalid source_type: %s (must be academic, general, or auto)", sourceType)
		}
	}

	// Use WaitGroup for parallel backend queries
	var wg sync.WaitGroup
	var mu sync.Mutex
	var allDiags []backends.Diagnostic

	// Query academic backends (Semantic Scholar + Crossref + OpenAlex)
	if searchAcademic {
		wg.Add(1)
		go func() {
			defer wg.Done()
			papers, diags, _ := searchAcademicBackends(ctx, client, cfg, query, limit, academicFilter, filters)
			mu.Lock()
			defer mu.Unlock()
			allDiags = append(allDiags, diags...)
			result.Papers, result.Books = splitBooksFromPapers(papers)
		}()
	}

	// Query Wikipedia
	if searchWikipedia {
		wg.Add(1)
		go func() {
			defer wg.Done()
			articles, diag, _ := searchWikipediaBackend(ctx, client, query, limit)
			mu.Lock()
			defer mu.Unlock()
			allDiags = append(allDiags, diag)
			result.Articles = articles
		}()
	}

	wg.Wait()

	result.Diagnostics = allDiags
	result.Warnings = warningsFromDiagnostics(allDiags)

	// Build summary metadata for papers
	if len(result.Papers) > 0 {
		result.Summary = buildSummary(result.Papers)
	}

	if len(result.Papers) == 0 && len(result.Books) == 0 && len(result.Articles) == 0 {
		result.EmptyResultExplanation = explanationWhenNothingIsReturned(query, allDiags)
	}

	return result, nil
}

// warningMaxErrorRunes keeps a warning to one line: the full error is
// in the diagnostics and the log.
const warningMaxErrorRunes = 120

// requestInAnErrorPattern matches Go's `Get "https://…": ` (or Post),
// the long part of a transport error that says least about why.
var requestInAnErrorPattern = regexp.MustCompile(`(Get|Post|Head) "[^"]*": `)

// errorWithoutTheRequestURL leaves the request's URL out of an error
// for a one-line warning; the diagnostics and the log keep it whole.
func errorWithoutTheRequestURL(message string) string {
	return requestInAnErrorPattern.ReplaceAllString(message, "")
}

// warningsFromDiagnostics gives one line for each source that failed,
// rested after a rate limit, or was skipped, in the diagnostics' order.
// A source that ran and found nothing is not a warning.
func warningsFromDiagnostics(diagnostics []backends.Diagnostic) []string {
	var warnings []string
	for _, d := range diagnostics {
		switch {
		case d.Error != "":
			warnings = append(warnings, fmt.Sprintf("%s failed (%s); results may be incomplete", d.Backend, firstCharacters(errorWithoutTheRequestURL(d.Error), warningMaxErrorRunes)))
		case d.Skipped && strings.HasPrefix(d.SkippedReason, "cooldown"):
			warnings = append(warnings, fmt.Sprintf("%s is resting after a rate limit (%s); results may be incomplete", d.Backend, d.SkippedReason))
		case d.Skipped:
			warnings = append(warnings, fmt.Sprintf("%s was not searched: %s", d.Backend, d.SkippedReason))
		}
	}
	return warnings
}

// explanationWhenNothingIsReturned tells a filter decision, a failure or
// a skip apart from the backends simply having nothing, so that none of
// them reads as evidence the work does not exist. It returns "" for that
// clean negative.
func explanationWhenNothingIsReturned(query string, diagnostics []backends.Diagnostic) string {
	rawResults := 0
	var failed, skipped []string
	for _, d := range diagnostics {
		rawResults += d.RawCount
		if d.Error != "" {
			failed = append(failed, d.Backend)
		}
		if d.Skipped {
			skipped = append(skipped, d.Backend)
		}
	}

	switch {
	case rawResults > 0:
		retrieved := fmt.Sprintf("%d results were", rawResults)
		if rawResults == 1 {
			retrieved = "1 result was"
		}
		if doi := doiWhenTheWholeQueryIsOne(query); doi != "" {
			return fmt.Sprintf("%s retrieved but none carries the DOI %s, so all were filtered out. Search backends match a DOI as text, so this does not show the work is absent; to look a DOI up, use check_citation (grounding check-citation --doi).", retrieved, doi)
		}
		return fmt.Sprintf("%s retrieved but all were filtered out (relevance to the query terms, and min_citations if set). This does not show the literature is absent; try other terms.", retrieved)
	case len(failed) > 0:
		return fmt.Sprintf("Nothing came back, but %s failed (see warnings), so this is not a clean negative.", strings.Join(failed, ", "))
	case len(skipped) > 0 && len(skipped) == len(diagnostics):
		return fmt.Sprintf("Nothing was searched: %s skipped (see warnings).", strings.Join(skipped, ", "))
	}
	return ""
}

// consensusCostNote is attached to every Consensus diagnostic; the API
// doesn't report per-call cost, and ~1¢ a query is the published rate.
const consensusCostNote = "~$0.01 per query"

// minRelevanceScore is the threshold for filtering academic papers.
// Kept low because the LLM consumer can judge relevance; false
// negatives (missing a canonical paper) are worse than false positives.
const minRelevanceScore = 0.1

// searchAcademicBackends queries Semantic Scholar, Crossref, and OpenAlex in parallel,
// deduplicates results by DOI, filters by relevance, and returns combined papers
// along with per-backend diagnostics.
//
// When backendFilter is non-nil, only backends whose name appears in the map are queried.
// A nil backendFilter queries all three backends.
func searchAcademicBackends(ctx context.Context, client *backends.Client, cfg config.Config, query string, limit int, backendFilter map[string]bool, filters backends.SearchFilters) ([]backends.Paper, []backends.Diagnostic, error) {
	type backendResult struct {
		name     string
		costNote string
		papers   []backends.Paper
		err      error
	}

	// Build the list of backends to query.
	type backendEntry struct {
		name     string
		costNote string
		search   func() ([]backends.Paper, error)
	}
	var toQuery []backendEntry
	var skipDiags []backends.Diagnostic

	queryAll := backendFilter == nil

	for _, source := range paperSearchSources() {
		if queryAll && !source.InDefaultSearch {
			continue
		}
		if !queryAll && !backendFilter[source.Name] {
			continue
		}
		// A backend the caller named but this server has no key for is
		// reported, not dropped in silence.
		if source.ReasonItIsNotConfigured != nil {
			if reason := source.ReasonItIsNotConfigured(cfg); reason != "" {
				skipDiags = append(skipDiags, backends.Diagnostic{Backend: source.Name, Skipped: true, SkippedReason: reason})
				continue
			}
		}
		if source.RequiresFullTier && auth.GetTier(ctx) == auth.TierPublic {
			skipDiags = append(skipDiags, backends.Diagnostic{Backend: source.Name, Skipped: true, SkippedReason: "requires authentication"})
			continue
		}
		if source.CooldownBackendName != "" {
			if expiry, active := backends.InCooldown(source.CooldownBackendName); active {
				remaining := time.Until(expiry).Truncate(time.Second)
				skipDiags = append(skipDiags, backends.Diagnostic{
					Backend:       source.Name,
					Skipped:       true,
					SkippedReason: fmt.Sprintf("cooldown (%s remaining)", remaining),
				})
				continue
			}
		}
		search := source.Search
		toQuery = append(toQuery, backendEntry{
			name:     source.Name,
			costNote: source.CostNote,
			search:   func() ([]backends.Paper, error) { return search(ctx, client, cfg, query, limit, filters) },
		})
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make([]backendResult, len(toQuery))

	for i, entry := range toQuery {
		wg.Add(1)
		go func(idx int, e backendEntry) {
			defer wg.Done()
			papers, err := e.search()
			mu.Lock()
			results[idx] = backendResult{e.name, e.costNote, papers, err}
			mu.Unlock()
		}(i, entry)
	}

	wg.Wait()

	// Build per-backend diagnostics
	diags := make([]backends.Diagnostic, 0, len(results)+len(skipDiags))
	diags = append(diags, skipDiags...)
	for _, r := range results {
		d := backends.Diagnostic{
			Backend:  r.name,
			RawCount: len(r.papers),
		}
		if r.err != nil {
			d.Error = r.err.Error()
		}
		d.CostNote = r.costNote
		diags = append(diags, d)
	}

	// Merge and deduplicate results from all backends.
	// Collect everything first, then let the relevance filter rank and
	// trim to limit. This ensures citation-sorted results from
	// Crossref/OpenAlex aren't excluded just because SS filled the quota.
	seen := make(map[string]bool)
	merged := make([]backends.Paper, 0, limit*6)

	addPapers := func(papers []backends.Paper) {
		for _, p := range papers {
			key := dedupeKey(p)
			if !seen[key] {
				seen[key] = true
				merged = append(merged, p)
			}
		}
	}

	// Add all backends (SS first for dedup preference on abstracts)
	for _, r := range results {
		addPapers(r.papers)
	}

	// Deduplicate book chapters: different DOIs from the same book
	// (e.g. "Masculinity Besieged?" appearing 4 times as separate chapters).
	merged = dedupeBookChapters(merged)

	// Title-normalisation dedup: catch encoding variants, punctuation
	// differences, and identical-title series (e.g. OECD data) that
	// have different DOIs. Groups by normTitle, keeps richest metadata.
	merged = dedupeTitleVariants(merged)

	// If all backends failed, return the first error with diagnostics
	if len(merged) == 0 {
		for _, r := range results {
			if r.err != nil {
				return nil, diags, r.err
			}
		}
	}

	// Post-retrieval citation filter for backends that don't support it
	// natively (SS, Crossref). OpenAlex already filtered server-side,
	// but re-checking here is harmless and keeps the logic uniform.
	// OpenLibrary counts no citations, so its books pass untouched.
	if filters.MinCitations > 0 {
		kept := merged[:0]
		for _, p := range merged {
			if p.Source == "openlibrary" || p.Citations >= filters.MinCitations {
				kept = append(kept, p)
			}
		}
		merged = kept
	}

	// Filter and re-rank by relevance to query terms
	filtered := FilterByRelevance(query, merged, minRelevanceScore)

	// Record filtered count per backend by checking which papers survived
	survivedDOIs := make(map[string]bool, len(filtered))
	survivedTitles := make(map[string]bool, len(filtered))
	for _, p := range filtered {
		if p.DOI != "" {
			survivedDOIs[p.DOI] = true
		}
		survivedTitles[p.Title] = true
	}
	diagOffset := len(skipDiags)
	for i, r := range results {
		count := 0
		for _, p := range r.papers {
			if p.DOI != "" && survivedDOIs[p.DOI] {
				count++
			} else if survivedTitles[p.Title] {
				count++
			}
		}
		diags[diagOffset+i].FilteredCount = count
	}

	// Books are ranked and limited apart from papers: with no abstract
	// or citation count they would otherwise always rank below the
	// papers and be trimmed away.
	filtered = keepTheFirstOfEachKind(filtered, limit)
	// Availability and the enrichers at once. Availability works on its
	// own copy of the records, since the enrichers write into them while
	// it reads; what it found is attached once both are done.
	recordsForAvailability := append([]backends.Paper(nil), filtered...)
	var availabilityAndEnrichment sync.WaitGroup
	availabilityAndEnrichment.Add(1)
	go func() {
		defer availabilityAndEnrichment.Done()
		fillAvailability(ctx, client, cfg, recordsForAvailability)
	}()
	for _, enrich := range paperSearchResultEnrichersAddedByAddOns {
		availabilityAndEnrichment.Add(1)
		go func(enrich PaperSearchResultEnricher) {
			defer availabilityAndEnrichment.Done()
			enrich(ctx, client, cfg, backendFilter, filtered)
		}(enrich)
	}
	availabilityAndEnrichment.Wait()
	for i := range filtered {
		filtered[i].Availability = recordsForAvailability[i].Availability
	}

	return filtered, diags, nil
}

// searchWikipediaBackend queries Wikipedia for articles and returns diagnostics.
func searchWikipediaBackend(ctx context.Context, client *backends.Client, query string, limit int) ([]backends.WikipediaArticle, backends.Diagnostic, error) {
	wp := backends.NewWikipedia(client)
	articles, err := wp.Search(ctx, query, limit)

	diag := backends.Diagnostic{
		Backend:       "wikipedia",
		RawCount:      wp.LastRawCount,
		FilteredCount: len(articles),
	}
	if err != nil {
		diag.Error = err.Error()
	}

	return articles, diag, err
}

// dedupeKey returns a key for deduplicating papers.
// Uses DOI (lowercased, since DOIs are case-insensitive) if available,
// otherwise normalised title (stripped of punctuation and lowercased).
func dedupeKey(p backends.Paper) string {
	if p.DOI != "" {
		return "doi:" + strings.ToLower(p.DOI)
	}
	return "title:" + normTitle(p.Title)
}

// minBookPrefixLen is the minimum shared title prefix length (in chars)
// for two papers to be considered chapters from the same book.
const minBookPrefixLen = 20

// dedupeBookChapters removes duplicate book chapters that share the
// same Journal field and a long common title prefix. Keeps the paper
// with the richest metadata (abstract, citations, year).
func dedupeBookChapters(papers []backends.Paper) []backends.Paper {
	if len(papers) < 2 {
		return papers
	}

	type entry struct {
		idx   int
		title string
	}
	groups := map[string][]entry{}
	for i, p := range papers {
		j := strings.TrimSpace(p.Journal)
		if j == "" {
			continue
		}
		key := strings.ToLower(j)
		groups[key] = append(groups[key], entry{i, strings.ToLower(p.Title)})
	}

	remove := map[int]bool{}
	for _, entries := range groups {
		if len(entries) < 2 {
			continue
		}
		for i := 0; i < len(entries); i++ {
			if remove[entries[i].idx] {
				continue
			}
			for j := i + 1; j < len(entries); j++ {
				if remove[entries[j].idx] {
					continue
				}
				if commonPrefixLen(entries[i].title, entries[j].title) >= minBookPrefixLen {
					// Keep the paper with richer metadata
					if paperMetadataScore(papers[entries[i].idx]) >= paperMetadataScore(papers[entries[j].idx]) {
						remove[entries[j].idx] = true
					} else {
						remove[entries[i].idx] = true
					}
				}
			}
		}
	}

	if len(remove) == 0 {
		return papers
	}

	result := make([]backends.Paper, 0, len(papers)-len(remove))
	for i, p := range papers {
		if !remove[i] {
			result = append(result, p)
		}
	}
	return result
}

// dedupeTitleVariants groups papers by normalised title and keeps the
// one with the richest metadata from each group. This catches encoding
// variants ("rites d'initiation" vs "rites d\u2019initiation"),
// punctuation differences, and identical-title data series (OECD) that
// have different DOIs and thus survive dedupeKey.
func dedupeTitleVariants(papers []backends.Paper) []backends.Paper {
	if len(papers) < 2 {
		return papers
	}

	type entry struct {
		idx   int
		score int
	}
	groups := map[string][]entry{}
	for i, p := range papers {
		key := normTitle(p.Title)
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], entry{i, paperMetadataScore(p)})
	}

	remove := map[int]bool{}
	for _, entries := range groups {
		if len(entries) < 2 {
			continue
		}
		// Find the best entry; mark all others for removal.
		best := 0
		for i := 1; i < len(entries); i++ {
			if entries[i].score > entries[best].score {
				best = i
			}
		}
		for i, e := range entries {
			if i != best {
				remove[e.idx] = true
			}
		}
	}

	if len(remove) == 0 {
		return papers
	}

	result := make([]backends.Paper, 0, len(papers)-len(remove))
	for i, p := range papers {
		if !remove[i] {
			result = append(result, p)
		}
	}
	return result
}

// commonPrefixLen returns the length of the longest common prefix
// between two strings.
func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// paperMetadataScore ranks a paper by metadata richness for dedup.
// Higher = more useful to keep.
func paperMetadataScore(p backends.Paper) int {
	s := 0
	if strings.TrimSpace(p.Abstract) != "" {
		s += 4
	}
	if p.Citations > 0 {
		s += 2
	}
	if p.Year > 0 {
		s += 1
	}
	return s
}

// buildSummary computes a ResultSummary from a slice of papers:
// total count, min/max year, and titles of the top 3 by citation count.
func buildSummary(papers []backends.Paper) *backends.ResultSummary {
	s := &backends.ResultSummary{TotalPapers: len(papers)}

	// Year range (only from papers that have a year)
	minY, maxY := 0, 0
	for _, p := range papers {
		if p.Year <= 0 {
			continue
		}
		if minY == 0 || p.Year < minY {
			minY = p.Year
		}
		if p.Year > maxY {
			maxY = p.Year
		}
	}
	if minY > 0 {
		s.YearRange = [2]int{minY, maxY}
	}

	// Top 3 by citation count
	type ranked struct {
		title     string
		citations int
	}
	ranked_papers := make([]ranked, 0, len(papers))
	for _, p := range papers {
		if p.Citations > 0 {
			ranked_papers = append(ranked_papers, ranked{p.Title, p.Citations})
		}
	}
	sort.Slice(ranked_papers, func(i, j int) bool {
		return ranked_papers[i].citations > ranked_papers[j].citations
	})
	top := 3
	if len(ranked_papers) < top {
		top = len(ranked_papers)
	}
	for _, r := range ranked_papers[:top] {
		s.TopCited = append(s.TopCited, fmt.Sprintf("%s (%d citations)", r.title, r.citations))
	}

	return s
}

// keepTheFirstOfEachKind keeps, in rank order, up to limit books and
// up to limit papers (book chapters count as papers), and gives every
// record a kind.
func keepTheFirstOfEachKind(ranked []backends.Paper, limit int) []backends.Paper {
	kept := make([]backends.Paper, 0, 2*limit)
	books, papers := 0, 0
	for _, record := range ranked {
		if record.Kind == "" {
			record.Kind = backends.RecordKindPaper
		}
		if record.Kind == backends.RecordKindBook {
			if books == limit {
				continue
			}
			books++
		} else {
			if papers == limit {
				continue
			}
			papers++
		}
		kept = append(kept, record)
	}
	return kept
}

// splitBooksFromPapers puts records of kind book in their own list.
func splitBooksFromPapers(records []backends.Paper) (papers, books []backends.Paper) {
	for _, record := range records {
		if record.Kind == backends.RecordKindBook {
			books = append(books, record)
		} else {
			papers = append(papers, record)
		}
	}
	return papers, books
}
