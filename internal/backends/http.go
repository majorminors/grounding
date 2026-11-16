package backends

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	defaultTimeout = 30 * time.Second

	// maxResponseBodyBytes caps how much of an API response is read.
	// Scholarly APIs return at most a few MB per page; anything beyond
	// this is a malfunctioning or hostile upstream, not data we want.
	maxResponseBodyBytes = 16 << 20

	// 429 retry constants, applied to every backend. One retry
	// distinguishes a transient blip (two concurrent legs of a search
	// landing in the same second on a 1 req/s pool) from genuine
	// exhaustion; more retries only delay the cooldown that lets the
	// caller move on to other backends.
	maxRetries       = 1
	initialBackoff   = 1 * time.Second
	backoffMultipler = 2.0

	// maxCooldown bounds how long a backend is benched after exhausting
	// its retries, even when the upstream asks for longer (OpenAlex's
	// daily-budget reset can be many hours away).
	maxCooldown = 1 * time.Hour
)

// Version is stamped into the User-Agent. cmd sets it from the build
// version at startup; "dev" otherwise.
var Version = "dev"

// userAgent builds the client identification string. The contact
// details are load-bearing: Crossref grants its polite pool to a
// User-Agent carrying "mailto:" (verified against x-api-pool, 2026-09),
// and Wikimedia's 2026 rate limits treat a User-Agent without contact
// details as unidentified (10 req/min instead of 200).
func userAgent(email string) string {
	if email == "" {
		return fmt.Sprintf("grounding/%s (https://grounding.btr.mt)", Version)
	}
	return fmt.Sprintf("grounding/%s (https://grounding.btr.mt; mailto:%s)", Version, email)
}

// rateLimiters is a package-level singleton map of backend -> *rate.Limiter.
// Survives across Client instances so the MCP server's long-lived process
// doesn't reset rate state between tool invocations.
var rateLimiters sync.Map // map[string]*rate.Limiter

// cooldowns tracks session-level cooldown expiry times per backend.
// Set when a backend exhausts its retry budget on 429s. Subsequent
// requests fail immediately with CooldownError until the expiry passes.
var cooldowns sync.Map // map[string]time.Time

const defaultCooldown = 60 * time.Second

// SetRateLimit configures (or overrides) the limiter for a backend.
// Tests use this to speed up rate-limited paths; production code uses
// ensureRateLimit so that the first configuration wins.
func SetRateLimit(backend string, rps float64, burst int) {
	rateLimiters.Store(backend, rate.NewLimiter(rate.Limit(rps), burst))
}

// ensureRateLimit installs a limiter for a backend only if none exists.
// Backend constructors call this rather than SetRateLimit because the
// tools layer constructs backends afresh on every call; overwriting the
// limiter each time would hand every call a full token bucket and make
// the "persists across tool invocations" guarantee a fiction.
func ensureRateLimit(backend string, rps float64, burst int) {
	rateLimiters.LoadOrStore(backend, rate.NewLimiter(rate.Limit(rps), burst))
}

// SetCooldown activates a cooldown for a backend. All requests to
// this backend will fail immediately with CooldownError until the
// duration elapses.
func SetCooldown(backend string, d time.Duration) {
	cooldowns.Store(backend, time.Now().Add(d))
}

// InCooldown returns the cooldown expiry time for a backend, if active.
func InCooldown(backend string) (time.Time, bool) {
	v, ok := cooldowns.Load(backend)
	if !ok {
		return time.Time{}, false
	}
	expiry := v.(time.Time)
	if time.Now().After(expiry) {
		cooldowns.Delete(backend)
		return time.Time{}, false
	}
	return expiry, true
}

// CooldownError is returned when a request is rejected because the
// backend is in a rate-limit cooldown period.
type CooldownError struct {
	Backend string
	Until   time.Time
}

func (e *CooldownError) Error() string {
	remaining := time.Until(e.Until).Truncate(time.Second)
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("rate limit cooldown for %s (%s remaining)", e.Backend, remaining)
}

// getRateLimiter returns the rate limiter for a backend, or nil.
func getRateLimiter(backend string) *rate.Limiter {
	if v, ok := rateLimiters.Load(backend); ok {
		return v.(*rate.Limiter)
	}
	return nil
}

// Client is a shared HTTP client with polite pool support and rate limiting.
type Client struct {
	http      *http.Client
	userAgent string
	email     string // For polite pool (Crossref/OpenAlex)
}

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithEmail sets the email for polite pool requests.
func WithEmail(email string) ClientOption {
	return func(c *Client) {
		c.email = email
	}
}

// WithTimeout sets the HTTP client timeout.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.http.Timeout = timeout
	}
}

// NewClient creates a new HTTP client with the given options.
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		http: &http.Client{
			Timeout: defaultTimeout,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	c.userAgent = userAgent(c.email)
	return c
}

// UserAgent returns the identification string sent on every request.
// Backends that build raw requests outside Do (file downloads, login
// POSTs) use this so they identify themselves the same way.
func (c *Client) UserAgent() string {
	return c.userAgent
}

// Request makes an HTTP request with rate limiting and polite pool headers.
type Request struct {
	Method      string
	URL         string
	Headers     map[string]string
	Backend     string // For rate limiting
	Body        []byte // Optional request body (e.g. JSON for POST endpoints)
	ContentType string // Content-Type header for Body; defaults to application/json when Body is set
}

// Do executes the request and returns the response body.
// A 429 response triggers a single retry after backoff (honouring
// Retry-After), then activates a session-level cooldown for the backend.
func (c *Client) Do(ctx context.Context, req Request) ([]byte, error) {
	// Cached answers cost nothing and bypass cooldowns too: a backend
	// benched for rate limiting can still answer what it already said.
	cacheable := cache != nil && len(req.Body) == 0 && (req.Method == "" || req.Method == http.MethodGet)
	key := ""
	if cacheable {
		key = cacheKey(req)
		if body, ok := cache.get(key); ok {
			return body, nil
		}
	}

	// Check cooldown before doing anything
	if expiry, active := InCooldown(req.Backend); active {
		return nil, &CooldownError{Backend: req.Backend, Until: expiry}
	}

	// Apply rate limiting if configured
	if limiter := getRateLimiter(req.Backend); limiter != nil {
		if err := limiter.Wait(ctx); err != nil {
			return nil, err
		}
	}

	body, err := c.doOnce(ctx, req)

	if isHTTP429(err) {
		body, err = c.retryOn429(ctx, req, err, body)
	}

	if cacheable && err == nil {
		cache.put(key, body)
	}

	return body, err
}

// retryOn429 handles 429 retry with exponential backoff.
func (c *Client) retryOn429(ctx context.Context, req Request, lastErr error, lastBody []byte) ([]byte, error) {
	backoff := initialBackoff

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Check for Retry-After header value encoded in the error
		if httpErr, ok := lastErr.(*HTTPError); ok {
			if ra := httpErr.RetryAfter; ra > 0 {
				backoff = ra
			}
		}

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		// Re-acquire rate limit token before retry
		if limiter := getRateLimiter(req.Backend); limiter != nil {
			if err := limiter.Wait(ctx); err != nil {
				return nil, err
			}
		}

		body, err := c.doOnce(ctx, req)
		if !isHTTP429(err) {
			return body, err
		}

		lastErr = err
		lastBody = body
		backoff = time.Duration(float64(backoff) * backoffMultipler)
	}

	// Retries exhausted — activate cooldown so subsequent requests
	// fail immediately instead of retrying a lost cause.
	cooldownDur := defaultCooldown
	if httpErr, ok := lastErr.(*HTTPError); ok && httpErr.RetryAfter > cooldownDur {
		cooldownDur = httpErr.RetryAfter
	}
	if cooldownDur > maxCooldown {
		cooldownDur = maxCooldown
	}
	SetCooldown(req.Backend, cooldownDur)

	return lastBody, lastErr
}

// doOnce executes a single HTTP request (no retry).
func (c *Client) doOnce(ctx context.Context, req Request) ([]byte, error) {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}

	var bodyReader io.Reader
	if len(req.Body) > 0 {
		bodyReader = bytes.NewReader(req.Body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if len(req.Body) > 0 {
		contentType := req.ContentType
		if contentType == "" {
			contentType = "application/json"
		}
		httpReq.Header.Set("Content-Type", contentType)
	}

	// Set headers. The contact email travels in the User-Agent (see
	// userAgent); a header literally named "mailto" is read by nobody.
	httpReq.Header.Set("User-Agent", c.userAgent)
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	// Execute
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	applyRateLimitHeaders(req.Backend, resp.Header)

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if len(body) > maxResponseBodyBytes {
		return nil, fmt.Errorf("response from %s exceeds %d bytes", req.Backend, maxResponseBodyBytes)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		httpErr := &HTTPError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			httpErr.RetryAfter = retryAfterFromHeaders(resp.Header)
		}
		return body, httpErr
	}

	return body, nil
}

// retryAfterFromHeaders reads how long a 429 asks us to wait. Retry-After
// (seconds) is the standard; OpenAlex instead sends x-ratelimit-reset
// with the seconds until its daily budget resets, which is the honest
// answer when the budget is what ran out.
func retryAfterFromHeaders(h http.Header) time.Duration {
	if ra := h.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	if reset := h.Get("x-ratelimit-reset"); reset != "" {
		if secs, err := strconv.Atoi(reset); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 0
}

// applyRateLimitHeaders tunes a backend's limiter from the upstream's
// own declaration when it publishes one. Crossref sends
// x-rate-limit-limit and x-rate-limit-interval (e.g. "3" and "1s"), and
// its allowance depends on which pool it assigned us to, so trusting
// the header beats hard-coding a guess.
func applyRateLimitHeaders(backend string, h http.Header) {
	limitStr := h.Get("x-rate-limit-limit")
	intervalStr := h.Get("x-rate-limit-interval")
	if limitStr == "" || intervalStr == "" {
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		return
	}
	interval, err := time.ParseDuration(intervalStr)
	if err != nil || interval <= 0 {
		return
	}
	limiter := getRateLimiter(backend)
	if limiter == nil {
		return
	}
	declared := rate.Limit(float64(limit) / interval.Seconds())
	if limiter.Limit() != declared {
		limiter.SetLimit(declared)
	}
}

// GetJSON makes a GET request and unmarshals the JSON response.
func (c *Client) GetJSON(ctx context.Context, url string, backend string, result any) error {
	body, err := c.Do(ctx, Request{
		Method:  http.MethodGet,
		URL:     url,
		Backend: backend,
	})
	if err != nil {
		return err
	}
	return json.Unmarshal(body, result)
}

// HTTPError represents an HTTP error response.
type HTTPError struct {
	StatusCode int
	Body       string
	RetryAfter time.Duration // Parsed from Retry-After header on 429
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

// IsNotFound returns true if the error is an HTTP 404.
// Unwraps error chains from fmt.Errorf %w.
func IsNotFound(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusNotFound
	}
	return false
}

// IsRateLimited returns true if the error is an HTTP 429 or a
// CooldownError. This lets callers (e.g. citation_graph fallback
// logic) treat both cases identically.
func IsRateLimited(err error) bool {
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusTooManyRequests
	}
	var cooldownErr *CooldownError
	return errors.As(err, &cooldownErr)
}

// isHTTP429 checks if an error is a 429 response.
func isHTTP429(err error) bool {
	return IsRateLimited(err)
}
