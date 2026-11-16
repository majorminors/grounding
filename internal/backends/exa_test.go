package backends

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// exaCapturedRequest records what the fake Exa server received on its
// most recent request, so tests can assert the wire shape.
type exaCapturedRequest struct {
	method      string
	path        string
	apiKey      string
	contentType string
	body        map[string]any
	hits        int
}

// newExaTestServer returns an httptest server that captures each
// request and replies with the given status and body.
func newExaTestServer(t *testing.T, status int, responseBody string) (*httptest.Server, *exaCapturedRequest) {
	t.Helper()
	captured := &exaCapturedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.hits++
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.apiKey = r.Header.Get("x-api-key")
		captured.contentType = r.Header.Get("Content-Type")
		captured.body = nil

		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &captured.body); err != nil {
				t.Errorf("request body is not JSON: %v\n%s", err, raw)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(responseBody))
	}))
	return server, captured
}

// newExaTestBackend points a backend at the fake server, lifts the
// 5 RPS default so multi-call tests don't stall, and clears any
// cooldown a previous test's 429 left in the package-level map.
func newExaTestBackend(serverURL, apiKey string) *ExaBackend {
	backend := NewExa(NewClient(), apiKey)
	backend.baseURL = serverURL
	SetRateLimit("exa", 1000, 10)
	cooldowns.Delete("exa")
	return backend
}

// exaSampleResponse follows the reference's example response, with a
// second result exercising the nullable fields.
const exaSampleResponse = `{
  "requestId": "b5947044c4b78efa9552a7c89b306d95",
  "results": [
    {
      "title": "A Comprehensive Overview of Large Language Models",
      "url": "https://arxiv.org/pdf/2307.06435.pdf",
      "publishedDate": "2023-11-16T01:36:32.547Z",
      "author": "Humza Naveed",
      "id": "https://arxiv.org/abs/2307.06435",
      "text": "Abstract Large Language Models (LLMs) have recently demonstrated remarkable capabilities.",
      "highlights": ["LLMs have recently demonstrated remarkable capabilities.", "Second snippet."],
      "highlightScores": [0.91, 0.72],
      "summary": "An overview of LLMs.",
      "score": 0.46
    },
    {
      "title": "Second Result",
      "url": "https://example.com/second",
      "publishedDate": null,
      "author": null
    }
  ],
  "costDollars": {
    "total": 0.027,
    "search": {"neural": 0.007},
    "contents": {"text": 0.01, "highlights": 0.01}
  },
  "searchTime": 312.4
}`

// exaNestedNumber digs a numeric leaf out of the decoded request body
// (JSON numbers decode as float64 in a map[string]any).
func exaNestedNumber(t *testing.T, body map[string]any, path ...string) float64 {
	t.Helper()
	var current any = body
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("path %v: expected object at %q, got %T", path, key, current)
		}
		current, ok = object[key]
		if !ok {
			t.Fatalf("path %v: key %q missing", path, key)
		}
	}
	number, ok := current.(float64)
	if !ok {
		t.Fatalf("path %v: expected number, got %T (%v)", path, current, current)
	}
	return number
}

func TestExa_Name(t *testing.T) {
	backend := NewExa(NewClient(), "")
	if backend.Name() != "exa" {
		t.Errorf("Expected name 'exa', got '%s'", backend.Name())
	}
}

func TestExa_Search_RequestShape(t *testing.T) {
	server, captured := newExaTestServer(t, http.StatusOK, exaSampleResponse)
	defer server.Close()
	backend := newExaTestBackend(server.URL, "test-key")

	t.Run("defaults", func(t *testing.T) {
		_, err := backend.Search(context.Background(), "cognitive load theory", 7, ExaSearchOptions{})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if captured.method != http.MethodPost {
			t.Errorf("method: want POST, got %s", captured.method)
		}
		if captured.path != "/search" {
			t.Errorf("path: want /search, got %s", captured.path)
		}
		if captured.apiKey != "test-key" {
			t.Errorf("x-api-key header: want %q, got %q", "test-key", captured.apiKey)
		}
		if captured.contentType != "application/json" {
			t.Errorf("Content-Type: want application/json, got %q", captured.contentType)
		}

		if got := captured.body["query"]; got != "cognitive load theory" {
			t.Errorf("query: want %q, got %v", "cognitive load theory", got)
		}
		if got := captured.body["type"]; got != "auto" {
			t.Errorf("type: want auto, got %v", got)
		}
		if got := exaNestedNumber(t, captured.body, "numResults"); got != 7 {
			t.Errorf("numResults: want 7, got %v", got)
		}
		if got := exaNestedNumber(t, captured.body, "contents", "text", "maxCharacters"); got != exaDefaultTextCharacters {
			t.Errorf("contents.text.maxCharacters: want %d, got %v", exaDefaultTextCharacters, got)
		}
		if got := exaNestedNumber(t, captured.body, "contents", "highlights", "maxCharacters"); got != exaDefaultHighlightCharacters {
			t.Errorf("contents.highlights.maxCharacters: want %d, got %v", exaDefaultHighlightCharacters, got)
		}

		// Deprecated highlight knobs must not be sent.
		highlights := captured.body["contents"].(map[string]any)["highlights"].(map[string]any)
		for _, deprecated := range []string{"numSentences", "highlightsPerUrl"} {
			if _, present := highlights[deprecated]; present {
				t.Errorf("deprecated highlights.%s was sent", deprecated)
			}
		}

		// Optional filters are omitted, not sent empty.
		for _, absent := range []string{"includeDomains", "startPublishedDate"} {
			if _, present := captured.body[absent]; present {
				t.Errorf("%s should be omitted when unset, got %v", absent, captured.body[absent])
			}
		}
	})

	t.Run("options", func(t *testing.T) {
		opts := ExaSearchOptions{
			IncludeDomains:     []string{"arxiv.org", "*.edu"},
			StartPublishedDate: "2024-01-01T00:00:00.000Z",
			MaxTextCharacters:  500,
			SearchType:         "fast",
		}
		_, err := backend.Search(context.Background(), "working memory", 3, opts)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if got := captured.body["type"]; got != "fast" {
			t.Errorf("type: want fast, got %v", got)
		}
		if got := captured.body["startPublishedDate"]; got != "2024-01-01T00:00:00.000Z" {
			t.Errorf("startPublishedDate: got %v", got)
		}
		domains, ok := captured.body["includeDomains"].([]any)
		if !ok || len(domains) != 2 || domains[0] != "arxiv.org" || domains[1] != "*.edu" {
			t.Errorf("includeDomains: got %v", captured.body["includeDomains"])
		}
		if got := exaNestedNumber(t, captured.body, "contents", "text", "maxCharacters"); got != 500 {
			t.Errorf("contents.text.maxCharacters: want 500, got %v", got)
		}
	})

	t.Run("text budget clamps to API maximum", func(t *testing.T) {
		_, err := backend.Search(context.Background(), "q", 1, ExaSearchOptions{MaxTextCharacters: 50000})
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if got := exaNestedNumber(t, captured.body, "contents", "text", "maxCharacters"); got != exaMaxContentCharacters {
			t.Errorf("contents.text.maxCharacters: want %d, got %v", exaMaxContentCharacters, got)
		}
	})

	t.Run("unknown search type rejected before the metered call", func(t *testing.T) {
		hitsBefore := captured.hits
		_, err := backend.Search(context.Background(), "q", 1, ExaSearchOptions{SearchType: "neural"})
		if err == nil || !strings.Contains(err.Error(), "unknown search type") {
			t.Fatalf("want unknown search type error, got %v", err)
		}
		if captured.hits != hitsBefore {
			t.Error("server should not be hit for an invalid search type")
		}
	})
}

func TestExa_Search_ResponseMapping(t *testing.T) {
	server, _ := newExaTestServer(t, http.StatusOK, exaSampleResponse)
	defer server.Close()
	backend := newExaTestBackend(server.URL, "test-key")

	results, err := backend.Search(context.Background(), "large language models", 10, ExaSearchOptions{})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}

	first := results[0]
	if first.Title != "A Comprehensive Overview of Large Language Models" {
		t.Errorf("Title: got %q", first.Title)
	}
	if first.URL != "https://arxiv.org/pdf/2307.06435.pdf" {
		t.Errorf("URL: got %q", first.URL)
	}
	if first.Author != "Humza Naveed" {
		t.Errorf("Author: got %q", first.Author)
	}
	if first.PublishedDate != "2023-11-16T01:36:32.547Z" {
		t.Errorf("PublishedDate: got %q", first.PublishedDate)
	}
	if !strings.HasPrefix(first.Text, "Abstract Large Language Models") {
		t.Errorf("Text: got %q", first.Text)
	}
	if len(first.Highlights) != 2 || first.Highlights[1] != "Second snippet." {
		t.Errorf("Highlights: got %v", first.Highlights)
	}
	if first.Summary != "An overview of LLMs." {
		t.Errorf("Summary: got %q", first.Summary)
	}
	if first.Score != 0.46 {
		t.Errorf("Score: want 0.46, got %v", first.Score)
	}

	// Nulls decode to zero values rather than failing.
	second := results[1]
	if second.Title != "Second Result" || second.URL != "https://example.com/second" {
		t.Errorf("second result: %+v", second)
	}
	if second.Author != "" || second.PublishedDate != "" || second.Text != "" || len(second.Highlights) != 0 || second.Score != 0 {
		t.Errorf("second result should have zero-value optional fields: %+v", second)
	}
}

func TestExa_Search_TruncatesToLimit(t *testing.T) {
	// If the API over-delivers, the caller still gets at most limit.
	server, _ := newExaTestServer(t, http.StatusOK, exaSampleResponse)
	defer server.Close()
	backend := newExaTestBackend(server.URL, "test-key")

	results, err := backend.Search(context.Background(), "q", 1, ExaSearchOptions{})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("want 1 result after truncation, got %d", len(results))
	}
}

func TestExa_Search_CapturesCost(t *testing.T) {
	t.Run("costDollars.total recorded", func(t *testing.T) {
		server, _ := newExaTestServer(t, http.StatusOK, exaSampleResponse)
		defer server.Close()
		backend := newExaTestBackend(server.URL, "test-key")

		if _, err := backend.Search(context.Background(), "q", 5, ExaSearchOptions{}); err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if backend.LastCostDollars != 0.027 {
			t.Errorf("LastCostDollars: want 0.027, got %v", backend.LastCostDollars)
		}
	})

	t.Run("missing costDollars resets to zero", func(t *testing.T) {
		server, _ := newExaTestServer(t, http.StatusOK, `{"requestId":"x","results":[]}`)
		defer server.Close()
		backend := newExaTestBackend(server.URL, "test-key")
		backend.LastCostDollars = 0.5 // stale value from an earlier call

		if _, err := backend.Search(context.Background(), "q", 5, ExaSearchOptions{}); err != nil {
			t.Fatalf("Search failed: %v", err)
		}
		if backend.LastCostDollars != 0 {
			t.Errorf("LastCostDollars: want 0 when the response carries no cost, got %v", backend.LastCostDollars)
		}
	})
}

func TestExa_Search_EmptyKey(t *testing.T) {
	server, captured := newExaTestServer(t, http.StatusOK, exaSampleResponse)
	defer server.Close()
	backend := newExaTestBackend(server.URL, "")

	_, err := backend.Search(context.Background(), "q", 5, ExaSearchOptions{})
	if err == nil {
		t.Fatal("expected error with empty API key")
	}
	if !strings.Contains(err.Error(), "EXA_API_KEY not configured") {
		t.Errorf("error should name the missing env var, got %q", err.Error())
	}
	if captured.hits != 0 {
		t.Error("server should not be hit without an API key")
	}
}

func TestExa_Search_APIError(t *testing.T) {
	t.Run("error envelope surfaced with tag", func(t *testing.T) {
		body := `{"requestId":"f2a4c6e8b0d2f4a6c8e0b2d4f6a8c0e2","error":"Invalid API key","tag":"INVALID_API_KEY"}`
		server, _ := newExaTestServer(t, http.StatusUnauthorized, body)
		defer server.Close()
		backend := newExaTestBackend(server.URL, "bad-key")

		_, err := backend.Search(context.Background(), "q", 5, ExaSearchOptions{})
		if err == nil {
			t.Fatal("expected error on 401")
		}
		if !strings.Contains(err.Error(), "Invalid API key") || !strings.Contains(err.Error(), "INVALID_API_KEY") {
			t.Errorf("error should carry Exa's message and tag, got %q", err.Error())
		}

		var apiErr *ExaAPIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("expected *ExaAPIError in chain, got %T", err)
		}
		if apiErr.StatusCode != http.StatusUnauthorized || apiErr.Tag != "INVALID_API_KEY" {
			t.Errorf("ExaAPIError fields: %+v", apiErr)
		}
	})

	t.Run("429 still reads as rate limited", func(t *testing.T) {
		// The shared client retries a 429 once (1s backoff) and then
		// puts the backend into a 60s cooldown, which would poison
		// every later test in this process without the cleanup.
		defer cooldowns.Delete("exa")

		body := `{"requestId":"7f9b","error":"You've exceeded the Exa rate limit","tag":"RATE_LIMIT_EXCEEDED"}`
		server, _ := newExaTestServer(t, http.StatusTooManyRequests, body)
		defer server.Close()
		backend := newExaTestBackend(server.URL, "test-key")

		_, err := backend.Search(context.Background(), "q", 5, ExaSearchOptions{})
		if err == nil {
			t.Fatal("expected error on 429")
		}
		if !IsRateLimited(err) {
			t.Errorf("IsRateLimited should see through ExaAPIError, got %v", err)
		}
	})

	t.Run("non-JSON body falls back to raw text", func(t *testing.T) {
		server, _ := newExaTestServer(t, http.StatusBadGateway, "Bad Gateway")
		defer server.Close()
		backend := newExaTestBackend(server.URL, "test-key")

		_, err := backend.Search(context.Background(), "q", 5, ExaSearchOptions{})
		if err == nil {
			t.Fatal("expected error on 502")
		}
		if !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "Bad Gateway") {
			t.Errorf("error should include status and body, got %q", err.Error())
		}
	})
}

func TestExa_Search_LimitClamping(t *testing.T) {
	server, captured := newExaTestServer(t, http.StatusOK, `{"requestId":"x","results":[]}`)
	defer server.Close()
	backend := newExaTestBackend(server.URL, "test-key")

	tests := []struct {
		limit int
		want  int
	}{
		{0, exaDefaultResults},  // unset takes the default
		{-3, exaDefaultResults}, // nonsense takes the default
		{1, 1},
		{5, 5},
		{20, 20},
		{21, exaMaxResults},
		{100, exaMaxResults},
	}
	for _, tt := range tests {
		if _, err := backend.Search(context.Background(), "q", tt.limit, ExaSearchOptions{}); err != nil {
			t.Fatalf("Search(limit=%d) failed: %v", tt.limit, err)
		}
		if got := exaNestedNumber(t, captured.body, "numResults"); int(got) != tt.want {
			t.Errorf("limit %d: want numResults %d, got %v", tt.limit, tt.want, got)
		}
	}
}

func TestExa_HealthCheck(t *testing.T) {
	t.Run("no key is available but unconfigured", func(t *testing.T) {
		server, captured := newExaTestServer(t, http.StatusOK, exaSampleResponse)
		defer server.Close()
		backend := newExaTestBackend(server.URL, "")

		status := backend.HealthCheck(context.Background())
		if !status.Healthy {
			t.Errorf("want Healthy=true without a key (opt-in backend), got %+v", status)
		}
		if !strings.Contains(status.Message, "EXA_API_KEY") {
			t.Errorf("message should tell the operator which env var to set, got %q", status.Message)
		}
		if captured.hits != 0 {
			t.Error("server should not be hit without an API key")
		}
	})

	t.Run("with key makes the cheapest call and reports cost", func(t *testing.T) {
		server, captured := newExaTestServer(t, http.StatusOK, `{"requestId":"x","results":[{"title":"t","url":"https://example.com"}],"costDollars":{"total":0.007,"search":{"neural":0.007}}}`)
		defer server.Close()
		backend := newExaTestBackend(server.URL, "test-key")

		status := backend.HealthCheck(context.Background())
		if !status.Healthy {
			t.Fatalf("want Healthy=true, got %+v", status)
		}
		if captured.apiKey != "test-key" {
			t.Errorf("x-api-key header: got %q", captured.apiKey)
		}
		if got := exaNestedNumber(t, captured.body, "numResults"); got != 1 {
			t.Errorf("health check numResults: want 1, got %v", got)
		}
		if _, present := captured.body["contents"]; present {
			t.Errorf("health check must not request contents (billed per page), got %v", captured.body["contents"])
		}
		if !strings.Contains(status.Message, "0.0070") {
			t.Errorf("message should report the cost, got %q", status.Message)
		}
		if backend.LastCostDollars != 0.007 {
			t.Errorf("LastCostDollars: want 0.007, got %v", backend.LastCostDollars)
		}
	})

	t.Run("API error is unhealthy", func(t *testing.T) {
		body := `{"requestId":"x","error":"Invalid API key","tag":"INVALID_API_KEY"}`
		server, _ := newExaTestServer(t, http.StatusUnauthorized, body)
		defer server.Close()
		backend := newExaTestBackend(server.URL, "bad-key")

		status := backend.HealthCheck(context.Background())
		if status.Healthy {
			t.Errorf("want Healthy=false on 401, got %+v", status)
		}
		if !strings.Contains(status.Message, "INVALID_API_KEY") {
			t.Errorf("message should surface Exa's tag, got %q", status.Message)
		}
	})
}
