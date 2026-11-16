package backends

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
)

const (
	wikipediaBaseURL = "https://en.wikipedia.org/w/api.php"
	wikipediaRPS     = 5.0 // 5 req/s - Wikipedia asks for "reasonable" use
)

// Wikipedia implements WikipediaBackend using the MediaWiki API.
type Wikipedia struct {
	client  *Client
	baseURL string

	// LastRawCount is set by Search to the number of pages returned
	// by the API before relevance filtering. Used for diagnostics.
	LastRawCount int
}

// NewWikipedia creates a new Wikipedia backend.
func NewWikipedia(client *Client) *Wikipedia {
	ensureRateLimit("wikipedia", wikipediaRPS, 1)
	return &Wikipedia{
		client:  client,
		baseURL: wikipediaBaseURL,
	}
}

// Name returns the backend identifier.
func (w *Wikipedia) Name() string {
	return "wikipedia"
}

// HealthCheck tests if the Wikipedia API is reachable.
func (w *Wikipedia) HealthCheck(ctx context.Context) HealthStatus {
	// Make a simple search request to verify the API is working
	_, err := w.Search(ctx, "test", 1)
	if err != nil {
		return HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("Wikipedia API error: %v", err),
		}
	}
	return HealthStatus{
		Healthy: true,
		Message: "Wikipedia API reachable",
	}
}

// fetchPages sends a single query to the Wikipedia API and returns
// pages sorted by search ranking. This is the raw API call without
// relevance filtering.
func (w *Wikipedia) fetchPages(ctx context.Context, query string, limit int) ([]wpPage, error) {
	searchURL := fmt.Sprintf("%s?action=query&format=json&generator=search&gsrsearch=%s&gsrlimit=%d&prop=extracts|info&exintro=1&explaintext=1&inprop=url",
		w.baseURL,
		url.QueryEscape(query),
		limit,
	)

	var resp wpQueryResponse
	if err := w.client.GetJSON(ctx, searchURL, "wikipedia", &resp); err != nil {
		return nil, err
	}

	pages := make([]wpPage, 0, len(resp.Query.Pages))
	for _, page := range resp.Query.Pages {
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool {
		return pages[i].Index < pages[j].Index
	})

	return pages, nil
}

// bigramQueries returns CirrusSearch queries with individual bigrams
// quoted as phrases. Each query quotes one bigram and leaves the
// remaining terms unquoted, so CirrusSearch boosts phrase matches
// without over-constraining (quoting all bigrams ANDs them).
//
// For terms ["second", "shift", "emotional", "labour", "gender"]:
//
//	"second shift" emotional labour gender
//	"emotional labour" second shift gender
//	"emotional labor" second shift gender   (UK/US variant)
//
// Returns nil for queries with fewer than 3 significant terms.
// Generates at most 2 bigrams to limit API calls.
func bigramQueries(terms []string) []string {
	if len(terms) < 3 {
		return nil
	}

	var queries []string
	i := 0
	for i+1 < len(terms) && len(queries) < 4 {
		// Remaining terms (everything except the bigram pair).
		var rest []string
		for j, t := range terms {
			if j != i && j != i+1 {
				rest = append(rest, t)
			}
		}
		restStr := strings.Join(rest, " ")

		// Query with this bigram quoted.
		queries = append(queries,
			`"`+terms[i]+" "+terms[i+1]+`" `+restStr)

		// Variant bigram if either term has a UK/US alternate.
		t0, t1 := terms[i], terms[i+1]
		changed := false
		if v := SpellingVariant(t0); v != "" {
			t0 = v
			changed = true
		}
		if v := SpellingVariant(t1); v != "" {
			t1 = v
			changed = true
		}
		if changed {
			queries = append(queries,
				`"`+t0+" "+t1+`" `+restStr)
		}

		i += 2
	}

	return queries
}

// Search returns Wikipedia articles matching the query. For queries
// with 3+ significant terms, it also fires a phrase-quoted variant
// (and a spelling-variant phrase query if applicable) to boost
// multi-word concept matches via CirrusSearch.
func (w *Wikipedia) Search(ctx context.Context, query string, limit int) ([]WikipediaArticle, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > 50 {
		limit = 50 // Wikipedia API max for generator
	}

	apiLimit := limit + 5
	terms := ExtractQueryTerms(query)

	// Only generate bigram queries when the user hasn't already
	// quoted phrases---manual quoting means they know what they want.
	var extras []string
	if !strings.Contains(query, `"`) {
		extras = bigramQueries(terms)
	}

	type fetchResult struct {
		pages   []wpPage
		err     error
		isExtra bool
	}

	numQueries := 1 + len(extras)
	ch := make(chan fetchResult, numQueries)
	var wg sync.WaitGroup

	fire := func(q string, extra bool) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pages, err := w.fetchPages(ctx, q, apiLimit)
			ch <- fetchResult{pages, err, extra}
		}()
	}

	fire(query, false)
	for _, eq := range extras {
		fire(eq, true)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	// Collect: bigram-query results first, then original.
	var extraPages, origPages []wpPage
	var firstErr error
	anySuccess := false
	for r := range ch {
		if r.err == nil {
			anySuccess = true
			if r.isExtra {
				extraPages = append(extraPages, r.pages...)
			} else {
				origPages = r.pages
			}
		} else if firstErr == nil {
			firstErr = r.err
		}
	}

	if !anySuccess && firstErr != nil {
		return nil, fmt.Errorf("wikipedia API error: %w", firstErr)
	}

	// Merge: bigram results first (more specific), then original.
	seen := make(map[int]bool)
	var merged []wpPage
	for _, p := range extraPages {
		if !seen[p.PageID] {
			seen[p.PageID] = true
			merged = append(merged, p)
		}
	}
	for _, p := range origPages {
		if !seen[p.PageID] {
			seen[p.PageID] = true
			merged = append(merged, p)
		}
	}

	w.LastRawCount = len(merged)

	// Filter and collect articles.
	articles := make([]WikipediaArticle, 0, limit)
	for _, page := range merged {
		if !w.isRelevant(page, query) {
			continue
		}

		article := WikipediaArticle{
			Title:   page.Title,
			Extract: page.Extract,
			URL:     page.FullURL,
		}
		if article.URL == "" {
			article.URL = "https://en.wikipedia.org/wiki/" + url.PathEscape(strings.ReplaceAll(page.Title, " ", "_"))
		}

		articles = append(articles, article)
		if len(articles) >= limit {
			break
		}
	}

	return articles, nil
}

// minTermMatches returns the minimum number of query terms that must
// appear in a result for it to be considered relevant.
//   - 0–1 terms: all must match
//   - 2+  terms: at least 2 or ceil(40%), whichever is larger
func minTermMatches(n int) int {
	if n <= 1 {
		return n
	}
	proportional := (n*2 + 4) / 5 // ceiling of n * 0.4
	if proportional < 2 {
		return 2
	}
	return proportional
}

// isRelevant checks if a page should be included in results.
func (w *Wikipedia) isRelevant(page wpPage, query string) bool {
	// Skip disambiguation pages
	if strings.Contains(page.Title, "(disambiguation)") {
		return false
	}

	// Skip pages with empty or very short extracts
	if len(strings.TrimSpace(page.Extract)) < 50 {
		return false
	}

	// Require proportional query-term overlap.
	// Title matches lower the threshold (the page is about a query term).
	// UK/US spelling variants are checked so e.g. "labour" matches "labor".
	terms := ExtractQueryTerms(query)
	if len(terms) > 0 {
		titleLower := strings.ToLower(page.Title)
		extractLower := strings.ToLower(page.Extract)

		matches := 0
		titleMatch := false
		for _, term := range terms {
			variant := SpellingVariant(term)
			inTitle := strings.Contains(titleLower, term) ||
				(variant != "" && strings.Contains(titleLower, variant))
			inExtract := strings.Contains(extractLower, term) ||
				(variant != "" && strings.Contains(extractLower, variant))

			if inTitle {
				matches++
				titleMatch = true
			} else if inExtract {
				matches++
			}
		}
		threshold := minTermMatches(len(terms))
		if titleMatch && threshold > 1 {
			threshold--
		}
		if matches < threshold {
			return false
		}
	}

	return true
}

// Wikipedia API response types

type wpQueryResponse struct {
	Query struct {
		Pages map[string]wpPage `json:"pages"`
	} `json:"query"`
}

type wpPage struct {
	PageID  int    `json:"pageid"`
	Index   int    `json:"index"` // Search ranking position from generator=search
	Title   string `json:"title"`
	Extract string `json:"extract"`
	FullURL string `json:"fullurl"`
}
