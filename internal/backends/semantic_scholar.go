package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

const (
	semanticScholarBaseURL = "https://api.semanticscholar.org/graph/v1"
	semanticScholarFields  = "title,authors,year,abstract,externalIds,citationCount,venue,url,openAccessPdf"
	// Keyed access is 1 req/s per key. Keyless access draws on a pool
	// shared by every unauthenticated client in the world, which 429s
	// unpredictably regardless of our own pace; the limiter cannot help
	// with that, only the cooldown can.
	semanticScholarRPS = 1.0
)

// SemanticScholarBackend implements CitationBackend and SearchBackend using the Semantic Scholar API.
type SemanticScholarBackend struct {
	client *Client
	apiKey string
}

// NewSemanticScholar creates a new Semantic Scholar backend.
func NewSemanticScholar(client *Client, apiKey string) *SemanticScholarBackend {
	ensureRateLimit("semantic-scholar", semanticScholarRPS, 1)
	return &SemanticScholarBackend{client: client, apiKey: apiKey}
}

// Name returns the backend identifier.
func (s *SemanticScholarBackend) Name() string {
	return "semantic-scholar"
}

// HealthCheck tests if the Semantic Scholar API is reachable.
func (s *SemanticScholarBackend) HealthCheck(ctx context.Context) HealthStatus {
	// Search for a common term to verify API is working
	searchURL := fmt.Sprintf("%s/paper/search?query=test&limit=1&fields=%s", semanticScholarBaseURL, semanticScholarFields)
	var resp ssSearchResponse
	if err := s.getJSON(ctx, searchURL, &resp); err != nil {
		return HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("Semantic Scholar API error: %v", err),
		}
	}
	return HealthStatus{
		Healthy: true,
		Message: "Semantic Scholar API reachable",
	}
}

// VerifyDOI checks if a DOI exists and returns its metadata.
func (s *SemanticScholarBackend) VerifyDOI(ctx context.Context, doi string) (*VerificationResult, error) {
	doi = normalizeDOI(doi)
	lookupURL := fmt.Sprintf("%s/paper/DOI:%s?fields=%s", semanticScholarBaseURL, url.PathEscape(doi), semanticScholarFields)

	var paper ssPaper
	if err := s.getJSON(ctx, lookupURL, &paper); err != nil {
		if IsNotFound(err) {
			return &VerificationResult{
				Verified: false,
				Source:   "semantic-scholar",
			}, nil
		}
		return nil, fmt.Errorf("semantic scholar API error: %w", err)
	}

	result := ssPaperToPaper(paper)
	return &VerificationResult{
		Verified:   true,
		Paper:      &result,
		Source:     "semantic-scholar",
		Confidence: 1.0,
	}, nil
}

// VerifyByTitle searches for a paper by title/authors/year and scores the match.
func (s *SemanticScholarBackend) VerifyByTitle(ctx context.Context, title string, authors []string, year int) (*VerificationResult, error) {
	searchURL := fmt.Sprintf("%s/paper/search?query=%s&limit=5&fields=%s", semanticScholarBaseURL, url.QueryEscape(title), semanticScholarFields)

	var resp ssSearchResponse
	if err := s.getJSON(ctx, searchURL, &resp); err != nil {
		return nil, fmt.Errorf("semantic scholar search error: %w", err)
	}

	if len(resp.Data) == 0 {
		return &VerificationResult{
			Verified: false,
			Source:   "semantic-scholar",
		}, nil
	}

	// Find best match
	var bestMatch *ssPaper
	var bestScore float64

	for i := range resp.Data {
		paper := &resp.Data[i]
		score := ssMatchConfidence(title, authors, year, paper)
		if score > bestScore {
			bestScore = score
			bestMatch = paper
		}
	}

	// Threshold for valid match
	if bestScore < 0.5 {
		return &VerificationResult{
			Verified: false,
			Source:   "semantic-scholar",
		}, nil
	}

	result := ssPaperToPaper(*bestMatch)
	return &VerificationResult{
		Verified:   true,
		Paper:      &result,
		Source:     "semantic-scholar",
		Confidence: bestScore,
	}, nil
}

// Search returns papers matching the query.
// Year filters are applied as native SS year parameter.
// Citation count filtering is not supported natively and is
// handled post-retrieval by the caller.
func (s *SemanticScholarBackend) Search(ctx context.Context, query string, limit int, filters SearchFilters) ([]Paper, error) {
	raw, err := s.searchRaw(ctx, query, limit, filters)
	if err != nil {
		return nil, err
	}
	papers := make([]Paper, 0, len(raw))
	for _, p := range raw {
		papers = append(papers, ssPaperToPaper(p))
	}
	return papers, nil
}

// searchRaw returns raw ssPaper results, preserving PaperID for internal use.
func (s *SemanticScholarBackend) searchRaw(ctx context.Context, query string, limit int, filters SearchFilters) ([]ssPaper, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	searchURL := fmt.Sprintf("%s/paper/search?query=%s&limit=%d&fields=%s", semanticScholarBaseURL, url.QueryEscape(query), limit, semanticScholarFields)

	// SS supports a year parameter: year=YYYY, year=YYYY-, year=-YYYY, year=YYYY-YYYY
	if filters.YearMin > 0 || filters.YearMax > 0 {
		switch {
		case filters.YearMin > 0 && filters.YearMax > 0:
			searchURL += fmt.Sprintf("&year=%d-%d", filters.YearMin, filters.YearMax)
		case filters.YearMin > 0:
			searchURL += fmt.Sprintf("&year=%d-", filters.YearMin)
		case filters.YearMax > 0:
			searchURL += fmt.Sprintf("&year=-%d", filters.YearMax)
		}
	}

	var resp ssSearchResponse
	if err := s.getJSON(ctx, searchURL, &resp); err != nil {
		return nil, fmt.Errorf("semantic scholar search error: %w", err)
	}
	return resp.Data, nil
}

// ResolvePaper looks up a paper by identifier and returns the converted
// Paper plus the native Semantic Scholar 40-char paper ID.
func (s *SemanticScholarBackend) ResolvePaper(ctx context.Context, paperID string) (*Paper, string, error) {
	lookupURL := fmt.Sprintf("%s/paper/%s?fields=%s", semanticScholarBaseURL, url.PathEscape(paperID), semanticScholarFields)

	var raw ssPaper
	if err := s.getJSON(ctx, lookupURL, &raw); err != nil {
		return nil, "", fmt.Errorf("resolve paper: %w", err)
	}
	p := ssPaperToPaper(raw)
	return &p, raw.PaperID, nil
}

// ResolveBySearch searches for a paper by title and resolves the top
// result, returning the converted Paper and native SS paper ID.
func (s *SemanticScholarBackend) ResolveBySearch(ctx context.Context, query string) (*Paper, string, error) {
	raw, err := s.searchRaw(ctx, query, 1, SearchFilters{})
	if err != nil {
		return nil, "", fmt.Errorf("search fallback: %w", err)
	}
	if len(raw) == 0 {
		return nil, "", fmt.Errorf("no paper found for %q", query)
	}
	top := raw[0]
	p := ssPaperToPaper(top)
	return &p, top.PaperID, nil
}

// FetchReferences returns the papers cited BY the given paper.
func (s *SemanticScholarBackend) FetchReferences(ctx context.Context, ssID string, limit int) ([]CitationEdge, error) {
	refURL := fmt.Sprintf("%s/paper/%s/references?fields=%s,isInfluential,intents&limit=%d",
		semanticScholarBaseURL, url.PathEscape(ssID), semanticScholarFields, limit)

	var resp ssReferencesResponse
	if err := s.getJSON(ctx, refURL, &resp); err != nil {
		return nil, fmt.Errorf("fetch references: %w", err)
	}

	edges := make([]CitationEdge, 0, len(resp.Data))
	for _, e := range resp.Data {
		if e.CitedPaper.Title == "" {
			continue // ghost paper
		}
		edges = append(edges, CitationEdge{
			Paper:         ssPaperToPaper(e.CitedPaper),
			IsInfluential: e.IsInfluential,
			Intents:       e.Intents,
		})
	}
	return edges, nil
}

// FetchCitations returns the papers that cite the given paper.
func (s *SemanticScholarBackend) FetchCitations(ctx context.Context, ssID string, limit int) ([]CitationEdge, error) {
	citURL := fmt.Sprintf("%s/paper/%s/citations?fields=%s,isInfluential,intents&limit=%d",
		semanticScholarBaseURL, url.PathEscape(ssID), semanticScholarFields, limit)

	var resp ssCitationsResponse
	if err := s.getJSON(ctx, citURL, &resp); err != nil {
		return nil, fmt.Errorf("fetch citations: %w", err)
	}

	edges := make([]CitationEdge, 0, len(resp.Data))
	for _, e := range resp.Data {
		if e.CitingPaper.Title == "" {
			continue // ghost paper
		}
		edges = append(edges, CitationEdge{
			Paper:         ssPaperToPaper(e.CitingPaper),
			IsInfluential: e.IsInfluential,
			Intents:       e.Intents,
		})
	}
	return edges, nil
}

// getJSON makes a GET request with optional API key header.
func (s *SemanticScholarBackend) getJSON(ctx context.Context, reqURL string, result any) error {
	headers := make(map[string]string)
	if s.apiKey != "" {
		headers["x-api-key"] = s.apiKey
	}

	body, err := s.client.Do(ctx, Request{
		Method:  http.MethodGet,
		URL:     reqURL,
		Backend: "semantic-scholar",
		Headers: headers,
	})
	if err != nil {
		return err
	}
	return json.Unmarshal(body, result)
}

// Semantic Scholar API response types

type ssSearchResponse struct {
	Total int       `json:"total"`
	Data  []ssPaper `json:"data"`
}

type ssPaper struct {
	PaperID       string `json:"paperId"`
	Title         string `json:"title"`
	Abstract      string `json:"abstract"`
	Year          int    `json:"year"`
	Venue         string `json:"venue"`
	CitationCount int    `json:"citationCount"`
	URL           string `json:"url"`
	ExternalIDs   struct {
		DOI string `json:"DOI"`
	} `json:"externalIds"`
	Authors []struct {
		Name string `json:"name"`
	} `json:"authors"`
	OpenAccessPDF *struct {
		URL string `json:"url"`
	} `json:"openAccessPdf"`
}

// Semantic Scholar Graph API edge types

type ssReferencesResponse struct {
	Data []ssReferenceEdge `json:"data"`
}

type ssReferenceEdge struct {
	CitedPaper    ssPaper  `json:"citedPaper"`
	IsInfluential bool     `json:"isInfluential"`
	Intents       []string `json:"intents"`
}

type ssCitationsResponse struct {
	Data []ssCitationEdge `json:"data"`
}

type ssCitationEdge struct {
	CitingPaper   ssPaper  `json:"citingPaper"`
	IsInfluential bool     `json:"isInfluential"`
	Intents       []string `json:"intents"`
}

// ssPaperToPaper converts a Semantic Scholar paper to a Paper.
func ssPaperToPaper(p ssPaper) Paper {
	authors := make([]string, 0, len(p.Authors))
	for _, a := range p.Authors {
		if a.Name != "" {
			authors = append(authors, a.Name)
		}
	}

	paperURL := p.URL
	// If there's an open access PDF, prefer that
	if p.OpenAccessPDF != nil && p.OpenAccessPDF.URL != "" {
		paperURL = p.OpenAccessPDF.URL
	}

	return Paper{
		Title:     p.Title,
		Authors:   authors,
		Year:      p.Year,
		DOI:       p.ExternalIDs.DOI,
		Abstract:  p.Abstract,
		URL:       paperURL,
		Source:    "semantic-scholar",
		Journal:   p.Venue,
		Citations: p.CitationCount,
	}
}

// ssMatchConfidence computes a weighted confidence score for a Semantic Scholar paper match.
// Weights: title (60%), year (20%), authors (20%).
// Post-processing: year gap penalty and first-author gate.
func ssMatchConfidence(queryTitle string, queryAuthors []string, queryYear int, paper *ssPaper) float64 {
	score := 0.0

	// Title similarity (60%)
	if paper.Title != "" {
		titleSim := jaccardSimilarity(queryTitle, paper.Title)
		score += 0.6 * titleSim
	}

	// Year match (20%)
	if queryYear > 0 && paper.Year > 0 {
		if queryYear == paper.Year {
			score += 0.2
		} else if abs(queryYear-paper.Year) <= 1 {
			score += 0.1
		}
	}

	// Author match (20%)
	if len(queryAuthors) > 0 && len(paper.Authors) > 0 {
		authorScore := ssAuthorSimilarity(queryAuthors, paper.Authors)
		score += 0.2 * authorScore
	}

	// Year gap penalty
	score *= yearPenalty(queryYear, paper.Year)

	// First-author gate: extract family names from SS author display names
	workFamilies := make([]string, 0, len(paper.Authors))
	for _, a := range paper.Authors {
		if a.Name != "" {
			workFamilies = append(workFamilies, extractFamilyName(a.Name))
		}
	}
	if !firstAuthorMatchesAny(queryAuthors, workFamilies) {
		if score > 0.4 {
			score = 0.4
		}
	}

	return score
}

// ssAuthorSimilarity computes how well query authors match paper authors.
func ssAuthorSimilarity(queryAuthors []string, paperAuthors []struct {
	Name string `json:"name"`
}) float64 {
	if len(queryAuthors) == 0 || len(paperAuthors) == 0 {
		return 0.0
	}

	matches := 0
	for _, qa := range queryAuthors {
		qaLower := tokenize(qa)
		for _, pa := range paperAuthors {
			paLower := tokenize(pa.Name)
			// Check if any token from query author appears in paper author
			for _, qt := range qaLower {
				for _, pt := range paLower {
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
