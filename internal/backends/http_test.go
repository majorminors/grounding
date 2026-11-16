package backends

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_GetJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("expected User-Agent header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"test","count":42}`))
	}))
	defer server.Close()

	client := NewClient()
	var result struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	err := client.GetJSON(context.Background(), server.URL, "test", &result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Name != "test" || result.Count != 42 {
		t.Errorf("unexpected result: %+v", result)
	}
}

func TestClient_WithEmail(t *testing.T) {
	var gotUserAgent, gotMailtoHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserAgent = r.Header.Get("User-Agent")
		gotMailtoHeader = r.Header.Get("mailto")
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := NewClient(WithEmail("test@example.com"))
	var result struct{}
	client.GetJSON(context.Background(), server.URL, "test", &result)

	// Crossref's polite pool and Wikimedia's identified-client tier both
	// key off contact details in the User-Agent; a bare "mailto" header
	// is read by neither, so it must not be what carries the email.
	if !strings.Contains(gotUserAgent, "mailto:test@example.com") {
		t.Errorf("expected mailto in User-Agent, got %q", gotUserAgent)
	}
	if gotMailtoHeader != "" {
		t.Errorf("expected no mailto header, got %q", gotMailtoHeader)
	}
}

func TestClient_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer server.Close()

	client := NewClient()
	_, err := client.Do(context.Background(), Request{URL: server.URL})

	if err == nil {
		t.Fatal("expected error")
	}

	if !IsNotFound(err) {
		t.Errorf("expected not found error, got %v", err)
	}
}

func TestClient_RateLimiting(t *testing.T) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	// 20 rps = 50ms between requests
	SetRateLimit("test-rate", 20, 1)

	client := NewClient()
	start := time.Now()

	// Make 3 requests
	for i := 0; i < 3; i++ {
		_, err := client.Do(context.Background(), Request{URL: server.URL, Backend: "test-rate"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("rate limiting not applied, elapsed: %v", elapsed)
	}

	if callCount.Load() != 3 {
		t.Errorf("expected 3 calls, got %d", callCount.Load())
	}
}

func TestClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	// 1 rps = 1s between requests
	SetRateLimit("test-cancel", 1, 1)

	client := NewClient()

	// First call to consume the burst token
	client.Do(context.Background(), Request{URL: server.URL, Backend: "test-cancel"})

	// Cancel before rate limit expires
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Do(ctx, Request{URL: server.URL, Backend: "test-cancel"})
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestIsRateLimited(t *testing.T) {
	err := &HTTPError{StatusCode: 429, Body: "too many requests"}
	if !IsRateLimited(err) {
		t.Error("expected IsRateLimited to return true")
	}

	err = &HTTPError{StatusCode: 200, Body: "ok"}
	if IsRateLimited(err) {
		t.Error("expected IsRateLimited to return false")
	}
}

func TestClient_429Retry_SemanticScholar(t *testing.T) {
	cooldowns.Delete("semantic-scholar")
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("rate limited"))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewClient()
	body, err := client.Do(context.Background(), Request{
		URL:     server.URL,
		Backend: "semantic-scholar",
	})
	if err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("unexpected body: %s", body)
	}
	if attempts.Load() != 2 {
		t.Errorf("expected 2 attempts (1 initial + 1 retry), got %d", attempts.Load())
	}
}

func TestClient_429Retry_ExhaustedReturnsError(t *testing.T) {
	cooldowns.Delete("semantic-scholar")
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limited"))
	}))
	defer server.Close()

	client := NewClient()
	_, err := client.Do(context.Background(), Request{
		URL:     server.URL,
		Backend: "semantic-scholar",
	})
	if !IsRateLimited(err) {
		t.Fatalf("expected rate limited error after exhausting retries, got %v", err)
	}
	// 1 initial + 1 retry = 2
	if attempts.Load() != 2 {
		t.Errorf("expected 2 total attempts, got %d", attempts.Load())
	}
	// Clean up cooldown set by retry exhaustion
	cooldowns.Delete("semantic-scholar")
}

func TestClient_429Retry_AnyBackend(t *testing.T) {
	// Every backend gets one retry and then a cooldown: Crossref's
	// public pool 429s transiently when two legs of a search land in
	// the same second, and OpenAlex 429s for the rest of the day when
	// its budget is spent. Both are handled by the same path.
	const backend = "test-429-any"
	cooldowns.Delete(backend)
	defer cooldowns.Delete(backend)

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limited"))
	}))
	defer server.Close()

	client := NewClient()
	_, err := client.Do(context.Background(), Request{
		URL:     server.URL,
		Backend: backend,
	})
	if !IsRateLimited(err) {
		t.Fatalf("expected rate limited error, got %v", err)
	}
	if attempts.Load() != 2 {
		t.Errorf("expected 1 attempt + 1 retry, got %d", attempts.Load())
	}
	if _, active := InCooldown(backend); !active {
		t.Error("expected cooldown after retries exhausted")
	}
}

func TestRetryAfterFromHeaders(t *testing.T) {
	h := http.Header{}
	if got := retryAfterFromHeaders(h); got != 0 {
		t.Errorf("no headers: got %v", got)
	}
	h.Set("x-ratelimit-reset", "120")
	if got := retryAfterFromHeaders(h); got != 120*time.Second {
		t.Errorf("x-ratelimit-reset: got %v", got)
	}
	h.Set("Retry-After", "5")
	if got := retryAfterFromHeaders(h); got != 5*time.Second {
		t.Errorf("Retry-After should win: got %v", got)
	}
}

func TestClient_CooldownIsCapped(t *testing.T) {
	const backend = "test-cooldown-cap"
	cooldowns.Delete(backend)
	defer cooldowns.Delete(backend)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Retry-After of several hours (as OpenAlex reports via
		// x-ratelimit-reset) must not bench the backend that long.
		w.Header().Set("Retry-After", "1")
		w.Header().Set("x-ratelimit-reset", "36000")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := NewClient()
	client.Do(context.Background(), Request{URL: server.URL, Backend: backend})
	expiry, active := InCooldown(backend)
	if !active {
		t.Fatal("expected cooldown")
	}
	if remaining := time.Until(expiry); remaining > maxCooldown+time.Second {
		t.Errorf("cooldown %v exceeds cap %v", remaining, maxCooldown)
	}
}

func TestApplyRateLimitHeaders(t *testing.T) {
	const backend = "test-header-limit"
	SetRateLimit(backend, 1, 1)

	h := http.Header{}
	h.Set("x-rate-limit-limit", "3")
	h.Set("x-rate-limit-interval", "1s")
	applyRateLimitHeaders(backend, h)
	if got := getRateLimiter(backend).Limit(); got != 3 {
		t.Errorf("expected limiter retuned to 3 req/s, got %v", got)
	}

	// Malformed or missing headers leave the limiter alone.
	h.Set("x-rate-limit-interval", "soon")
	applyRateLimitHeaders(backend, h)
	if got := getRateLimiter(backend).Limit(); got != 3 {
		t.Errorf("malformed interval should not change limiter, got %v", got)
	}
}

func TestClient_429Retry_RespectsRetryAfter(t *testing.T) {
	cooldowns.Delete("semantic-scholar")
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("rate limited"))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewClient()
	start := time.Now()
	_, err := client.Do(context.Background(), Request{
		URL:     server.URL,
		Backend: "semantic-scholar",
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	// Should have waited at least 1s from the Retry-After header
	if elapsed < 1*time.Second {
		t.Errorf("expected at least 1s wait from Retry-After, elapsed: %v", elapsed)
	}
}

func TestClient_429Retry_RespectsContextCancellation(t *testing.T) {
	cooldowns.Delete("semantic-scholar")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limited"))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	client := NewClient()
	_, err := client.Do(ctx, Request{
		URL:     server.URL,
		Backend: "semantic-scholar",
	})
	// Should get context deadline exceeded, not a 429 error
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestCooldown_SetAndCheck(t *testing.T) {
	backend := "test-cooldown-lifecycle"

	// No cooldown initially
	if _, active := InCooldown(backend); active {
		t.Fatal("expected no cooldown initially")
	}

	// Set a short cooldown
	SetCooldown(backend, 200*time.Millisecond)

	expiry, active := InCooldown(backend)
	if !active {
		t.Fatal("expected cooldown to be active")
	}
	if time.Until(expiry) <= 0 {
		t.Error("expected expiry in the future")
	}

	// Wait for cooldown to expire
	time.Sleep(250 * time.Millisecond)

	if _, active := InCooldown(backend); active {
		t.Fatal("expected cooldown to have expired")
	}
}

func TestCooldown_DoReturnsErrorWithoutHTTP(t *testing.T) {
	backend := "test-cooldown-no-http"
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	SetCooldown(backend, 5*time.Second)
	defer cooldowns.Delete(backend) // cleanup

	client := NewClient()
	_, err := client.Do(context.Background(), Request{
		URL:     server.URL,
		Backend: backend,
	})

	if err == nil {
		t.Fatal("expected CooldownError")
	}

	var cooldownErr *CooldownError
	if !errors.As(err, &cooldownErr) {
		t.Fatalf("expected CooldownError, got %T: %v", err, err)
	}
	if cooldownErr.Backend != backend {
		t.Errorf("expected backend %q, got %q", backend, cooldownErr.Backend)
	}
	if callCount.Load() != 0 {
		t.Errorf("expected no HTTP requests, got %d", callCount.Load())
	}
}

func TestCooldown_IsRateLimitedMatchesCooldownError(t *testing.T) {
	err := &CooldownError{Backend: "test", Until: time.Now().Add(time.Minute)}
	if !IsRateLimited(err) {
		t.Error("expected IsRateLimited to return true for CooldownError")
	}

	// Wrapped in fmt.Errorf
	wrapped := fmt.Errorf("search failed: %w", err)
	if !IsRateLimited(wrapped) {
		t.Error("expected IsRateLimited to return true for wrapped CooldownError")
	}
}

func TestCooldown_SetByRetryExhaustion(t *testing.T) {
	backend := "test-cooldown-retry"
	// Clear any existing cooldown
	cooldowns.Delete(backend)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limited"))
	}))
	defer server.Close()

	// Temporarily override the backend name to "semantic-scholar" since
	// retryOn429 is only called for that backend. Instead, we'll test
	// directly via the mechanism.
	// We need to use "semantic-scholar" backend to trigger retry logic.
	cooldowns.Delete("semantic-scholar")

	client := NewClient()
	_, err := client.Do(context.Background(), Request{
		URL:     server.URL,
		Backend: "semantic-scholar",
	})
	if !IsRateLimited(err) {
		t.Fatalf("expected rate limited error, got %v", err)
	}

	// Cooldown should now be set for semantic-scholar
	expiry, active := InCooldown("semantic-scholar")
	if !active {
		t.Fatal("expected cooldown to be active after retry exhaustion")
	}
	remaining := time.Until(expiry)
	if remaining < 50*time.Second || remaining > 65*time.Second {
		t.Errorf("expected ~60s cooldown, got %v remaining", remaining)
	}

	// Cleanup
	cooldowns.Delete("semantic-scholar")
}

func TestHTTPError_RetryAfterParsing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("slow down"))
	}))
	defer server.Close()

	client := NewClient()
	// Use doOnce to get raw error without retry
	_, err := client.doOnce(context.Background(), Request{URL: server.URL})
	if err == nil {
		t.Fatal("expected error")
	}

	httpErr, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("expected HTTPError, got %T", err)
	}
	if httpErr.RetryAfter != 5*time.Second {
		t.Errorf("expected RetryAfter=5s, got %v", httpErr.RetryAfter)
	}
}
