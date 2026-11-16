package backends

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// WebPageFetcher fetches an arbitrary public web page for the web_fetch
// tool. It is a separate client from Client because the URL comes from
// the caller, which may be a prompt-injected model: Client is built for
// a fixed set of API hosts, this one for anywhere on the public
// internet and nowhere else.
//
// Server-side request forgery is the risk. A server running grounding
// can reach its own loopback services, often a mesh VPN (carrier-grade
// NAT, 100.64/10) and, on many hosts, a cloud metadata address. A fetch tool that
// followed a caller's URL there would hand the caller whatever those
// services return. So every connection this client makes is checked
// after DNS resolution, at dial time, against publicAddressOrRefusal:
// that covers redirects (each hop dials afresh) and DNS rebinding (a
// name that resolves public at one lookup and private at the next is
// judged by the address actually dialled).

const (
	webPageFetchTimeout = 20 * time.Second

	// webPageMaxBodyBytes caps what is read of one page. A readable
	// article is well under a megabyte of HTML; past this it is a
	// file, not a page.
	webPageMaxBodyBytes = 5 << 20

	webPageMaxRedirects = 5

	// Fetches go to arbitrary hosts, so the limiter only bounds how
	// hard grounding as a whole can be driven as a fetcher.
	webPageFetchRPS   = 5.0
	webPageFetchBurst = 5
)

// WebPage is one fetched page, before any conversion.
type WebPage struct {
	RequestedURL string `json:"requested_url"`
	FinalURL     string `json:"final_url"` // after redirects
	StatusCode   int    `json:"status_code"`
	ContentType  string `json:"content_type"` // media type only, e.g. "text/html"
	Charset      string `json:"charset,omitempty"`
	Body         []byte `json:"body"`
	// FromCache is true when grounding's cache answered, not the site.
	FromCache bool `json:"-"`
}

// ErrAddressNotPublic is returned when a URL resolves to, or redirects
// to, an address grounding refuses to fetch from.
var ErrAddressNotPublic = errors.New("address is not on the public internet")

// nonPublicPrefixes are the ranges netip's own predicates miss.
// Carrier-grade NAT (100.64.0.0/10) is the one that matters most here:
// it holds NetBird's and Tailscale's mesh addresses and some cloud
// providers' metadata services.
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT, mesh VPNs
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),  // documentation
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),  // reserved, broadcast
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64: could translate to a private IPv4 address
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001:db8::/32"), // documentation
}

// publicAddressOrRefusal returns nil when address is somewhere on the
// public internet, and ErrAddressNotPublic otherwise.
func publicAddressOrRefusal(address netip.Addr) error {
	address = address.Unmap() // ::ffff:127.0.0.1 is 127.0.0.1
	if !address.IsValid() ||
		address.IsLoopback() ||
		address.IsPrivate() ||
		address.IsLinkLocalUnicast() || // includes 169.254.169.254, the metadata address
		address.IsLinkLocalMulticast() ||
		address.IsInterfaceLocalMulticast() ||
		address.IsMulticast() ||
		address.IsUnspecified() {
		return fmt.Errorf("%w: %s", ErrAddressNotPublic, address)
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(address) {
			return fmt.Errorf("%w: %s", ErrAddressNotPublic, address)
		}
	}
	return nil
}

// refuseNonPublicDialAddress is a net.Dialer Control hook: it runs
// after resolution, on the exact address about to be dialled.
func refuseNonPublicDialAddress(network, hostAndPort string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(hostAndPort)
	if err != nil {
		return err
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%w: unparseable dial address %q", ErrAddressNotPublic, host)
	}
	return publicAddressOrRefusal(address)
}

// WebPageFetcherOption configures a WebPageFetcher.
type WebPageFetcherOption func(*webPageFetcherSettings)

type webPageFetcherSettings struct {
	allowNonPublicAddresses bool
	dialEveryHostAt         string
}

// DialEveryHostAtForTests sends every connection to hostAndPort, a test
// server, whatever host the URL names, and switches the address guard
// off. It lets a test give a page a public-looking URL (which the
// browser renderers' pre-check accepts) and still serve it locally.
func DialEveryHostAtForTests(hostAndPort string) WebPageFetcherOption {
	return func(settings *webPageFetcherSettings) {
		settings.allowNonPublicAddresses = true
		settings.dialEveryHostAt = hostAndPort
	}
}

// AllowNonPublicAddressesForTests switches the address guard off. Test
// servers listen on loopback; nothing else should ever pass this.
func AllowNonPublicAddressesForTests() WebPageFetcherOption {
	return func(settings *webPageFetcherSettings) {
		settings.allowNonPublicAddresses = true
	}
}

// WebPageFetcher fetches public web pages; see the file comment.
type WebPageFetcher struct {
	http                    *http.Client
	userAgent               string
	allowNonPublicAddresses bool
}

// NewWebPageFetcher builds a fetcher whose every connection is checked
// against the public-address guard.
func NewWebPageFetcher(client *Client, opts ...WebPageFetcherOption) *WebPageFetcher {
	ensureRateLimit("web_page", webPageFetchRPS, webPageFetchBurst)
	var settings webPageFetcherSettings
	for _, opt := range opts {
		opt(&settings)
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if !settings.allowNonPublicAddresses {
		dialer.Control = refuseNonPublicDialAddress
	}
	dialContext := dialer.DialContext
	if settings.dialEveryHostAt != "" {
		dialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, settings.dialEveryHostAt)
		}
	}
	transport := &http.Transport{
		// No proxy from the environment: a proxy would be dialled in
		// place of the page's host, and the guard would judge the proxy.
		Proxy:                 nil,
		DialContext:           dialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
	}

	return &WebPageFetcher{
		http: &http.Client{
			Timeout:   webPageFetchTimeout,
			Transport: transport,
			CheckRedirect: func(next *http.Request, previous []*http.Request) error {
				if len(previous) >= webPageMaxRedirects {
					return fmt.Errorf("stopped after %d redirects", webPageMaxRedirects)
				}
				return urlOrRefusal(next.URL, settings.allowNonPublicAddresses)
			},
		},
		userAgent:               client.UserAgent(),
		allowNonPublicAddresses: settings.allowNonPublicAddresses,
	}
}

func urlOrRefusal(pageURL *url.URL, allowNonPublicAddresses bool) error {
	if allowNonPublicAddresses {
		if pageURL.Scheme != "http" && pageURL.Scheme != "https" {
			return fmt.Errorf("only http and https URLs can be fetched, not %q", pageURL.Scheme)
		}
		return nil
	}
	return WebURLOrRefusal(pageURL)
}

// WebURLOrRefusal checks what can be checked of a URL before any DNS
// lookup: an http or https scheme, a host, no credentials, and no host
// that names a non-public place outright. The dial-time guard is the
// one that holds; this one gives the caller a clear error early, and
// is all the protection a URL handed to a third-party renderer gets.
func WebURLOrRefusal(pageURL *url.URL) error {
	if pageURL.Scheme != "http" && pageURL.Scheme != "https" {
		return fmt.Errorf("only http and https URLs can be fetched, not %q", pageURL.Scheme)
	}
	if pageURL.User != nil {
		return errors.New("URLs carrying a username or password are not fetched")
	}
	host := strings.TrimSuffix(strings.ToLower(pageURL.Hostname()), ".")
	if host == "" {
		return errors.New("the URL has no host")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") ||
		!strings.Contains(host, ".") && net.ParseIP(host) == nil {
		return fmt.Errorf("%w: %s", ErrAddressNotPublic, host)
	}
	if address, err := netip.ParseAddr(host); err == nil {
		return publicAddressOrRefusal(address)
	}
	return nil
}

// Fetch GETs pageURL and returns the page as served. A non-2xx status
// is an error carrying the status; a body over the size cap is refused
// rather than truncated, since a cut HTML document converts badly.
// Successful fetches are cached (when the server has the cache on) so
// that paging through a long page with start_index fetches it once.
func (f *WebPageFetcher) Fetch(ctx context.Context, pageURL string) (*WebPage, error) {
	parsedURL, err := url.Parse(pageURL)
	if err != nil {
		return nil, fmt.Errorf("not a URL: %w", err)
	}
	if err := urlOrRefusal(parsedURL, f.allowNonPublicAddresses); err != nil {
		return nil, err
	}

	key := "web_page\x00" + parsedURL.String()
	if cache != nil {
		if cached, ok := cache.get(key); ok {
			var page WebPage
			if json.Unmarshal(cached, &page) == nil {
				page.FromCache = true
				return &page, nil
			}
		}
	}

	if limiter := getRateLimiter("web_page"); limiter != nil {
		if err := limiter.Wait(ctx); err != nil {
			return nil, err
		}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	request.Header.Set("User-Agent", f.userAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,application/json;q=0.8,*/*;q=0.5")

	response, err := f.http.Do(request)
	if err != nil {
		if errors.Is(err, ErrAddressNotPublic) {
			return nil, ErrAddressNotPublic
		}
		return nil, fmt.Errorf("fetching %s: %w", parsedURL.Redacted(), err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, webPageMaxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", parsedURL.Redacted(), err)
	}
	if len(body) > webPageMaxBodyBytes {
		return nil, fmt.Errorf("the page is larger than %d MB; it is not fetched", webPageMaxBodyBytes>>20)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &HTTPError{StatusCode: response.StatusCode, Body: firstCharacters(string(body), 300)}
	}

	mediaType, params, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	page := &WebPage{
		RequestedURL: parsedURL.String(),
		FinalURL:     response.Request.URL.String(),
		StatusCode:   response.StatusCode,
		ContentType:  mediaType,
		Charset:      params["charset"],
		Body:         body,
	}
	if cache != nil {
		if encoded, err := json.Marshal(page); err == nil {
			cache.put(key, encoded)
		}
	}
	return page, nil
}

func firstCharacters(text string, count int) string {
	runes := []rune(text)
	if len(runes) <= count {
		return text
	}
	return string(runes[:count]) + "…"
}
