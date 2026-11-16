package backends

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	openLibraryBaseURL = "https://openlibrary.org"
	openLibraryRPS     = 1.0 // 1 req/s without User-Agent identification
)

// OpenLibraryBackend implements CitationBackend for book verification
// via the Open Library Search API. Books are poorly indexed in academic
// databases (Crossref, Semantic Scholar, OpenAlex); this backend
// provides accurate title, author, and first-publication-year metadata
// for books specifically.
type OpenLibraryBackend struct {
	client *Client
}

// NewOpenLibrary creates a new OpenLibrary backend.
func NewOpenLibrary(client *Client) *OpenLibraryBackend {
	ensureRateLimit("openlibrary", openLibraryRPS, 1)
	return &OpenLibraryBackend{client: client}
}

// Name returns the backend identifier.
func (o *OpenLibraryBackend) Name() string {
	return "openlibrary"
}

// HealthCheck tests if the OpenLibrary API is reachable.
func (o *OpenLibraryBackend) HealthCheck(ctx context.Context) HealthStatus {
	u := openLibraryBaseURL + "/search.json?q=test&limit=1&fields=title"
	var resp openLibrarySearchResponse
	if err := o.client.GetJSON(ctx, u, "openlibrary", &resp); err != nil {
		return HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("OpenLibrary API error: %v", err),
		}
	}
	return HealthStatus{
		Healthy: true,
		Message: "OpenLibrary API reachable",
	}
}

// VerifyDOI returns not-found; OpenLibrary doesn't index by DOI.
func (o *OpenLibraryBackend) VerifyDOI(ctx context.Context, doi string) (*VerificationResult, error) {
	return &VerificationResult{
		Verified: false,
		Source:   "openlibrary",
	}, nil
}

// VerifyByTitle searches for a book by title/authors/year and scores
// the match. Uses the same 60/20/20 weighting scheme as other backends
// (title similarity, year match, author match) plus year-gap penalty
// and first-author gate.
func (o *OpenLibraryBackend) VerifyByTitle(ctx context.Context, title string, authors []string, year int) (*VerificationResult, error) {
	// Build search URL with title and optional first author.
	// Strip subtitles (after colon) — OL's title search returns
	// zero results for long "Title: Full Subtitle" strings but
	// matches the short title reliably.
	searchTitle := title
	if idx := strings.Index(searchTitle, ":"); idx > 0 {
		searchTitle = strings.TrimSpace(searchTitle[:idx])
	}
	params := url.Values{}
	params.Set("title", searchTitle)
	if len(authors) > 0 {
		// Use only the first author's family name — OpenLibrary's
		// author search works best with a single surname.
		family := extractFamilyName(authors[0])
		if family != "" {
			params.Set("author", family)
		}
	}
	params.Set("limit", "5")
	params.Set("fields", "title,author_name,first_publish_year,isbn,publisher,edition_count,key")

	searchURL := openLibraryBaseURL + "/search.json?" + params.Encode()

	var resp openLibrarySearchResponse
	if err := o.client.GetJSON(ctx, searchURL, "openlibrary", &resp); err != nil {
		return nil, fmt.Errorf("openlibrary search error: %w", err)
	}

	if len(resp.Docs) == 0 {
		return &VerificationResult{
			Verified: false,
			Source:   "openlibrary",
		}, nil
	}

	// Score each result and keep the best.
	// Try both the full title and the stripped (no subtitle) title,
	// since OL often omits subtitles. Take the higher score.
	var bestScore float64
	var bestDoc *openLibraryDoc
	for i := range resp.Docs {
		score := olMatchConfidence(title, authors, year, &resp.Docs[i])
		if searchTitle != title {
			if s := olMatchConfidence(searchTitle, authors, year, &resp.Docs[i]); s > score {
				score = s
			}
		}
		if score > bestScore {
			bestScore = score
			bestDoc = &resp.Docs[i]
		}
	}

	if bestDoc == nil || bestScore < 0.5 {
		return &VerificationResult{
			Verified: false,
			Source:   "openlibrary",
		}, nil
	}

	paper := olDocToPaper(bestDoc)
	return &VerificationResult{
		Verified:   true,
		Paper:      &paper,
		Source:     "openlibrary",
		Confidence: bestScore,
	}, nil
}

// Search finds books by keyword: OpenLibrary's free search over its
// catalogue of works, for the catalogue search's books. A work has no
// abstract or citation count, so filters.MinCitations and Domain cannot
// apply here; the year filters are applied to the first publication
// year.
func (o *OpenLibraryBackend) Search(ctx context.Context, query string, limit int, filters SearchFilters) ([]Paper, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", strconv.Itoa(limit*2))
	params.Set("fields", "title,author_name,first_publish_year,isbn,publisher,edition_count,key")
	searchURL := openLibraryBaseURL + "/search.json?" + params.Encode()

	var resp openLibrarySearchResponse
	if err := o.client.GetJSON(ctx, searchURL, "openlibrary", &resp); err != nil {
		return nil, fmt.Errorf("openlibrary search error: %w", err)
	}
	return booksFromOpenLibrarySearch(resp, limit, filters), nil
}

// booksFromOpenLibrarySearch turns a search response into at most
// limit books within the year filters.
func booksFromOpenLibrarySearch(resp openLibrarySearchResponse, limit int, filters SearchFilters) []Paper {
	papers := make([]Paper, 0, len(resp.Docs))
	for i := range resp.Docs {
		book := olDocToPaper(&resp.Docs[i])
		if filters.YearMin > 0 && book.Year > 0 && book.Year < filters.YearMin {
			continue
		}
		if filters.YearMax > 0 && book.Year > filters.YearMax {
			continue
		}
		papers = append(papers, book)
		if len(papers) == limit {
			break
		}
	}
	return papers
}

// olMatchConfidence scores an OpenLibrary doc against a query using
// the same 60/20/20 scheme as other backends.
func olMatchConfidence(queryTitle string, queryAuthors []string, queryYear int, doc *openLibraryDoc) float64 {
	score := 0.0

	// Title similarity (60%)
	if doc.Title != "" {
		titleSim := jaccardSimilarity(queryTitle, doc.Title)
		score += 0.6 * titleSim
	}

	// Year match (20%) — uses first_publish_year (work-level, not per-edition)
	if queryYear > 0 && doc.FirstPublishYear > 0 {
		if queryYear == doc.FirstPublishYear {
			score += 0.2
		} else if abs(queryYear-doc.FirstPublishYear) <= 1 {
			score += 0.1
		}
	}

	// Author match (20%)
	if len(queryAuthors) > 0 && len(doc.AuthorName) > 0 {
		authorScore := olAuthorSimilarity(queryAuthors, doc.AuthorName)
		score += 0.2 * authorScore
	}

	// Year gap penalty
	score *= yearPenalty(queryYear, doc.FirstPublishYear)

	// First-author gate
	workFamilies := make([]string, 0, len(doc.AuthorName))
	for _, name := range doc.AuthorName {
		if name != "" {
			workFamilies = append(workFamilies, extractFamilyName(name))
		}
	}
	if !firstAuthorMatchesAny(queryAuthors, workFamilies) {
		if score > 0.4 {
			score = 0.4
		}
	}

	return score
}

// olAuthorSimilarity computes how well query authors match OpenLibrary
// author names. Uses token-level matching like SS/OA backends.
func olAuthorSimilarity(queryAuthors []string, bookAuthors []string) float64 {
	if len(queryAuthors) == 0 || len(bookAuthors) == 0 {
		return 0.0
	}

	matches := 0
	for _, qa := range queryAuthors {
		qaTokens := tokenize(qa)
		for _, ba := range bookAuthors {
			baTokens := tokenize(ba)
			for _, qt := range qaTokens {
				for _, bt := range baTokens {
					if qt == bt && len(qt) > 1 {
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

// olDocToPaper converts an OpenLibrary document to a Paper.
func olDocToPaper(doc *openLibraryDoc) Paper {
	var bookURL string
	if doc.Key != "" {
		bookURL = openLibraryBaseURL + doc.Key
	}

	return Paper{
		Kind:      RecordKindBook,
		Title:     doc.Title,
		Authors:   doc.AuthorName,
		Year:      doc.FirstPublishYear,
		URL:       bookURL,
		ISBN:      olISBN(doc),
		Publisher: olPublisher(doc),
		Source:    "openlibrary",
	}
}

// openLibrarySearchResponse represents the OpenLibrary search API response.
type openLibrarySearchResponse struct {
	NumFound int              `json:"numFound"`
	Docs     []openLibraryDoc `json:"docs"`
}

// openLibraryDoc represents a single document from OpenLibrary search.
type openLibraryDoc struct {
	Title            string   `json:"title"`
	AuthorName       []string `json:"author_name"`
	FirstPublishYear int      `json:"first_publish_year"`
	ISBN             []string `json:"isbn"`
	Publisher        []string `json:"publisher"`
	EditionCount     int      `json:"edition_count"`
	Key              string   `json:"key"` // e.g. "/works/OL2627285W"
}

// olPublisher returns the first publisher name, if any.
func olPublisher(doc *openLibraryDoc) string {
	if len(doc.Publisher) > 0 {
		return doc.Publisher[0]
	}
	return ""
}

// olISBN returns the first ISBN, preferring ISBN-13.
func olISBN(doc *openLibraryDoc) string {
	for _, isbn := range doc.ISBN {
		isbn = strings.TrimSpace(isbn)
		if len(isbn) == 13 {
			return isbn
		}
	}
	if len(doc.ISBN) > 0 {
		return strings.TrimSpace(doc.ISBN[0])
	}
	return ""
}
