package backends

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Exa is a neural web search API that returns page text alongside
// the hits, so a claim can be grounded in the page that supports it
// rather than a bare link. It is opt-in and metered: every Search
// call spends credits, and the caller-facing tool description must
// say so.
//
// Verified 2026-09-17 against:
//   - https://exa.ai/docs/reference/search (POST /search reference;
//     the raw OpenAPI at the .md suffix was read directly)
//   - https://exa.ai/docs/reference/getting-started (auth quickstart)
//   - https://exa.ai/docs/reference/rate-limits (QPS per plan)
//   - https://exa.ai/pricing (per-request cost)
//   - https://github.com/exa-labs/openapi-spec (older spec; the only
//     source still documenting the per-result `score` field)
//
// Cost, in USD, from the pricing page: a standard search (`instant`,
// `fast` or `auto`) is $7 per 1,000 requests with up to 10 results,
// so $0.007 per call, and each result above 10 adds $1 per 1,000
// ($0.001 each). Deep modes are $12 per 1,000 (`deep-lite`, `deep`)
// and $15 per 1,000 (`deep-reasoning`). The pricing page lists
// "webpage text and highlights" under the search price and the
// reference example shows a 10-result search with text at
// costDollars.total 0.007, so text and highlights requested inside
// /search appear to be bundled; standalone /contents is $1 per 1,000
// pages per content type. Each response carries costDollars.total,
// captured in LastCostDollars, and that figure is the one to trust
// over this comment. New accounts get $20 of credit and the free
// tier adds $10 a month.
//
// Rate limit, from the rate-limits page: 10 QPS on /search for
// `instant`/`fast`/`auto` and 5 QPS for the deep modes, on both Free
// and Pay-as-you-go. One limiter has to be safe for whichever
// SearchType the caller picks, so exaRPS is the deep-mode ceiling.
// Exceeding it returns 429 with tag RATE_LIMIT_EXCEEDED; running out
// of credit returns 402 with tag NO_MORE_CREDITS.

const (
	exaBaseURL = "https://api.exa.ai"
	exaRPS     = 5.0 // documented deep-mode ceiling; standard modes allow 10

	exaDefaultResults = 10 // API default; also the largest count inside the base price
	exaMaxResults     = 20 // API allows 100, but every result above 10 is billed separately

	exaDefaultSearchType = "auto"

	// Per-result content budgets. The API caps both at 10,000
	// characters. Highlights used to be requested as numSentences and
	// highlightsPerUrl; both are deprecated (the latter is ignored
	// outright), and maxCharacters is the documented replacement.
	exaDefaultTextCharacters      = 2000
	exaDefaultHighlightCharacters = 600 // roughly two or three sentences
	exaMaxContentCharacters       = 10000
)

// exaSearchTypes is the documented `type` enum. Validated locally so
// a typo fails before it reaches the metered endpoint.
var exaSearchTypes = map[string]bool{
	"instant":        true,
	"fast":           true,
	"auto":           true,
	"deep-lite":      true,
	"deep":           true,
	"deep-reasoning": true,
}

// WebResult is a single web page returned by Exa. It is not an
// academic Paper: there is no DOI or citation count, and Text is page
// content rather than an abstract.
type WebResult struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Author        string   `json:"author,omitempty"`
	PublishedDate string   `json:"published_date,omitempty"` // ISO 8601, estimated by Exa from the page
	Text          string   `json:"text,omitempty"`           // page text, truncated to the requested budget
	Summary       string   `json:"summary,omitempty"`        // only when a summary was requested
	Highlights    []string `json:"highlights,omitempty"`     // query-relevant snippets chosen by Exa
	Score         float64  `json:"score,omitempty"`          // query similarity 0-1; absent from the current reference
}

// ExaSearchOptions holds the optional constraints for an Exa search.
// Zero values mean "use the default".
type ExaSearchOptions struct {
	IncludeDomains     []string // hostnames, path prefixes or wildcard subdomains, e.g. "arxiv.org", "example.com/docs", "*.example.com"
	StartPublishedDate string   // ISO 8601 datetime, e.g. "2024-01-01T00:00:00.000Z"; only pages published after this
	MaxTextCharacters  int      // page text budget per result; default exaDefaultTextCharacters, API maximum 10000
	SearchType         string   // instant | fast | auto | deep-lite | deep | deep-reasoning; default auto
}

// ExaBackend searches the web through Exa's neural search API.
type ExaBackend struct {
	client  *Client
	apiKey  string
	baseURL string

	// LastCostDollars is set by Search and HealthCheck to the
	// costDollars.total the API reported for the most recent call, or
	// zero when the response carried none. Diagnostic only: Exa
	// describes it as an estimate, not an invoice record.
	LastCostDollars float64
}

// NewExa creates a new Exa backend. An empty apiKey is allowed so the
// backend can be registered without credentials; Search then fails
// with a clear message and HealthCheck reports it as available but
// unconfigured.
func NewExa(client *Client, apiKey string) *ExaBackend {
	ensureRateLimit("exa", exaRPS, 1)
	return &ExaBackend{
		client:  client,
		apiKey:  apiKey,
		baseURL: exaBaseURL,
	}
}

// Name returns the backend identifier.
func (e *ExaBackend) Name() string {
	return "exa"
}

// HealthCheck tests if the Exa API is reachable. Without a key it
// reports healthy-but-unconfigured rather than failing, because the
// backend is opt-in. With a key it makes the cheapest real call the
// API offers: one result, no contents, which bills a single standard
// search ($0.007 at the verified price) and nothing else.
func (e *ExaBackend) HealthCheck(ctx context.Context) HealthStatus {
	if e.apiKey == "" {
		return HealthStatus{
			Healthy: true,
			Message: "available (no key configured—set EXA_API_KEY)",
		}
	}

	resp, err := e.postSearch(ctx, exaSearchRequest{
		Query:      "test",
		Type:       "fast",
		NumResults: 1,
	})
	if err != nil {
		return HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("Exa API error: %v", err),
		}
	}

	message := "Exa API reachable"
	if resp.CostDollars != nil {
		message = fmt.Sprintf("Exa API reachable (health check cost $%.4f)", resp.CostDollars.Total)
	}
	return HealthStatus{Healthy: true, Message: message}
}

// Search returns web pages matching the query, with page text and
// highlights for each. Every call is billed; see the file comment for
// the verified prices and LastCostDollars for what this call cost.
func (e *ExaBackend) Search(ctx context.Context, query string, limit int, opts ExaSearchOptions) ([]WebResult, error) {
	if e.apiKey == "" {
		return nil, fmt.Errorf("exa: EXA_API_KEY not configured")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("exa: empty query")
	}

	searchType := opts.SearchType
	if searchType == "" {
		searchType = exaDefaultSearchType
	}
	if !exaSearchTypes[searchType] {
		return nil, fmt.Errorf("exa: unknown search type %q (want instant, fast, auto, deep-lite, deep or deep-reasoning)", searchType)
	}

	limit = clampExaLimit(limit)
	textCharacters := clampExaContentCharacters(opts.MaxTextCharacters, exaDefaultTextCharacters)

	req := exaSearchRequest{
		Query:              query,
		Type:               searchType,
		NumResults:         limit,
		IncludeDomains:     opts.IncludeDomains,
		StartPublishedDate: opts.StartPublishedDate,
		Contents: &exaContentsRequest{
			Text:       &exaTextRequest{MaxCharacters: textCharacters},
			Highlights: &exaHighlightsRequest{MaxCharacters: exaDefaultHighlightCharacters},
		},
	}

	resp, err := e.postSearch(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("exa search error: %w", err)
	}

	results := make([]WebResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		if len(results) >= limit {
			break
		}
		results = append(results, exaResultToWebResult(r))
	}
	return results, nil
}

// postSearch sends one POST /search, records the reported cost, and
// decodes the response. Shared by Search and HealthCheck so both go
// through the same rate limiter and error handling.
func (e *ExaBackend) postSearch(ctx context.Context, req exaSearchRequest) (*exaSearchResponse, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("exa: encoding request: %w", err)
	}

	body, err := e.client.Do(ctx, Request{
		Method:  http.MethodPost,
		URL:     e.baseURL + "/search",
		Backend: "exa",
		Headers: map[string]string{
			"x-api-key": e.apiKey,
		},
		Body: payload,
	})
	if err != nil {
		return nil, wrapExaHTTPError(err)
	}

	var resp exaSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("exa: failed to parse response: %w", err)
	}

	e.LastCostDollars = 0
	if resp.CostDollars != nil {
		e.LastCostDollars = resp.CostDollars.Total
	}
	return &resp, nil
}

// clampExaLimit keeps numResults within [1, exaMaxResults]. A zero or
// negative limit means "unspecified" and takes the default, matching
// the other backends, rather than collapsing to the minimum.
func clampExaLimit(limit int) int {
	if limit <= 0 {
		return exaDefaultResults
	}
	if limit > exaMaxResults {
		return exaMaxResults
	}
	return limit
}

// clampExaContentCharacters applies the default for an unset budget
// and the API's 10,000-character ceiling for an oversized one.
func clampExaContentCharacters(requested, fallback int) int {
	if requested <= 0 {
		return fallback
	}
	if requested > exaMaxContentCharacters {
		return exaMaxContentCharacters
	}
	return requested
}

func exaResultToWebResult(r exaResult) WebResult {
	return WebResult{
		Title:         r.Title,
		URL:           r.URL,
		Author:        r.Author,
		PublishedDate: r.PublishedDate,
		Text:          r.Text,
		Summary:       r.Summary,
		Highlights:    r.Highlights,
		Score:         r.Score,
	}
}

// ExaAPIError carries the message and tag Exa puts in a non-2xx body,
// e.g. "Invalid API key" / INVALID_API_KEY, or NO_MORE_CREDITS. It
// unwraps to the underlying *HTTPError so IsRateLimited and IsNotFound
// keep working through it.
type ExaAPIError struct {
	StatusCode int
	Message    string
	Tag        string
	cause      *HTTPError
}

func (e *ExaAPIError) Error() string {
	if e.Tag != "" {
		return fmt.Sprintf("exa: HTTP %d %s: %s", e.StatusCode, e.Tag, e.Message)
	}
	return fmt.Sprintf("exa: HTTP %d: %s", e.StatusCode, e.Message)
}

func (e *ExaAPIError) Unwrap() error {
	return e.cause
}

// wrapExaHTTPError turns a Client.Do error into an ExaAPIError when
// the body follows Exa's {requestId, error, tag} envelope, so the
// caller sees the API's own explanation instead of raw JSON. Any
// other error (transport, cooldown, non-JSON body) passes through.
func wrapExaHTTPError(err error) error {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		return err
	}

	var envelope exaErrorResponse
	if json.Unmarshal([]byte(httpErr.Body), &envelope) != nil || envelope.Error == "" {
		return &ExaAPIError{
			StatusCode: httpErr.StatusCode,
			Message:    strings.TrimSpace(httpErr.Body),
			cause:      httpErr,
		}
	}
	return &ExaAPIError{
		StatusCode: httpErr.StatusCode,
		Message:    envelope.Error,
		Tag:        envelope.Tag,
		cause:      httpErr,
	}
}

// Exa API request types. Field names are the API's camelCase, so no
// snake_case tags here.

type exaSearchRequest struct {
	Query              string              `json:"query"`
	Type               string              `json:"type,omitempty"`
	NumResults         int                 `json:"numResults"`
	IncludeDomains     []string            `json:"includeDomains,omitempty"`
	StartPublishedDate string              `json:"startPublishedDate,omitempty"`
	Contents           *exaContentsRequest `json:"contents,omitempty"`
}

type exaContentsRequest struct {
	Text       *exaTextRequest       `json:"text,omitempty"`
	Highlights *exaHighlightsRequest `json:"highlights,omitempty"`
}

type exaTextRequest struct {
	MaxCharacters int `json:"maxCharacters,omitempty"`
}

type exaHighlightsRequest struct {
	MaxCharacters int `json:"maxCharacters,omitempty"`
}

// Exa API response types

type exaSearchResponse struct {
	RequestID   string          `json:"requestId"`
	Results     []exaResult     `json:"results"`
	CostDollars *exaCostDollars `json:"costDollars"`
}

type exaResult struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	PublishedDate string   `json:"publishedDate"` // may be null
	Author        string   `json:"author"`        // may be null
	Text          string   `json:"text"`
	Highlights    []string `json:"highlights"`
	Summary       string   `json:"summary"`
	Score         float64  `json:"score"` // documented only in the older GitHub spec; zero when absent
}

// exaCostDollars mirrors CostDollarsOutput. The API also breaks the
// figure down into search.{neural,keyword}, summary and
// contents.{text,highlights,summary}; only the total is kept.
type exaCostDollars struct {
	Total float64 `json:"total"`
}

type exaErrorResponse struct {
	RequestID string `json:"requestId"`
	Error     string `json:"error"`
	Tag       string `json:"tag"`
}
