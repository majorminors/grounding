package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

const (
	openalexDefaultBaseURL = "https://api.openalex.org"

	// OpenAlex no longer rate-limits by request rate below 100 req/s;
	// since 2026 it meters a daily credit budget instead (a list query
	// costs 10 credits, a single-work GET is free; keyless callers get
	// 1000 credits a day per IP, a free key ten times that). The client
	// side limit here is politeness and burst smoothing, nothing more.
	openalexRPS = 10.0

	// openalexSelectFields trims each work to what we read. Responses
	// carry dozens of fields we never look at; asking for these alone
	// roughly halves the bytes per result.
	openalexSelectFields = "id,doi,type,title,publication_year,cited_by_count,authorships,primary_location,open_access,referenced_works,topics,abstract_inverted_index"
)

// openalexDomainIDs maps display names to OpenAlex numeric domain IDs.
// The filter API accepts bare integers, not full URLs.
var openalexDomainIDs = map[string]string{
	"Life Sciences":     "1",
	"Social Sciences":   "2",
	"Physical Sciences": "3",
	"Health Sciences":   "4",
}

// openalexDomainID returns the OA domain ID URL for a display name.
func openalexDomainID(displayName string) string {
	return openalexDomainIDs[displayName]
}

// OpenAlexBackend implements CitationBackend and SearchBackend using the OpenAlex API.
type OpenAlexBackend struct {
	client *Client
	apiKey string
	// baseURL is openalexDefaultBaseURL; a test points it elsewhere.
	baseURL string
}

// NewOpenAlex creates a new OpenAlex backend. apiKey may be empty;
// see config.OpenAlexAPIKey for why you want one on a shared server.
func NewOpenAlex(client *Client, apiKey string) *OpenAlexBackend {
	ensureRateLimit("openalex", openalexRPS, 1)
	return &OpenAlexBackend{client: client, apiKey: apiKey, baseURL: openalexDefaultBaseURL}
}

// getJSON issues an OpenAlex request with the API key (when configured)
// and the field selection every caller wants. Centralising the select
// parameter here keeps the URL builders readable.
func (o *OpenAlexBackend) getJSON(ctx context.Context, requestURL string, result any) error {
	if !strings.Contains(requestURL, "select=") {
		separator := "?"
		if strings.Contains(requestURL, "?") {
			separator = "&"
		}
		requestURL += separator + "select=" + openalexSelectFields
	}
	headers := map[string]string{}
	if o.apiKey != "" {
		headers["Authorization"] = "Bearer " + o.apiKey
	}
	body, err := o.client.Do(ctx, Request{
		URL:     requestURL,
		Backend: "openalex",
		Headers: headers,
	})
	if err != nil {
		return err
	}
	return json.Unmarshal(body, result)
}

// Name returns the backend identifier.
func (o *OpenAlexBackend) Name() string {
	return "openalex"
}

// HealthCheck tests if the OpenAlex API is reachable.
func (o *OpenAlexBackend) HealthCheck(ctx context.Context) HealthStatus {
	checkURL := o.baseURL + "/works?per_page=1"
	var resp openalexSearchResponse
	if err := o.getJSON(ctx, checkURL, &resp); err != nil {
		return HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("OpenAlex API error: %v", err),
		}
	}
	return HealthStatus{
		Healthy: true,
		Message: "OpenAlex API reachable",
	}
}

// VerifyDOI checks if a DOI exists and returns its metadata.
func (o *OpenAlexBackend) VerifyDOI(ctx context.Context, doi string) (*VerificationResult, error) {
	doi = normalizeDOI(doi)
	// OpenAlex uses https://doi.org/ prefix for DOI lookups
	lookupURL := fmt.Sprintf("%s/works/https://doi.org/%s", o.baseURL, url.PathEscape(doi))

	var work openalexWork
	if err := o.getJSON(ctx, lookupURL, &work); err != nil {
		if IsNotFound(err) {
			return &VerificationResult{
				Verified: false,
				Source:   "openalex",
			}, nil
		}
		return nil, fmt.Errorf("openalex API error: %w", err)
	}

	paper := openalexWorkToPaper(work)
	return &VerificationResult{
		Verified:   true,
		Paper:      &paper,
		Source:     "openalex",
		Confidence: 1.0,
	}, nil
}

// VerifyByTitle searches for a paper by title/authors/year and scores the match.
func (o *OpenAlexBackend) VerifyByTitle(ctx context.Context, title string, authors []string, year int) (*VerificationResult, error) {
	searchURL := fmt.Sprintf("%s/works?search=%s&per_page=5", o.baseURL, url.QueryEscape(title))

	var resp openalexSearchResponse
	if err := o.getJSON(ctx, searchURL, &resp); err != nil {
		return nil, fmt.Errorf("openalex search error: %w", err)
	}

	if len(resp.Results) == 0 {
		return &VerificationResult{
			Verified: false,
			Source:   "openalex",
		}, nil
	}

	// Find best match
	var bestMatch *openalexWork
	var bestScore float64

	for i := range resp.Results {
		work := &resp.Results[i]
		score := oaMatchConfidence(title, authors, year, work)
		if score > bestScore {
			bestScore = score
			bestMatch = work
		}
	}

	// Threshold for valid match
	if bestScore < 0.5 {
		return &VerificationResult{
			Verified: false,
			Source:   "openalex",
		}, nil
	}

	paper := openalexWorkToPaper(*bestMatch)
	return &VerificationResult{
		Verified:   true,
		Paper:      &paper,
		Source:     "openalex",
		Confidence: bestScore,
	}, nil
}

// Search returns papers matching the query, blending relevance-ordered
// and citation-ordered results to surface both recent and canonical work.
// Filters are applied as native OpenAlex API parameters.
func (o *OpenAlexBackend) Search(ctx context.Context, query string, limit int, filters SearchFilters) ([]Paper, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	escaped := url.QueryEscape(query)

	// Build native filter string for year range and citation count.
	var filterParts []string
	if filters.YearMin > 0 {
		filterParts = append(filterParts, fmt.Sprintf("from_publication_date:%d-01-01", filters.YearMin))
	}
	if filters.YearMax > 0 {
		filterParts = append(filterParts, fmt.Sprintf("to_publication_date:%d-12-31", filters.YearMax))
	}
	if filters.MinCitations > 0 {
		filterParts = append(filterParts, fmt.Sprintf("cited_by_count:>%d", filters.MinCitations-1))
	}
	if filters.Domain != "" {
		if domainID := openalexDomainID(filters.Domain); domainID != "" {
			filterParts = append(filterParts, "primary_topic.domain.id:"+domainID)
		}
	}

	filterParam := ""
	if len(filterParts) > 0 {
		filterParam = "&filter=" + url.QueryEscape(strings.Join(filterParts, ","))
	}

	relevanceURL := fmt.Sprintf("%s/works?search=%s&per_page=%d%s", o.baseURL, escaped, limit, filterParam)
	citationURL := fmt.Sprintf("%s/works?search=%s&per_page=%d&sort=cited_by_count:desc%s", o.baseURL, escaped, limit, filterParam)

	// If the query contains UK/US spelling variants, send an extra
	// relevance query with the other spelling to catch papers that
	// use the alternate convention (e.g. "labor" vs "labour").
	variant := QuerySpellingVariant(query)
	var variantURL string
	if variant != "" {
		variantURL = fmt.Sprintf("%s/works?search=%s&per_page=%d%s", o.baseURL, url.QueryEscape(variant), limit, filterParam)
	}

	var wg sync.WaitGroup
	var relResp, citResp, varResp openalexSearchResponse
	var relErr, citErr, varErr error

	wg.Add(2)
	if variantURL != "" {
		wg.Add(1)
	}
	go func() {
		defer wg.Done()
		relErr = o.getJSON(ctx, relevanceURL, &relResp)
	}()
	go func() {
		defer wg.Done()
		citErr = o.getJSON(ctx, citationURL, &citResp)
	}()
	if variantURL != "" {
		go func() {
			defer wg.Done()
			varErr = o.getJSON(ctx, variantURL, &varResp)
		}()
	}
	wg.Wait()

	// Merge unique results from all legs, capped at 2*limit. Relevance
	// and spelling-variant results fill first (they're targeted); the
	// citation-sorted leg gets remaining slots. This ensures the
	// citation-sorted leg contributes canonical papers without flooding
	// the caller with irrelevant high-citation results.
	cap := limit * 2
	seen := make(map[string]bool)
	papers := make([]Paper, 0, cap)

	addWork := func(w openalexWork) {
		if len(papers) >= cap {
			return
		}
		p := openalexWorkToPaper(w)
		key := strings.ToLower(p.DOI)
		if key == "" {
			key = "title:" + p.Title
		}
		if seen[key] {
			return
		}
		seen[key] = true
		papers = append(papers, p)
	}

	relResults := relResp.Results
	citResults := citResp.Results
	if relErr != nil {
		relResults = nil
	}
	if citErr != nil {
		citResults = nil
	}
	varResults := varResp.Results
	if varErr != nil {
		varResults = nil
	}

	// Relevance and variant first (targeted results), citation-sorted last.
	for _, w := range relResults {
		addWork(w)
	}
	for _, w := range varResults {
		addWork(w)
	}
	for _, w := range citResults {
		addWork(w)
	}

	if len(papers) == 0 && relErr != nil {
		return nil, fmt.Errorf("openalex search error: %w", relErr)
	}

	return papers, nil
}

// ResolvePaper looks up a paper by DOI and returns the converted Paper
// plus the OpenAlex work ID (e.g. "W2316701970").
func (o *OpenAlexBackend) ResolvePaper(ctx context.Context, doi string) (*Paper, string, error) {
	doi = normalizeDOI(doi)
	lookupURL := fmt.Sprintf("%s/works/https://doi.org/%s", o.baseURL, url.PathEscape(doi))

	var work openalexWork
	if err := o.getJSON(ctx, lookupURL, &work); err != nil {
		return nil, "", fmt.Errorf("openalex resolve paper: %w", err)
	}

	p := openalexWorkToPaper(work)
	return &p, extractOAID(work.ID), nil
}

// ResolveBySearch searches for a paper by title and resolves the top
// result, returning the converted Paper and OpenAlex work ID.
func (o *OpenAlexBackend) ResolveBySearch(ctx context.Context, query string) (*Paper, string, error) {
	searchURL := fmt.Sprintf("%s/works?search=%s&per_page=1", o.baseURL, url.QueryEscape(query))

	var resp openalexSearchResponse
	if err := o.getJSON(ctx, searchURL, &resp); err != nil {
		return nil, "", fmt.Errorf("openalex search fallback: %w", err)
	}
	if len(resp.Results) == 0 {
		return nil, "", fmt.Errorf("no paper found for %q", query)
	}

	work := resp.Results[0]
	p := openalexWorkToPaper(work)
	return &p, extractOAID(work.ID), nil
}

// FetchCitations returns papers that cite the given paper.
// Uses the OpenAlex filter API: /works?filter=cites:{oaID}
// sortBy controls ordering: "citations" sorts by cited_by_count:desc,
// "recency" (or empty) sorts by publication_date:desc.
func (o *OpenAlexBackend) FetchCitations(ctx context.Context, oaID string, limit int, sortBy string) ([]CitationEdge, error) {
	if limit > 200 {
		limit = 200
	}
	sortClause := "publication_date:desc"
	if sortBy == "citations" {
		sortClause = "cited_by_count:desc"
	}
	citURL := fmt.Sprintf("%s/works?filter=cites:%s&per_page=%d&sort=%s",
		o.baseURL, url.QueryEscape(oaID), limit, sortClause)

	var resp openalexSearchResponse
	if err := o.getJSON(ctx, citURL, &resp); err != nil {
		return nil, fmt.Errorf("openalex fetch citations: %w", err)
	}

	edges := make([]CitationEdge, 0, len(resp.Results))
	for _, w := range resp.Results {
		if w.Title == "" {
			continue
		}
		edges = append(edges, CitationEdge{
			Paper: openalexWorkToPaper(w),
		})
	}
	return edges, nil
}

// FetchReferences returns papers cited BY the given paper.
// Fetches the work to get referenced_works IDs, then batch-fetches them.
func (o *OpenAlexBackend) FetchReferences(ctx context.Context, oaID string, limit int) ([]CitationEdge, error) {
	// First fetch the work itself to get referenced_works
	workURL := fmt.Sprintf("%s/works/%s", o.baseURL, url.PathEscape(oaID))

	var work openalexWork
	if err := o.getJSON(ctx, workURL, &work); err != nil {
		return nil, fmt.Errorf("openalex fetch work: %w", err)
	}

	if len(work.ReferencedWorks) == 0 {
		return nil, nil
	}

	// Truncate to limit
	refs := work.ReferencedWorks
	if len(refs) > limit {
		refs = refs[:limit]
	}

	// Batch-fetch referenced works using pipe-separated filter.
	// OpenAlex supports up to 50 IDs per filter, so batch if needed;
	// the batches run at once (fetchOpenAlexBatchesAtOnce).
	ids := make([]string, len(refs))
	for j, ref := range refs {
		ids[j] = extractOAID(ref)
	}
	papers, err := o.fetchOpenAlexBatchesAtOnce(ctx, ids, func(batch []string) string {
		return fmt.Sprintf("%s/works?filter=openalex:%s&per_page=%d",
			o.baseURL, url.QueryEscape(strings.Join(batch, "|")), len(batch))
	})
	if err != nil {
		return nil, fmt.Errorf("openalex fetch references batch: %w", err)
	}
	allEdges := make([]CitationEdge, 0, len(papers))
	for _, p := range papers {
		allEdges = append(allEdges, CitationEdge{Paper: p})
	}
	return allEdges, nil
}

// FetchWorksByDOIs looks up works for a list of DOIs in batches of 50
// (the filter's OR limit). DOIs OpenAlex doesn't know are simply absent
// from the result. Used to put metadata on the bare DOIs OpenCitations
// returns.
func (o *OpenAlexBackend) FetchWorksByDOIs(ctx context.Context, dois []string) ([]Paper, error) {
	papers, err := o.fetchOpenAlexBatchesAtOnce(ctx, dois, func(batch []string) string {
		return fmt.Sprintf("%s/works?filter=doi:%s&per_page=%d",
			o.baseURL, url.QueryEscape(strings.Join(batch, "|")), len(batch))
	})
	if err != nil {
		return nil, fmt.Errorf("openalex fetch works by doi: %w", err)
	}
	return papers, nil
}

// openAlexFilterBatchSize is the most values one OpenAlex filter ORs.
const openAlexFilterBatchSize = 50

// fetchOpenAlexBatchesAtOnce splits keys into batches of the filter's
// limit, fetches every batch at once (the limiter paces them), and
// returns the titled works in the keys' order. Any batch failing fails
// the whole, as the one-at-a-time loops did.
func (o *OpenAlexBackend) fetchOpenAlexBatchesAtOnce(ctx context.Context, keys []string, urlForBatch func(batch []string) string) ([]Paper, error) {
	var batches [][]string
	for start := 0; start < len(keys); start += openAlexFilterBatchSize {
		end := start + openAlexFilterBatchSize
		if end > len(keys) {
			end = len(keys)
		}
		batches = append(batches, keys[start:end])
	}
	results := make([]openalexSearchResponse, len(batches))
	errs := make([]error, len(batches))
	var wg sync.WaitGroup
	for i, batch := range batches {
		wg.Add(1)
		go func(i int, batch []string) {
			defer wg.Done()
			errs[i] = o.getJSON(ctx, urlForBatch(batch), &results[i])
		}(i, batch)
	}
	wg.Wait()
	var papers []Paper
	for i := range batches {
		if errs[i] != nil {
			return nil, errs[i]
		}
		for _, w := range results[i].Results {
			if w.Title == "" {
				continue
			}
			papers = append(papers, openalexWorkToPaper(w))
		}
	}
	return papers, nil
}

// extractOAID extracts the short OpenAlex ID from a full URL.
// "https://openalex.org/W2316701970" -> "W2316701970"
func extractOAID(fullID string) string {
	if i := strings.LastIndex(fullID, "/"); i >= 0 {
		return fullID[i+1:]
	}
	return fullID
}

// OpenAlex API response types

type openalexSearchResponse struct {
	Results []openalexWork `json:"results"`
	Meta    struct {
		Count int `json:"count"`
	} `json:"meta"`
}

type openalexWork struct {
	ID              string `json:"id"`
	DOI             string `json:"doi"`
	Type            string `json:"type"`
	Title           string `json:"title"`
	PublicationYear int    `json:"publication_year"`
	CitedByCount    int    `json:"cited_by_count"`
	Authorships     []struct {
		Author struct {
			DisplayName string `json:"display_name"`
		} `json:"author"`
	} `json:"authorships"`
	PrimaryLocation *struct {
		Source *struct {
			DisplayName string `json:"display_name"`
		} `json:"source"`
		LandingPageURL string `json:"landing_page_url"`
		PDFURL         string `json:"pdf_url"`
	} `json:"primary_location"`
	OpenAccess *struct {
		OAURL string `json:"oa_url"`
	} `json:"open_access"`
	ReferencedWorks []string `json:"referenced_works"`
	// OpenAlex publishes abstracts only as an inverted index (word ->
	// positions) for licensing reasons; openalexAbstractFromInvertedIndex
	// turns it back into prose.
	AbstractInvertedIndex map[string][]int `json:"abstract_inverted_index"`
	Topics                []struct {
		DisplayName string `json:"display_name"`
		Subfield    struct {
			DisplayName string `json:"display_name"`
		} `json:"subfield"`
		Field struct {
			DisplayName string `json:"display_name"`
		} `json:"field"`
		Domain struct {
			DisplayName string `json:"display_name"`
		} `json:"domain"`
	} `json:"topics"`
}

// openalexWorkToPaper converts an OpenAlex work to a Paper.
func openalexWorkToPaper(w openalexWork) Paper {
	// Extract authors
	authors := make([]string, 0, len(w.Authorships))
	for _, a := range w.Authorships {
		if a.Author.DisplayName != "" {
			authors = append(authors, a.Author.DisplayName)
		}
	}

	// Extract DOI (OpenAlex returns full URL like "https://doi.org/10.1234/...")
	doi := normalizeDOI(w.DOI)

	// Extract journal
	journal := ""
	if w.PrimaryLocation != nil && w.PrimaryLocation.Source != nil {
		journal = w.PrimaryLocation.Source.DisplayName
	}

	// Determine best URL: prefer PDF > OA > landing page
	paperURL := ""
	if w.PrimaryLocation != nil {
		if w.PrimaryLocation.PDFURL != "" {
			paperURL = w.PrimaryLocation.PDFURL
		} else if w.OpenAccess != nil && w.OpenAccess.OAURL != "" {
			paperURL = w.OpenAccess.OAURL
		} else if w.PrimaryLocation.LandingPageURL != "" {
			paperURL = w.PrimaryLocation.LandingPageURL
		}
	} else if w.OpenAccess != nil && w.OpenAccess.OAURL != "" {
		paperURL = w.OpenAccess.OAURL
	}

	var domain string
	if len(w.Topics) > 0 {
		domain = w.Topics[0].Domain.DisplayName
	}

	return Paper{
		Kind:      RecordKindFromPublicationType(w.Type),
		Title:     w.Title,
		Authors:   authors,
		Year:      w.PublicationYear,
		DOI:       doi,
		Abstract:  openalexAbstractFromInvertedIndex(w.AbstractInvertedIndex),
		URL:       paperURL,
		Source:    "openalex",
		Journal:   journal,
		Citations: w.CitedByCount,
		Domain:    domain,
	}
}

// openalexAbstractFromInvertedIndex rebuilds an abstract from OpenAlex's
// word -> positions map. Positions are zero-based and dense; a gap
// (which does occur in practice) is simply skipped rather than padded.
func openalexAbstractFromInvertedIndex(index map[string][]int) string {
	if len(index) == 0 {
		return ""
	}
	maxPosition := -1
	for _, positions := range index {
		for _, position := range positions {
			if position > maxPosition {
				maxPosition = position
			}
		}
	}
	if maxPosition < 0 {
		return ""
	}
	words := make([]string, maxPosition+1)
	for word, positions := range index {
		for _, position := range positions {
			if position >= 0 {
				words[position] = word
			}
		}
	}
	filled := words[:0]
	for _, word := range words {
		if word != "" {
			filled = append(filled, word)
		}
	}
	return strings.TrimSpace(strings.Join(filled, " "))
}

// oaMatchConfidence computes a weighted confidence score for an OpenAlex work match.
// Weights: title (60%), year (20%), authors (20%).
// Post-processing: year gap penalty and first-author gate.
func oaMatchConfidence(queryTitle string, queryAuthors []string, queryYear int, work *openalexWork) float64 {
	score := 0.0

	// Title similarity (60%)
	if work.Title != "" {
		titleSim := jaccardSimilarity(queryTitle, work.Title)
		score += 0.6 * titleSim
	}

	// Year match (20%)
	if queryYear > 0 && work.PublicationYear > 0 {
		if queryYear == work.PublicationYear {
			score += 0.2
		} else if abs(queryYear-work.PublicationYear) <= 1 {
			// Allow 1 year difference (publication delays)
			score += 0.1
		}
	}

	// Author match (20%)
	if len(queryAuthors) > 0 && len(work.Authorships) > 0 {
		authorScore := oaAuthorSimilarity(queryAuthors, work.Authorships)
		score += 0.2 * authorScore
	}

	// Year gap penalty
	score *= yearPenalty(queryYear, work.PublicationYear)

	// First-author gate: extract family names from OA display names
	workFamilies := make([]string, 0, len(work.Authorships))
	for _, a := range work.Authorships {
		if a.Author.DisplayName != "" {
			workFamilies = append(workFamilies, extractFamilyName(a.Author.DisplayName))
		}
	}
	if !firstAuthorMatchesAny(queryAuthors, workFamilies) {
		if score > 0.4 {
			score = 0.4
		}
	}

	return score
}

// oaAuthorSimilarity computes how well query authors match work authors.
func oaAuthorSimilarity(queryAuthors []string, authorships []struct {
	Author struct {
		DisplayName string `json:"display_name"`
	} `json:"author"`
}) float64 {
	if len(queryAuthors) == 0 || len(authorships) == 0 {
		return 0.0
	}

	matches := 0
	for _, qa := range queryAuthors {
		qaTokens := tokenize(qa)
		for _, a := range authorships {
			paTokens := tokenize(a.Author.DisplayName)
			// Check if any token from query author appears in paper author
			for _, qt := range qaTokens {
				for _, pt := range paTokens {
					if qt == pt && len(qt) > 1 { // Avoid single-letter matches
						matches++
						goto nextAuthor
					}
				}
			}
		}
	nextAuthor:
	}

	return float64(matches) / float64(len(queryAuthors))
}
