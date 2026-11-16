package backends

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// crawl4ai (github.com/unclecode/crawl4ai, Apache-2.0 with an
// attribution clause) renders a page in a headless browser on a server
// grounding's operator runs, beside grounding, so a render costs nothing
// but memory. It is the "auto" renderer's free browser rung, between
// the plain fetch and Firecrawl, and can be asked for by name.
//
// Only the rendered HTML is read: web_fetch picks out the main content
// itself (markdownFromHTML), the same way for every renderer. crawl4ai
// runs on its defaults: in the page-reading bake-off (LOG, 2026-11-07)
// the bare render read pages better than with its consent-popup,
// overlay, full-page-scroll and iframe features on, which broke a page
// the bare render read.
//
// The browser dials from its own host, so grounding's dial-time address guard
// does not cover what the page loads. The URL passes WebURLOrRefusal
// and its host's addresses are looked up and checked before it is
// sent, the address it ended on is checked after, and the rest is the
// host's job: the container is kept off loopback, the mesh
// and private addresses by the firewall (PLAN "Page reading").

const crawl4aiRPS = 1.0

// Crawl4AIBackend renders single pages through a crawl4ai server.
type Crawl4AIBackend struct {
	client   *Client
	baseURL  string
	apiToken string
	// resolve looks a host's addresses up; tests replace it.
	resolve func(ctx context.Context, host string) ([]netip.Addr, error)
}

// Crawl4AIUnixSocketPrefix marks a base URL that is a Unix socket's
// path (unix:///run/grounding-browser/grounding-browser.sock): the
// render service can listen on a socket, not a port, so that a systemd
// network filter on its unit (IPAddressDeny of loopback and private
// ranges) need not let it accept TCP on loopback, and a socket
// needs no token: its directory's group says who may connect.
const Crawl4AIUnixSocketPrefix = "unix://"

// NewCrawl4AI creates a crawl4ai backend. An empty baseURL means none
// is configured; Render then says so. A unix:// base URL dials that
// socket, keeping client's timeout and identification.
func NewCrawl4AI(client *Client, baseURL, apiToken string) *Crawl4AIBackend {
	ensureRateLimit("crawl4ai", crawl4aiRPS, 2)
	baseURL = strings.TrimRight(baseURL, "/")
	if socketPath, isSocket := strings.CutPrefix(baseURL, Crawl4AIUnixSocketPrefix); isSocket && socketPath != "" {
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		client = &Client{
			http: &http.Client{Timeout: client.http.Timeout, Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, "unix", socketPath)
				},
			}},
			userAgent: client.userAgent,
			email:     client.email,
		}
		// The host part is never dialled; it names the service in logs.
		baseURL = "http://crawl4ai-render-service"
	}
	return &Crawl4AIBackend{
		client: client, baseURL: baseURL, apiToken: apiToken,
		resolve: func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		},
	}
}

// ResolvingEveryHostToForTests makes every host resolve to address, so
// tests run offline.
func (c *Crawl4AIBackend) ResolvingEveryHostToForTests(address netip.Addr) *Crawl4AIBackend {
	c.resolve = func(context.Context, string) ([]netip.Addr, error) { return []netip.Addr{address}, nil }
	return c
}

// hostResolvesOnlyToPublicAddresses refuses a host any of whose
// addresses is not public. crawl4ai's browser dials from its own host, so a
// name pointed at 127.0.0.1 or a mesh address would aim that host's
// browser at its own services; the plain fetch's dial-time guard is not there to
// stop it. A name can still change its answer between this lookup and
// the browser's (DNS rebinding), and a page can redirect or load from
// anywhere: the host's firewall is the guard for those, and this is the
// layer in front of it.
func (c *Crawl4AIBackend) hostResolvesOnlyToPublicAddresses(ctx context.Context, host string) error {
	if address, err := netip.ParseAddr(host); err == nil {
		return publicAddressOrRefusal(address)
	}
	addresses, err := c.resolve(ctx, host)
	if err != nil {
		return fmt.Errorf("looking up %s: %w", host, err)
	}
	for _, address := range addresses {
		if err := publicAddressOrRefusal(address); err != nil {
			return err
		}
	}
	return nil
}

// Name returns the backend identifier.
func (c *Crawl4AIBackend) Name() string {
	return "crawl4ai"
}

// Configured reports whether a crawl4ai server is set.
func (c *Crawl4AIBackend) Configured() bool {
	return c != nil && c.baseURL != ""
}

// ErrCrawl4AINotConfigured is returned when no server is set.
var ErrCrawl4AINotConfigured = errors.New("crawl4ai: no server configured (set CRAWL4AI_BASE_URL)")

// Crawl4AIPage is one rendered page: its HTML as the browser left it.
type Crawl4AIPage struct {
	HTML       string
	FinalURL   string
	StatusCode int
	// FromCache is true when grounding's cache answered, not crawl4ai.
	FromCache bool `json:"-"`
}

// Render renders pageURL in crawl4ai's browser and returns the HTML.
// A page the site answered with HTTP 400 or above comes back as an
// *HTTPError carrying that status, as the plain fetch does. Successful
// renders are cached (when the server has the cache on), so paging
// through a long page renders it once.
func (c *Crawl4AIBackend) Render(ctx context.Context, pageURL string) (*Crawl4AIPage, error) {
	if !c.Configured() {
		return nil, ErrCrawl4AINotConfigured
	}
	parsedURL, err := url.Parse(pageURL)
	if err != nil {
		return nil, fmt.Errorf("not a URL: %w", err)
	}
	if err := WebURLOrRefusal(parsedURL); err != nil {
		return nil, err
	}
	if err := c.hostResolvesOnlyToPublicAddresses(ctx, parsedURL.Hostname()); err != nil {
		return nil, err
	}

	key := "crawl4ai\x00" + parsedURL.String()
	if cache != nil {
		if cached, ok := cache.get(key); ok {
			var page Crawl4AIPage
			if json.Unmarshal(cached, &page) == nil {
				page.FromCache = true
				return &page, nil
			}
		}
	}

	// The request shape is the bake-off's, run against crawl4ai 0.9.5's
	// Docker server on 2026-11-07. cache_mode bypass: grounding caches,
	// and crawl4ai's own cache would outlive grounding's ten minutes.
	requestBody, err := json.Marshal(map[string]any{
		"urls":           []string{parsedURL.String()},
		"browser_config": map[string]any{"type": "BrowserConfig", "params": map[string]any{"headless": true}},
		"crawler_config": map[string]any{"type": "CrawlerRunConfig", "params": map[string]any{"cache_mode": "bypass"}},
	})
	if err != nil {
		return nil, err
	}
	headers := map[string]string{}
	if c.apiToken != "" {
		headers["Authorization"] = "Bearer " + c.apiToken
	}
	body, err := c.client.Do(ctx, Request{
		Method:  http.MethodPost,
		URL:     c.baseURL + "/crawl",
		Backend: "crawl4ai",
		Body:    requestBody,
		Headers: headers,
	})
	if err != nil {
		// %v, not %w: an HTTP error here is crawl4ai's server's own (a
		// wrong token, a crash), and must not read as the site's status.
		return nil, fmt.Errorf("the crawl4ai server failed: %v", err)
	}

	var response struct {
		Results []struct {
			Success       bool   `json:"success"`
			ErrorMessage  string `json:"error_message"`
			HTML          string `json:"html"`
			URL           string `json:"url"`
			RedirectedURL string `json:"redirected_url"`
			StatusCode    int    `json:"status_code"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("crawl4ai: response is not the expected JSON: %w", err)
	}
	if len(response.Results) == 0 {
		return nil, errors.New("crawl4ai answered with no result")
	}
	result := response.Results[0]
	finalURL := result.RedirectedURL
	if finalURL == "" {
		finalURL = parsedURL.String()
	}
	if landedOn, err := url.Parse(finalURL); err == nil {
		if err := WebURLOrRefusal(landedOn); err != nil {
			return nil, err
		}
	}
	if result.StatusCode >= 400 {
		return nil, &HTTPError{StatusCode: result.StatusCode, Body: firstCharacters(result.ErrorMessage, 300)}
	}
	if !result.Success {
		return nil, fmt.Errorf("crawl4ai could not render the page: %s", firstCharacters(result.ErrorMessage, 300))
	}
	if strings.TrimSpace(result.HTML) == "" {
		return nil, errors.New("crawl4ai rendered the page but returned no HTML")
	}
	page := &Crawl4AIPage{HTML: result.HTML, FinalURL: finalURL, StatusCode: result.StatusCode}
	if cache != nil {
		if encoded, err := json.Marshal(page); err == nil {
			cache.put(key, encoded)
		}
	}
	return page, nil
}

// HealthCheck asks the server's /health, which is free.
func (c *Crawl4AIBackend) HealthCheck(ctx context.Context) HealthStatus {
	if !c.Configured() {
		return HealthStatus{Healthy: true, Message: "not configured (set CRAWL4AI_BASE_URL)"}
	}
	if _, err := c.client.Do(ctx, Request{Method: http.MethodGet, URL: c.baseURL + "/health", Backend: "crawl4ai"}); err != nil {
		return HealthStatus{Healthy: false, Message: err.Error()}
	}
	return HealthStatus{Healthy: true, Message: "ok"}
}
