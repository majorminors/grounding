package backends

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCrawl4AIRenderSendsTheBareRequestAndReadsTheHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/crawl" || r.Header.Get("Authorization") != "Bearer c4a-token" {
			t.Errorf("unexpected request %s %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var body struct {
			URLs          []string `json:"urls"`
			CrawlerConfig struct {
				Params map[string]any `json:"params"`
			} `json:"crawler_config"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if len(body.URLs) != 1 || body.URLs[0] != "https://spa.example/page" {
			t.Errorf("unexpected urls %v", body.URLs)
		}
		// The bare render: crawl4ai's page-interaction features did worse
		// in the bake-off, so none is asked for.
		if len(body.CrawlerConfig.Params) != 1 || body.CrawlerConfig.Params["cache_mode"] != "bypass" {
			t.Errorf("want only cache_mode bypass, got %v", body.CrawlerConfig.Params)
		}
		w.Write([]byte(`{"success":true,"results":[{"success":true,"status_code":200,"html":"<html><body><h1>Rendered</h1></body></html>","redirected_url":"https://spa.example/page/"}]}`))
	}))
	defer server.Close()

	SetRateLimit("crawl4ai", 1000, 10)
	page, err := NewCrawl4AI(NewClient(), server.URL+"/", "c4a-token").ResolvingEveryHostToForTests(netip.MustParseAddr("93.184.215.14")).Render(context.Background(), "https://spa.example/page")
	if err != nil {
		t.Fatal(err)
	}
	if page.HTML != "<html><body><h1>Rendered</h1></body></html>" || page.FinalURL != "https://spa.example/page/" {
		t.Errorf("unexpected page %+v", page)
	}
}

func TestCrawl4AIRenderReportsTheSitesStatusAsTheSites(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results":[{"success":false,"status_code":403,"error_message":"Blocked by anti-bot protection","html":""}]}`))
	}))
	defer server.Close()
	SetRateLimit("crawl4ai", 1000, 10)
	_, err := NewCrawl4AI(NewClient(), server.URL, "").ResolvingEveryHostToForTests(netip.MustParseAddr("93.184.215.14")).Render(context.Background(), "https://blocked.example/")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 403 {
		t.Errorf("want the site's HTTP 403, got %v", err)
	}
}

func TestCrawl4AIServersOwnFailureIsNotTheSites(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // a wrong token
	}))
	defer server.Close()
	SetRateLimit("crawl4ai", 1000, 10)
	_, err := NewCrawl4AI(NewClient(), server.URL, "wrong").ResolvingEveryHostToForTests(netip.MustParseAddr("93.184.215.14")).Render(context.Background(), "https://a.example/")
	var httpErr *HTTPError
	if err == nil || errors.As(err, &httpErr) || !strings.Contains(err.Error(), "crawl4ai server failed") {
		t.Errorf("want the server's failure, not unwrappable as the site's status, got %v", err)
	}
}

func TestCrawl4AIRefusesNonPublicURLBeforeSendingIt(t *testing.T) {
	_, err := NewCrawl4AI(NewClient(), "http://127.0.0.1:1", "").Render(context.Background(), "http://100.64.0.2:8384/")
	if !errors.Is(err, ErrAddressNotPublic) {
		t.Errorf("want ErrAddressNotPublic, got %v", err)
	}
}

// A public name pointed at an internal address would aim the browser's
// host at its own services.
func TestCrawl4AIRefusesAHostThatResolvesToAnInternalAddress(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer server.Close()
	for _, internal := range []string{"127.0.0.1", "100.64.0.1", "10.88.0.1", "::1"} {
		_, err := NewCrawl4AI(NewClient(), server.URL, "").ResolvingEveryHostToForTests(netip.MustParseAddr(internal)).
			Render(context.Background(), "https://rebind.example.com/")
		if !errors.Is(err, ErrAddressNotPublic) {
			t.Errorf("%s: want ErrAddressNotPublic, got %v", internal, err)
		}
	}
	if called {
		t.Error("crawl4ai was sent a URL whose host resolves internally")
	}
}

// The render service may listen on a Unix socket, and grounding reaches it by a unix:// base URL.
func TestCrawl4AIReachesTheRenderServiceOnAUnixSocket(t *testing.T) {
	// Not t.TempDir(): its long name passes macOS's 104-byte socket path limit.
	directory, err := os.MkdirTemp("", "c4a")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	socketPath := filepath.Join(directory, "render.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.Write([]byte(`{"success":true,"results":[{"success":true,"status_code":200,"html":"<html><body><p>via the socket</p></body></html>","redirected_url":"https://a.example/"}]}`))
	})}
	go server.Serve(listener)
	defer server.Close()

	SetRateLimit("crawl4ai", 1000, 10)
	crawl4ai := NewCrawl4AI(NewClient(), "unix://"+socketPath, "").ResolvingEveryHostToForTests(netip.MustParseAddr("93.184.215.14"))
	page, err := crawl4ai.Render(context.Background(), "https://a.example/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page.HTML, "via the socket") {
		t.Errorf("unexpected page %+v", page)
	}
	if health := crawl4ai.HealthCheck(context.Background()); !health.Healthy {
		t.Errorf("health over the socket: %+v", health)
	}
}
