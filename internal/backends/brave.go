package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Brave Search answers web_search's keyword mode: a conventional web
// index behind a documented REST API with key-header auth. Ported from
// an earlier agent of the author's, which chose it over the
// alternatives (2026-05-24).
//
// Cost: the author's key sits on a grandfathered free plan. A key made
// today may be on Brave's metered plans instead; check the dashboard
// before assuming any search is free.
//
// Rate: the free plan allows one query per second, which the limiter
// holds to whatever the plan.

const (
	braveBaseURL = "https://api.search.brave.com"
	braveRPS     = 1.0

	braveDefaultResults = 10
	braveMaxResults     = 20 // the API's own ceiling for count
)

// BraveBackend searches the web through the Brave Search API.
type BraveBackend struct {
	client  *Client
	apiKey  string
	baseURL string
}

// NewBrave creates a Brave backend. An empty apiKey is allowed; Search
// then fails with a message naming the variable to set.
func NewBrave(client *Client, apiKey string) *BraveBackend {
	ensureRateLimit("brave", braveRPS, 1)
	return &BraveBackend{client: client, apiKey: apiKey, baseURL: braveBaseURL}
}

// Name returns the backend identifier.
func (b *BraveBackend) Name() string {
	return "brave"
}

// Search returns up to limit web results for query.
func (b *BraveBackend) Search(ctx context.Context, query string, limit int) ([]WebResult, error) {
	if b.apiKey == "" {
		return nil, fmt.Errorf("brave: BRAVE_API_KEY not configured")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("brave: empty query")
	}
	if limit <= 0 {
		limit = braveDefaultResults
	}
	if limit > braveMaxResults {
		limit = braveMaxResults
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("count", strconv.Itoa(limit))
	body, err := b.client.Do(ctx, Request{
		Method:  http.MethodGet,
		URL:     b.baseURL + "/res/v1/web/search?" + params.Encode(),
		Backend: "brave",
		Headers: map[string]string{
			"X-Subscription-Token": b.apiKey,
			"Accept":               "application/json",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("brave search error: %w", err)
	}

	var response braveSearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("brave: response is not the expected JSON: %w", err)
	}
	results := make([]WebResult, 0, len(response.Web.Results))
	for _, result := range response.Web.Results {
		if len(results) >= limit {
			break
		}
		webResult := WebResult{
			Title:         textFromBraveMarkup(result.Title),
			URL:           result.URL,
			PublishedDate: result.PageAge,
		}
		if snippet := textFromBraveMarkup(result.Description); snippet != "" {
			webResult.Highlights = append(webResult.Highlights, snippet)
		}
		for _, extra := range result.ExtraSnippets {
			if snippet := textFromBraveMarkup(extra); snippet != "" {
				webResult.Highlights = append(webResult.Highlights, snippet)
			}
		}
		results = append(results, webResult)
	}
	return results, nil
}

// HealthCheck reports whether a key is configured. It makes no call:
// every Brave query counts against the plan.
func (b *BraveBackend) HealthCheck(ctx context.Context) HealthStatus {
	if b.apiKey == "" {
		return HealthStatus{Healthy: true, Message: "available (no key configured—set BRAVE_API_KEY)"}
	}
	return HealthStatus{Healthy: true, Message: "key configured (not probed: every query counts against the plan)"}
}

// braveMarkupTag matches the <strong> highlighting Brave puts around
// query terms in titles and descriptions.
var braveMarkupTag = regexp.MustCompile(`</?[a-zA-Z][^>]*>`)

func textFromBraveMarkup(text string) string {
	return strings.TrimSpace(html.UnescapeString(braveMarkupTag.ReplaceAllString(text, "")))
}

type braveSearchResponse struct {
	Web struct {
		Results []braveResult `json:"results"`
	} `json:"web"`
}

type braveResult struct {
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Description   string   `json:"description"`
	PageAge       string   `json:"page_age,omitempty"`
	ExtraSnippets []string `json:"extra_snippets,omitempty"`
}
