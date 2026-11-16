package backends

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// unpaywallTestEmail is a syntactically plausible address for the
// ?email= parameter. Tests never reach the real API, so it only needs
// to be non-empty and recognisable in assertions.
const unpaywallTestEmail = "tester@grounding.test"

// newUnpaywallTestBackend wires an UnpaywallBackend to an httptest
// server. The caller owns the server and must Close it.
func newUnpaywallTestBackend(t *testing.T, handler http.HandlerFunc) (*UnpaywallBackend, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	// NewUnpaywall installs a 10 RPS / burst 1 limiter in the package
	// singleton, which would make every call after the first wait 100ms.
	// Override it so the suite stays fast; the limiter itself is
	// exercised in http_test.go.
	SetRateLimit("unpaywall", 1000, 10)
	backend := &UnpaywallBackend{
		client:  NewClient(),
		email:   unpaywallTestEmail,
		baseURL: server.URL,
	}
	return backend, server
}

// unpaywallOAResponse is the fixture for a gold OA article with both a
// PDF and a landing page, shaped like the live /v2/{doi} payload.
func unpaywallOAResponse() map[string]any {
	best := map[string]any{
		"url":                  "https://journals.plos.org/plosmedicine/article/file?id=10.1371/journal.pmed.0020124&type=printable",
		"url_for_pdf":          "https://journals.plos.org/plosmedicine/article/file?id=10.1371/journal.pmed.0020124&type=printable",
		"url_for_landing_page": "https://doi.org/10.1371/journal.pmed.0020124",
		"host_type":            "publisher",
		"version":              "publishedVersion",
		"license":              "cc-by",
		"is_best":              true,
	}
	return map[string]any{
		"doi":              "10.1371/journal.pmed.0020124",
		"doi_url":          "https://doi.org/10.1371/journal.pmed.0020124",
		"is_oa":            true,
		"oa_status":        "gold",
		"title":            "Why Most Published Research Findings Are False",
		"year":             2005,
		"journal_name":     "PLoS Medicine",
		"best_oa_location": best,
		"oa_locations":     []map[string]any{best},
	}
}

func TestUnpaywall_Name(t *testing.T) {
	backend := NewUnpaywall(NewClient(), unpaywallTestEmail)
	if backend.Name() != "unpaywall" {
		t.Errorf("Expected name 'unpaywall', got '%s'", backend.Name())
	}
}

func TestUnpaywall_FindOpenAccess_OAFound(t *testing.T) {
	backend, server := newUnpaywallTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/10.1371/journal.pmed.0020124" {
			t.Errorf("Expected path /v2/10.1371/journal.pmed.0020124, got %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("email"); got != unpaywallTestEmail {
			t.Errorf("Expected email=%s, got %q", unpaywallTestEmail, got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(unpaywallOAResponse())
	})
	defer server.Close()

	location, err := backend.FindOpenAccess(context.Background(), "10.1371/journal.pmed.0020124")
	if err != nil {
		t.Fatalf("FindOpenAccess failed: %v", err)
	}
	if location == nil {
		t.Fatal("Expected a location, got nil")
	}

	if !location.IsOA {
		t.Error("Expected IsOA true")
	}
	if location.OAStatus != "gold" {
		t.Errorf("Expected OAStatus 'gold', got '%s'", location.OAStatus)
	}
	if location.DOI != "10.1371/journal.pmed.0020124" {
		t.Errorf("Expected DOI '10.1371/journal.pmed.0020124', got '%s'", location.DOI)
	}
	if !strings.Contains(location.PDFURL, "type=printable") {
		t.Errorf("Expected PDFURL to be the printable PLOS link, got '%s'", location.PDFURL)
	}
	if location.LandingPageURL != "https://doi.org/10.1371/journal.pmed.0020124" {
		t.Errorf("Expected LandingPageURL to be the DOI link, got '%s'", location.LandingPageURL)
	}
	if location.URL != location.PDFURL {
		t.Errorf("Expected URL to prefer the PDF, got '%s'", location.URL)
	}
	if location.HostType != "publisher" {
		t.Errorf("Expected HostType 'publisher', got '%s'", location.HostType)
	}
	if location.Version != "publishedVersion" {
		t.Errorf("Expected Version 'publishedVersion', got '%s'", location.Version)
	}
	if location.License != "cc-by" {
		t.Errorf("Expected License 'cc-by', got '%s'", location.License)
	}
	if location.Title != "Why Most Published Research Findings Are False" {
		t.Errorf("Unexpected title '%s'", location.Title)
	}
	if location.Year != 2005 {
		t.Errorf("Expected Year 2005, got %d", location.Year)
	}
}

func TestUnpaywall_FindOpenAccess_KnownButNotOA(t *testing.T) {
	backend, server := newUnpaywallTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Nullable fields are sent as JSON null, exactly as the live API
		// does for a closed-access article.
		w.Write([]byte(`{
			"doi": "10.1038/nature12373",
			"is_oa": false,
			"oa_status": "closed",
			"title": "Nanometre-scale thermometry in a living cell",
			"year": 2013,
			"journal_name": "Nature",
			"best_oa_location": null,
			"oa_locations": []
		}`))
	})
	defer server.Close()

	location, err := backend.FindOpenAccess(context.Background(), "10.1038/nature12373")
	if err != nil {
		t.Fatalf("FindOpenAccess failed: %v", err)
	}
	if location == nil {
		t.Fatal("Expected a location for a known-but-closed DOI, got nil")
	}

	if location.IsOA {
		t.Error("Expected IsOA false")
	}
	if location.OAStatus != "closed" {
		t.Errorf("Expected OAStatus 'closed', got '%s'", location.OAStatus)
	}
	if location.URL != "" || location.PDFURL != "" || location.LandingPageURL != "" {
		t.Errorf("Expected no URLs for a closed DOI, got URL=%q PDFURL=%q LandingPageURL=%q",
			location.URL, location.PDFURL, location.LandingPageURL)
	}
	if location.HostType != "" || location.Version != "" || location.License != "" {
		t.Errorf("Expected no location metadata for a closed DOI, got HostType=%q Version=%q License=%q",
			location.HostType, location.Version, location.License)
	}
	// Bibliographic metadata should still come through so the caller can
	// confirm which record was matched.
	if location.Title != "Nanometre-scale thermometry in a living cell" {
		t.Errorf("Unexpected title '%s'", location.Title)
	}
	if location.Year != 2013 {
		t.Errorf("Expected Year 2013, got %d", location.Year)
	}
}

func TestUnpaywall_FindOpenAccess_NotFound(t *testing.T) {
	backend, server := newUnpaywallTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"HTTP_status_code": 404, "error": true, "message": "'10.9999/nope' is an invalid doi."}`))
	})
	defer server.Close()

	location, err := backend.FindOpenAccess(context.Background(), "10.9999/nope")
	if err != nil {
		t.Fatalf("Expected 404 to be swallowed as not-found, got error: %v", err)
	}
	if location != nil {
		t.Errorf("Expected nil location for an unknown DOI, got %+v", location)
	}
}

func TestUnpaywall_FindOpenAccess_MissingEmail(t *testing.T) {
	backend, server := newUnpaywallTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("Server should not be called when no email is configured")
	})
	defer server.Close()
	backend.email = ""

	location, err := backend.FindOpenAccess(context.Background(), "10.1371/journal.pmed.0020124")
	if err == nil {
		t.Fatal("Expected an error when email is empty, got nil")
	}
	if !errors.Is(err, ErrUnpaywallEmailRequired) {
		t.Errorf("Expected ErrUnpaywallEmailRequired, got %v", err)
	}
	if !strings.Contains(err.Error(), "GROUNDING_EMAIL") {
		t.Errorf("Expected error to name GROUNDING_EMAIL, got %q", err.Error())
	}
	if location != nil {
		t.Errorf("Expected nil location on configuration error, got %+v", location)
	}
}

func TestUnpaywall_FindOpenAccess_ServerError(t *testing.T) {
	backend, server := newUnpaywallTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("upstream exploded"))
	})
	defer server.Close()

	location, err := backend.FindOpenAccess(context.Background(), "10.1371/journal.pmed.0020124")
	if err == nil {
		t.Fatal("Expected an error on HTTP 500, got nil")
	}
	if location != nil {
		t.Errorf("Expected nil location on HTTP 500, got %+v", location)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("Expected error to carry the status code, got %q", err.Error())
	}
	if IsNotFound(err) {
		t.Error("A 500 must not be mistaken for not-found")
	}
}

func TestUnpaywall_FindOpenAccess_NormalisesDOI(t *testing.T) {
	inputs := []string{
		"https://doi.org/10.1038/NATURE12373",
		"http://dx.doi.org/10.1038/nature12373",
		"doi:10.1038/nature12373",
		"  10.1038/nature12373  ",
	}

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			var gotPath string
			backend, server := newUnpaywallTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"doi": "10.1038/nature12373", "is_oa": false, "oa_status": "closed", "best_oa_location": null}`))
			})
			defer server.Close()

			if _, err := backend.FindOpenAccess(context.Background(), input); err != nil {
				t.Fatalf("FindOpenAccess failed: %v", err)
			}
			if gotPath != "/v2/10.1038/nature12373" {
				t.Errorf("Expected request path /v2/10.1038/nature12373, got %s", gotPath)
			}
		})
	}
}

func TestUnpaywall_LookupURL_EscapesAwkwardDOIs(t *testing.T) {
	backend := &UnpaywallBackend{email: unpaywallTestEmail, baseURL: "https://api.example"}

	// SICI-style DOI with characters that are legal in a DOI but not in
	// a raw URL path. The slash must survive as a path separator.
	got := backend.lookupURL("10.1002/(sici)1097-0258(19980430)17:8<857::aid-sim777>3.0.co;2-e")
	if !strings.HasPrefix(got, "https://api.example/v2/10.1002/") {
		t.Errorf("Expected the DOI prefix to remain a path segment, got %s", got)
	}
	if strings.Contains(got, "<") || strings.Contains(got, ">") {
		t.Errorf("Expected angle brackets to be percent-encoded, got %s", got)
	}
	if strings.Contains(got, "%2F") {
		t.Errorf("Expected the DOI slash to stay a literal '/', got %s", got)
	}
	if !strings.HasSuffix(got, "?email=tester%40grounding.test") {
		t.Errorf("Expected the email query parameter, got %s", got)
	}
}

func TestUnpaywall_HealthCheck(t *testing.T) {
	t.Run("no email configured reports available", func(t *testing.T) {
		backend, server := newUnpaywallTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
			t.Error("Server should not be called when no email is configured")
		})
		defer server.Close()
		backend.email = ""

		status := backend.HealthCheck(context.Background())
		if !status.Healthy {
			t.Error("Expected Healthy true when only the email is missing")
		}
		if status.Message != "available (GROUNDING_EMAIL not set)" {
			t.Errorf("Unexpected message %q", status.Message)
		}
	})

	t.Run("reachable", func(t *testing.T) {
		backend, server := newUnpaywallTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v2/"+unpaywallHealthCheckDOI {
				t.Errorf("Expected health check to resolve %s, got path %s", unpaywallHealthCheckDOI, r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(unpaywallOAResponse())
		})
		defer server.Close()

		status := backend.HealthCheck(context.Background())
		if !status.Healthy {
			t.Errorf("Expected Healthy true, got message %q", status.Message)
		}
		if !strings.Contains(status.Message, "reachable") {
			t.Errorf("Expected message to say reachable, got %q", status.Message)
		}
	})

	t.Run("server error reports unhealthy", func(t *testing.T) {
		backend, server := newUnpaywallTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		})
		defer server.Close()

		status := backend.HealthCheck(context.Background())
		if status.Healthy {
			t.Error("Expected Healthy false on HTTP 502")
		}
		if !strings.Contains(status.Message, "502") {
			t.Errorf("Expected message to carry the status code, got %q", status.Message)
		}
	})

	t.Run("known DOI missing reports unhealthy", func(t *testing.T) {
		backend, server := newUnpaywallTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		defer server.Close()

		status := backend.HealthCheck(context.Background())
		if status.Healthy {
			t.Error("Expected Healthy false when the health-check DOI 404s")
		}
		if !strings.Contains(status.Message, unpaywallHealthCheckDOI) {
			t.Errorf("Expected message to name the DOI, got %q", status.Message)
		}
	})
}
