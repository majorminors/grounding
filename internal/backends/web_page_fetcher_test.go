package backends

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestPublicAddressOrRefusal(t *testing.T) {
	refused := []string{
		"127.0.0.1", "::1", "10.0.0.7", "172.16.4.4", "192.168.1.1",
		"100.64.0.2",      // the NetBird mesh
		"100.100.100.200", // a cloud metadata address inside carrier-grade NAT
		"169.254.169.254", // the common cloud metadata address
		"0.0.0.0", "::", "224.0.0.1", "fe80::1", "fd00::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", // IPv4-mapped
		"64:ff9b::a00:1", // NAT64 of 10.0.0.1
		"255.255.255.255",
	}
	for _, text := range refused {
		if err := publicAddressOrRefusal(netip.MustParseAddr(text)); !errors.Is(err, ErrAddressNotPublic) {
			t.Errorf("%s should be refused, got %v", text, err)
		}
	}
	allowed := []string{"93.184.216.34", "1.1.1.1", "2606:4700:4700::1111", "152.53.83.8"}
	for _, text := range allowed {
		if err := publicAddressOrRefusal(netip.MustParseAddr(text)); err != nil {
			t.Errorf("%s should be allowed, got %v", text, err)
		}
	}
}

func TestWebURLOrRefusal(t *testing.T) {
	refused := []string{
		"file:///etc/passwd", "ftp://example.org/", "gopher://example.org/",
		"http://localhost:7700/", "http://LOCALHOST./", "http://api.localhost/",
		"http://printer.local/", "http://metadata.google.internal/",
		"http://homeserver:8384/", // a bare mesh hostname
		"http://127.0.0.1/", "http://[::1]/", "http://100.64.0.1/",
		"http://user:secret@example.org/", "http:///nohost",
	}
	for _, text := range refused {
		parsed, err := url.Parse(text)
		if err != nil {
			continue
		}
		if WebURLOrRefusal(parsed) == nil {
			t.Errorf("%s should be refused", text)
		}
	}
	for _, text := range []string{"https://example.org/a?b=c", "http://93.184.216.34/"} {
		parsed, _ := url.Parse(text)
		if err := WebURLOrRefusal(parsed); err != nil {
			t.Errorf("%s should be allowed, got %v", text, err)
		}
	}
}

// The dial-time guard is the one that holds against redirects and DNS
// rebinding, so it is tested on its own: the request goes straight to
// the guarded transport, skipping the URL pre-check.
func TestGuardedTransportRefusesToDialLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret"))
	}))
	defer server.Close()

	fetcher := NewWebPageFetcher(NewClient())
	response, err := fetcher.http.Get(server.URL)
	if err == nil {
		response.Body.Close()
		t.Fatal("the guarded transport dialled a loopback test server")
	}
	if !errors.Is(err, ErrAddressNotPublic) {
		t.Errorf("want ErrAddressNotPublic, got %v", err)
	}
}

func TestFetchRefusesLoopbackURL(t *testing.T) {
	fetcher := NewWebPageFetcher(NewClient())
	if _, err := fetcher.Fetch(context.Background(), "http://127.0.0.1:1/"); !errors.Is(err, ErrAddressNotPublic) {
		t.Errorf("want ErrAddressNotPublic, got %v", err)
	}
}

func TestFetchReturnsPageWithFinalURLAfterRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusFound)
			return
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "grounding/") {
			t.Errorf("User-Agent not set: %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<p>hello</p>"))
	}))
	defer server.Close()

	fetcher := NewWebPageFetcher(NewClient(), AllowNonPublicAddressesForTests())
	page, err := fetcher.Fetch(context.Background(), server.URL+"/old")
	if err != nil {
		t.Fatal(err)
	}
	if page.FinalURL != server.URL+"/new" || page.ContentType != "text/html" || page.Charset != "utf-8" {
		t.Errorf("unexpected page: %+v", page)
	}
	if string(page.Body) != "<p>hello</p>" {
		t.Errorf("body %q", page.Body)
	}
}

func TestFetchStopsRedirectLoops(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
	}))
	defer server.Close()

	fetcher := NewWebPageFetcher(NewClient(), AllowNonPublicAddressesForTests())
	if _, err := fetcher.Fetch(context.Background(), server.URL+"/"); err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Errorf("want a redirect-limit error, got %v", err)
	}
}

func TestFetchRefusesRedirectToAnotherScheme(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
	}))
	defer server.Close()

	fetcher := NewWebPageFetcher(NewClient(), AllowNonPublicAddressesForTests())
	if _, err := fetcher.Fetch(context.Background(), server.URL+"/"); err == nil {
		t.Error("followed a redirect to file://")
	}
}

func TestFetchRefusesOversizedPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, webPageMaxBodyBytes+10))
	}))
	defer server.Close()

	fetcher := NewWebPageFetcher(NewClient(), AllowNonPublicAddressesForTests())
	if _, err := fetcher.Fetch(context.Background(), server.URL+"/"); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Errorf("want a size error, got %v", err)
	}
}

func TestFetchReportsHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer server.Close()

	fetcher := NewWebPageFetcher(NewClient(), AllowNonPublicAddressesForTests())
	_, err := fetcher.Fetch(context.Background(), server.URL+"/")
	if !IsNotFound(err) {
		t.Errorf("want a 404 HTTPError, got %v", err)
	}
}

func TestFetchUsesCacheWhenEnabled(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte("page"))
	}))
	defer server.Close()

	EnableResponseCache(0)
	defer DisableResponseCache()
	fetcher := NewWebPageFetcher(NewClient(), AllowNonPublicAddressesForTests())
	for range 3 {
		if _, err := fetcher.Fetch(context.Background(), server.URL+"/p"); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 {
		t.Errorf("paging a page should fetch it once; fetched %d times", requests)
	}
}
