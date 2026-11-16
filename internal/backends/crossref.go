package backends

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

const (
	crossrefBaseURL = "https://api.crossref.org"

	// Crossref assigns a pool per request: anonymous callers get the
	// public pool (1 req/s as observed in x-rate-limit-* headers,
	// 2026-09), callers whose User-Agent carries a mailto: get the
	// polite pool (3 req/s observed). These are the starting rates; the
	// client re-tunes from the headers Crossref sends back.
	crossrefPublicRPS = 1.0
	crossrefPoliteRPS = 3.0
)

// CrossrefBackend implements CitationBackend and SearchBackend using the Crossref API.
type CrossrefBackend struct {
	client *Client
}

// NewCrossref creates a new Crossref backend.
func NewCrossref(client *Client) *CrossrefBackend {
	rps := crossrefPublicRPS
	if client.email != "" {
		rps = crossrefPoliteRPS
	}
	ensureRateLimit("crossref", rps, 1)
	return &CrossrefBackend{client: client}
}

// Name returns the backend identifier.
func (c *CrossrefBackend) Name() string {
	return "crossref"
}

// HealthCheck tests if the Crossref API is reachable.
func (c *CrossrefBackend) HealthCheck(ctx context.Context) HealthStatus {
	url := crossrefBaseURL + "/works?rows=1"
	var resp crossrefSearchResponse
	if err := c.client.GetJSON(ctx, url, "crossref", &resp); err != nil {
		return HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("Crossref API error: %v", err),
		}
	}
	return HealthStatus{
		Healthy: true,
		Message: "Crossref API reachable",
	}
}

// VerifyDOI checks if a DOI exists and returns its metadata.
func (c *CrossrefBackend) VerifyDOI(ctx context.Context, doi string) (*VerificationResult, error) {
	doi = normalizeDOI(doi)
	url := fmt.Sprintf("%s/works/%s", crossrefBaseURL, url.PathEscape(doi))

	var resp crossrefResponse
	if err := c.client.GetJSON(ctx, url, "crossref", &resp); err != nil {
		if IsNotFound(err) {
			return &VerificationResult{
				Verified: false,
				Source:   "crossref",
			}, nil
		}
		return nil, fmt.Errorf("crossref API error: %w", err)
	}

	paper := workToPaper(resp.Message)
	return &VerificationResult{
		Verified:   true,
		Paper:      &paper,
		Source:     "crossref",
		Confidence: 1.0,
	}, nil
}

// VerifyByTitle searches for a paper by title/authors/year and scores the match.
func (c *CrossrefBackend) VerifyByTitle(ctx context.Context, title string, authors []string, year int) (*VerificationResult, error) {
	// Search by title
	searchURL := fmt.Sprintf("%s/works?query.title=%s&rows=5", crossrefBaseURL, url.QueryEscape(title))
	var resp crossrefSearchResponse
	if err := c.client.GetJSON(ctx, searchURL, "crossref", &resp); err != nil {
		return nil, fmt.Errorf("crossref search error: %w", err)
	}

	if len(resp.Message.Items) == 0 {
		return &VerificationResult{
			Verified: false,
			Source:   "crossref",
		}, nil
	}

	// Find the best matching result
	var bestMatch *crossrefWork
	var bestScore float64

	for i := range resp.Message.Items {
		work := &resp.Message.Items[i]
		score := matchConfidence(title, authors, year, work)
		if score > bestScore {
			bestScore = score
			bestMatch = work
		}
	}

	// Threshold for considering a match valid
	if bestScore < 0.5 {
		return &VerificationResult{
			Verified: false,
			Source:   "crossref",
		}, nil
	}

	paper := workToPaper(*bestMatch)
	return &VerificationResult{
		Verified:   true,
		Paper:      &paper,
		Source:     "crossref",
		Confidence: bestScore,
	}, nil
}

// Search returns papers matching the query, blending relevance-ordered
// and citation-ordered results to surface both recent and canonical work.
// Year filters are applied as native Crossref filter parameters.
// Citation count filtering is not supported natively by Crossref and
// is handled post-retrieval by the caller.
func (c *CrossrefBackend) Search(ctx context.Context, query string, limit int, filters SearchFilters) ([]Paper, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	escaped := url.QueryEscape(query)

	// Build native filter string for year range.
	// Crossref uses from-pub-date / until-pub-date (YYYY-MM-DD or YYYY).
	var filterParts []string
	if filters.YearMin > 0 {
		filterParts = append(filterParts, fmt.Sprintf("from-pub-date:%d", filters.YearMin))
	}
	if filters.YearMax > 0 {
		filterParts = append(filterParts, fmt.Sprintf("until-pub-date:%d", filters.YearMax))
	}

	filterParam := ""
	if len(filterParts) > 0 {
		filterParam = "&filter=" + strings.Join(filterParts, ",")
	}

	relevanceURL := fmt.Sprintf("%s/works?query=%s&rows=%d%s", crossrefBaseURL, escaped, limit, filterParam)
	citationURL := fmt.Sprintf("%s/works?query=%s&rows=%d&sort=is-referenced-by-count&order=desc%s", crossrefBaseURL, escaped, limit, filterParam)

	// If the query contains UK/US spelling variants, send an extra
	// relevance query with the other spelling.
	variant := QuerySpellingVariant(query)
	var variantURL string
	if variant != "" {
		variantURL = fmt.Sprintf("%s/works?query=%s&rows=%d%s", crossrefBaseURL, url.QueryEscape(variant), limit, filterParam)
	}

	var wg sync.WaitGroup
	var relResp, citResp, varResp crossrefSearchResponse
	var relErr, citErr, varErr error

	wg.Add(2)
	if variantURL != "" {
		wg.Add(1)
	}
	go func() {
		defer wg.Done()
		relErr = c.client.GetJSON(ctx, relevanceURL, "crossref", &relResp)
	}()
	go func() {
		defer wg.Done()
		citErr = c.client.GetJSON(ctx, citationURL, "crossref", &citResp)
	}()
	if variantURL != "" {
		go func() {
			defer wg.Done()
			varErr = c.client.GetJSON(ctx, variantURL, "crossref", &varResp)
		}()
	}
	wg.Wait()

	// Merge unique results from all legs, capped at 2*limit. Relevance
	// and spelling-variant results fill first (they're targeted); the
	// citation-sorted leg gets remaining slots.
	cap := limit * 2
	seen := make(map[string]bool)
	papers := make([]Paper, 0, cap)

	addWork := func(w crossrefWork) {
		if len(papers) >= cap {
			return
		}
		p := workToPaper(w)
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

	relItems := relResp.Message.Items
	citItems := citResp.Message.Items
	if relErr != nil {
		relItems = nil
	}
	if citErr != nil {
		citItems = nil
	}
	varItems := varResp.Message.Items
	if varErr != nil {
		varItems = nil
	}

	// Relevance and variant first (targeted results), citation-sorted last.
	for _, w := range relItems {
		addWork(w)
	}
	for _, w := range varItems {
		addWork(w)
	}
	for _, w := range citItems {
		addWork(w)
	}

	if len(papers) == 0 && relErr != nil {
		return nil, fmt.Errorf("crossref search error: %w", relErr)
	}

	return papers, nil
}

// Crossref API response types

type crossrefResponse struct {
	Message crossrefWork `json:"message"`
}

type crossrefSearchResponse struct {
	Message struct {
		Items []crossrefWork `json:"items"`
	} `json:"message"`
}

type crossrefWork struct {
	DOI    string   `json:"DOI"`
	Type   string   `json:"type"`
	Title  []string `json:"title"`
	Author []struct {
		Given  string `json:"given"`
		Family string `json:"family"`
	} `json:"author"`
	Published struct {
		DateParts [][]int `json:"date-parts"`
	} `json:"published"`
	ContainerTitle      []string `json:"container-title"`
	IsReferencedByCount int      `json:"is-referenced-by-count"`
	Abstract            string   `json:"abstract"`
	URL                 string   `json:"URL"`
}

// Helper functions

// NormalisedDOI is normalizeDOI for callers outside this package.
func NormalisedDOI(doi string) string {
	return normalizeDOI(doi)
}

// normalizeDOI strips common DOI URL prefixes.
func normalizeDOI(doi string) string {
	doi = strings.TrimSpace(doi)
	prefixes := []string{
		"https://doi.org/",
		"http://doi.org/",
		"https://dx.doi.org/",
		"http://dx.doi.org/",
		"doi:",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(strings.ToLower(doi), strings.ToLower(prefix)) {
			doi = doi[len(prefix):]
			break
		}
	}
	doi = strings.ToLower(doi)
	// Collapse double slashes (APA-style DOIs like 10.1037//0022-3514.52.3.511).
	doi = strings.ReplaceAll(doi, "//", "/")
	return doi
}

// workToPaper converts a Crossref work to a Paper.
func workToPaper(work crossrefWork) Paper {
	// Extract title
	title := ""
	if len(work.Title) > 0 {
		title = work.Title[0]
	}

	// Extract authors
	authors := make([]string, 0, len(work.Author))
	for _, a := range work.Author {
		name := strings.TrimSpace(a.Given + " " + a.Family)
		if name != "" {
			authors = append(authors, name)
		}
	}

	// Extract year
	year := 0
	if len(work.Published.DateParts) > 0 && len(work.Published.DateParts[0]) > 0 {
		year = work.Published.DateParts[0][0]
	}

	// Extract journal
	journal := ""
	if len(work.ContainerTitle) > 0 {
		journal = work.ContainerTitle[0]
	}

	return Paper{
		Kind:      RecordKindFromPublicationType(work.Type),
		Title:     title,
		Authors:   authors,
		Year:      year,
		DOI:       work.DOI,
		Abstract:  stripXMLTags(work.Abstract),
		URL:       work.URL,
		Source:    "crossref",
		Journal:   journal,
		Citations: work.IsReferencedByCount,
	}
}

// jaccardSimilarity computes the Jaccard similarity between two strings based on word sets.
func jaccardSimilarity(a, b string) float64 {
	wordsA := tokenize(a)
	wordsB := tokenize(b)

	if len(wordsA) == 0 && len(wordsB) == 0 {
		return 1.0
	}
	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0.0
	}

	setA := make(map[string]bool)
	for _, w := range wordsA {
		setA[w] = true
	}

	setB := make(map[string]bool)
	for _, w := range wordsB {
		setB[w] = true
	}

	// Intersection
	intersection := 0
	for w := range setA {
		if setB[w] {
			intersection++
		}
	}

	// Union
	union := make(map[string]bool)
	for w := range setA {
		union[w] = true
	}
	for w := range setB {
		union[w] = true
	}

	return float64(intersection) / float64(len(union))
}

// tokenize splits a string into lowercase words.
func tokenize(s string) []string {
	s = strings.ToLower(s)
	// Simple word splitting - split on non-alphanumeric
	var words []string
	var current strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			current.WriteRune(r)
		} else if current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		words = append(words, current.String())
	}
	return words
}

// matchConfidence computes a weighted confidence score for a match.
// Weights: title (60%), year (20%), authors (20%).
// Post-processing: year gap penalty and first-author gate.
func matchConfidence(queryTitle string, queryAuthors []string, queryYear int, work *crossrefWork) float64 {
	score := 0.0

	// Title similarity (60%)
	if len(work.Title) > 0 {
		titleSim := jaccardSimilarity(queryTitle, work.Title[0])
		score += 0.6 * titleSim
	}

	// Year match (20%)
	workYear := 0
	if len(work.Published.DateParts) > 0 && len(work.Published.DateParts[0]) > 0 {
		workYear = work.Published.DateParts[0][0]
	}
	if queryYear > 0 && workYear > 0 {
		if queryYear == workYear {
			score += 0.2
		} else if abs(queryYear-workYear) <= 1 {
			// Allow 1 year difference (publication delays)
			score += 0.1
		}
	}

	// Author match (20%)
	if len(queryAuthors) > 0 && len(work.Author) > 0 {
		authorScore := authorSimilarity(queryAuthors, work.Author)
		score += 0.2 * authorScore
	}

	// Year gap penalty: large gaps decay the score multiplicatively
	score *= yearPenalty(queryYear, workYear)

	// First-author gate: if the first query author's family name
	// doesn't appear among the work's authors, cap below threshold
	workFamilies := make([]string, 0, len(work.Author))
	for _, a := range work.Author {
		if a.Family != "" {
			workFamilies = append(workFamilies, a.Family)
		}
	}
	if !firstAuthorMatchesAny(queryAuthors, workFamilies) {
		if score > 0.4 {
			score = 0.4
		}
	}

	return score
}

// authorSimilarity computes how well query authors match work authors.
func authorSimilarity(queryAuthors []string, workAuthors []struct {
	Given  string `json:"given"`
	Family string `json:"family"`
}) float64 {
	if len(queryAuthors) == 0 || len(workAuthors) == 0 {
		return 0.0
	}

	// Build work author full names and family names
	workNames := make([]string, 0, len(workAuthors))
	for _, a := range workAuthors {
		name := strings.ToLower(strings.TrimSpace(a.Given + " " + a.Family))
		if name != "" {
			workNames = append(workNames, name)
		}
	}

	if len(workNames) == 0 {
		return 0.0
	}

	// Count matches (once per query author)
	matches := 0
	for _, qa := range queryAuthors {
		qaLower := strings.ToLower(qa)
		matched := false
		// Full-name substring match
		for _, wn := range workNames {
			if strings.Contains(wn, qaLower) || strings.Contains(qaLower, wn) {
				matched = true
				break
			}
		}
		// Fall back to family-name match
		if !matched {
			for _, a := range workAuthors {
				if a.Family != "" && (strings.EqualFold(a.Family, qa) || strings.Contains(qaLower, strings.ToLower(a.Family))) {
					matched = true
					break
				}
			}
		}
		if matched {
			matches++
		}
	}

	return float64(matches) / float64(len(queryAuthors))
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// yearPenalty returns a multiplicative penalty for year mismatches.
// Gaps of 0-2 years are tolerated (returns 1.0). Larger gaps decay
// the score, clamped at 0.2 for gaps of 16+ years.
func yearPenalty(queryYear, workYear int) float64 {
	if queryYear <= 0 || workYear <= 0 {
		return 1.0
	}
	gap := abs(queryYear - workYear)
	if gap <= 2 {
		return 1.0
	}
	p := 1.0 - float64(gap)*0.05
	if p < 0.2 {
		return 0.2
	}
	return p
}

// extractFamilyName pulls the family name from an author string.
// Handles "Last, First" (returns "Last") and "First Last" (returns "Last").
func extractFamilyName(author string) string {
	author = strings.TrimSpace(author)
	if author == "" {
		return ""
	}
	if idx := strings.Index(author, ","); idx > 0 {
		return strings.TrimSpace(author[:idx])
	}
	parts := strings.Fields(author)
	return parts[len(parts)-1]
}

// firstAuthorMatchesAny checks whether the first query author's family
// name appears among the work's author family names. Returns true if
// queryAuthors is empty (no constraint to check).
func firstAuthorMatchesAny(queryAuthors []string, workFamilyNames []string) bool {
	if len(queryAuthors) == 0 || len(workFamilyNames) == 0 {
		return true
	}
	queryFamily := strings.ToLower(extractFamilyName(queryAuthors[0]))
	if queryFamily == "" {
		return true
	}
	for _, wf := range workFamilyNames {
		if strings.ToLower(wf) == queryFamily {
			return true
		}
	}
	return false
}
