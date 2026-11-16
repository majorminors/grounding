package backends

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// newTestOpenCitations builds a backend pointed at a test server and
// lifts the 3 RPS rate limit so the suite isn't throttled. The limiter
// and the 429 cooldown are package-level singletons keyed by backend
// name, so an earlier NewOpenCitations call or a 429 in another test
// in this process would otherwise leak into this one.
func newTestOpenCitations(server *httptest.Server, accessToken string) *OpenCitationsBackend {
	SetRateLimit("opencitations", 1000, 100)
	cooldowns.Delete("opencitations")
	return &OpenCitationsBackend{
		client:      NewClient(),
		baseURL:     server.URL,
		accessToken: accessToken,
	}
}

// openCitationsEdgeJSON renders one edge row in the live API's shape.
func openCitationsEdgeJSON(citing string, cited string) string {
	return `{"oci":"1-2","citing":"` + citing + `","cited":"` + cited + `","creation":"2013-07-31","timespan":"P7Y8M13D","journal_sc":"no","author_sc":"no"}`
}

func TestOpenCitations_Name(t *testing.T) {
	backend := NewOpenCitations(NewClient(), "")
	if backend.Name() != "opencitations" {
		t.Errorf("Expected name 'opencitations', got '%s'", backend.Name())
	}
}

func TestOpenCitations_ConstructorTrimsToken(t *testing.T) {
	backend := NewOpenCitations(NewClient(), "  abc123  ")
	if backend.accessToken != "abc123" {
		t.Errorf("Expected trimmed token 'abc123', got %q", backend.accessToken)
	}
}

func TestExtractDOIFromIdentifierList(t *testing.T) {
	tests := []struct {
		name        string
		identifiers string
		expected    string
	}{
		{
			name:        "doi among omid, openalex and pmid",
			identifiers: "omid:br/06120344846 doi:10.1038/nature12373 openalex:W2159974629 pmid:23903748",
			expected:    "10.1038/nature12373",
		},
		{
			name:        "doi is lower-cased",
			identifiers: "omid:br/1 doi:10.1002/(SICI)1097-0258(19980430)17:8<857::AID-SIM777>3.0.CO;2-E",
			expected:    "10.1002/(sici)1097-0258(19980430)17:8<857::aid-sim777>3.0.co;2-e",
		},
		{
			name:        "upper-case scheme still matches",
			identifiers: "omid:br/1 DOI:10.1000/ABC",
			expected:    "10.1000/abc",
		},
		{
			name:        "first of two dois wins",
			identifiers: "omid:br/06101802023 doi:10.7717/peerj-cs.163 pmid:33816816 doi:10.7287/peerj.preprints.26727v1",
			expected:    "10.7717/peerj-cs.163",
		},
		{
			name:        "trailing separator trimmed",
			identifiers: "omid:br/1 doi:10.1000/xyz; pmid:5",
			expected:    "10.1000/xyz",
		},
		{
			name:        "no doi present",
			identifiers: "omid:br/06120344846 pmid:23903748",
			expected:    "",
		},
		{
			name:        "empty string",
			identifiers: "",
			expected:    "",
		},
		{
			name:        "bare scheme with no value is skipped",
			identifiers: "doi: omid:br/1",
			expected:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDOIFromIdentifierList(tt.identifiers)
			if got != tt.expected {
				t.Errorf("extractDOIFromIdentifierList(%q) = %q, expected %q", tt.identifiers, got, tt.expected)
			}
		})
	}
}

func TestOpenCitations_FetchReferenceDOIs(t *testing.T) {
	var gotPath string
	var gotAuthorization string
	var gotHasAuthorization bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization, gotHasAuthorization = headerLookup(r.Header, "authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[" + strings.Join([]string{
			openCitationsEdgeJSON("omid:br/1 doi:10.1038/nature12373", "omid:br/2 doi:10.1038/nmeth818 pmid:16299478"),
			openCitationsEdgeJSON("omid:br/1 doi:10.1038/nature12373", "omid:br/3 doi:10.1146/ANNUREV.MATSCI.29.1.505"),
			openCitationsEdgeJSON("omid:br/1 doi:10.1038/nature12373", "omid:br/4 pmid:99999"),           // no DOI: skipped
			openCitationsEdgeJSON("omid:br/1 doi:10.1038/nature12373", "omid:br/2 doi:10.1038/nmeth818"), // duplicate
			openCitationsEdgeJSON("omid:br/1 doi:10.1038/nature12373", "omid:br/5 doi:10.1021/ja00001a001"),
		}, ",") + "]"))
	}))
	defer server.Close()

	backend := newTestOpenCitations(server, "")
	dois, err := backend.FetchReferenceDOIs(context.Background(), "https://doi.org/10.1038/Nature12373", 0)
	if err != nil {
		t.Fatalf("FetchReferenceDOIs: %v", err)
	}

	if gotPath != "/references/doi:10.1038/nature12373" {
		t.Errorf("Expected path '/references/doi:10.1038/nature12373', got %q", gotPath)
	}
	if gotHasAuthorization {
		t.Errorf("Expected no authorization header without a token, got %q", gotAuthorization)
	}

	expected := []string{"10.1038/nmeth818", "10.1146/annurev.matsci.29.1.505", "10.1021/ja00001a001"}
	if !reflect.DeepEqual(dois, expected) {
		t.Errorf("Expected %v, got %v", expected, dois)
	}
}

func TestOpenCitations_FetchCitationDOIs(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[" + strings.Join([]string{
			openCitationsEdgeJSON("omid:br/10 doi:10.1063/1.5011231 openalex:W3100592981", "omid:br/1 doi:10.1038/nature12373"),
			openCitationsEdgeJSON("omid:br/11 doi:10.1021/acscentsci.2c00976", "omid:br/1 doi:10.1038/nature12373"),
			openCitationsEdgeJSON("omid:br/12 omid-only-entity", "omid:br/1 doi:10.1038/nature12373"),
			openCitationsEdgeJSON("omid:br/10 doi:10.1063/1.5011231", "omid:br/1 doi:10.1038/nature12373"),
		}, ",") + "]"))
	}))
	defer server.Close()

	backend := newTestOpenCitations(server, "")
	dois, err := backend.FetchCitationDOIs(context.Background(), "10.1038/nature12373", 0)
	if err != nil {
		t.Fatalf("FetchCitationDOIs: %v", err)
	}

	if gotPath != "/citations/doi:10.1038/nature12373" {
		t.Errorf("Expected path '/citations/doi:10.1038/nature12373', got %q", gotPath)
	}

	// The citing field is the one that matters here; the cited field is
	// the seed on every row and must not leak into the result.
	expected := []string{"10.1063/1.5011231", "10.1021/acscentsci.2c00976"}
	if !reflect.DeepEqual(dois, expected) {
		t.Errorf("Expected %v, got %v", expected, dois)
	}
}

func TestOpenCitations_LimitCapsAfterDedup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[" + strings.Join([]string{
			openCitationsEdgeJSON("omid:br/1 doi:10.1000/seed", "omid:br/2 doi:10.1000/a"),
			openCitationsEdgeJSON("omid:br/1 doi:10.1000/seed", "omid:br/2 doi:10.1000/a"), // duplicate must not consume the cap
			openCitationsEdgeJSON("omid:br/1 doi:10.1000/seed", "omid:br/3 doi:10.1000/b"),
			openCitationsEdgeJSON("omid:br/1 doi:10.1000/seed", "omid:br/4 doi:10.1000/c"),
			openCitationsEdgeJSON("omid:br/1 doi:10.1000/seed", "omid:br/5 doi:10.1000/d"),
		}, ",") + "]"))
	}))
	defer server.Close()

	backend := newTestOpenCitations(server, "")
	ctx := context.Background()

	tests := []struct {
		name     string
		limit    int
		expected []string
	}{
		{"limit 2 keeps first two distinct", 2, []string{"10.1000/a", "10.1000/b"}},
		{"limit equal to distinct count", 4, []string{"10.1000/a", "10.1000/b", "10.1000/c", "10.1000/d"}},
		{"limit above distinct count", 10, []string{"10.1000/a", "10.1000/b", "10.1000/c", "10.1000/d"}},
		{"limit 0 means no cap", 0, []string{"10.1000/a", "10.1000/b", "10.1000/c", "10.1000/d"}},
		{"negative limit means no cap", -1, []string{"10.1000/a", "10.1000/b", "10.1000/c", "10.1000/d"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dois, err := backend.FetchReferenceDOIs(ctx, "10.1000/seed", tt.limit)
			if err != nil {
				t.Fatalf("FetchReferenceDOIs: %v", err)
			}
			if !reflect.DeepEqual(dois, tt.expected) {
				t.Errorf("limit %d: expected %v, got %v", tt.limit, tt.expected, dois)
			}
		})
	}
}

func TestOpenCitations_EmptyResultIsEmptySlice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))
	defer server.Close()

	backend := newTestOpenCitations(server, "")
	ctx := context.Background()

	references, err := backend.FetchReferenceDOIs(ctx, "10.1000/does-not-exist", 10)
	if err != nil {
		t.Fatalf("FetchReferenceDOIs on empty result: unexpected error %v", err)
	}
	if references == nil {
		t.Error("Expected non-nil empty slice from FetchReferenceDOIs, got nil")
	}
	if len(references) != 0 {
		t.Errorf("Expected 0 references, got %d", len(references))
	}

	citations, err := backend.FetchCitationDOIs(ctx, "10.1000/does-not-exist", 10)
	if err != nil {
		t.Fatalf("FetchCitationDOIs on empty result: unexpected error %v", err)
	}
	if citations == nil {
		t.Error("Expected non-nil empty slice from FetchCitationDOIs, got nil")
	}
	if len(citations) != 0 {
		t.Errorf("Expected 0 citations, got %d", len(citations))
	}
}

func TestOpenCitations_CitationCount(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		expectedCount int
		expectError   bool
	}{
		{"count as string (live shape)", `[{"count":"1806"}]`, 1806, false},
		{"count as bare number", `[{"count":42}]`, 42, false},
		{"zero for unknown doi", `[{"count":"0"}]`, 0, false},
		{"empty array is an error", `[]`, 0, true},
		{"non-numeric count is an error", `[{"count":"lots"}]`, 0, true},
		{"malformed json is an error", `{"count":"1"}`, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(tt.body))
			}))
			defer server.Close()

			backend := newTestOpenCitations(server, "")
			count, err := backend.CitationCount(context.Background(), "10.1038/nature12373")

			if gotPath != "/citation-count/doi:10.1038/nature12373" {
				t.Errorf("Expected path '/citation-count/doi:10.1038/nature12373', got %q", gotPath)
			}
			if tt.expectError {
				if err == nil {
					t.Fatalf("Expected error for body %s, got count %d", tt.body, count)
				}
				return
			}
			if err != nil {
				t.Fatalf("CitationCount: %v", err)
			}
			if count != tt.expectedCount {
				t.Errorf("Expected count %d, got %d", tt.expectedCount, count)
			}
		})
	}
}

func TestOpenCitations_AccessTokenHeader(t *testing.T) {
	tests := []struct {
		name           string
		token          string
		expectPresent  bool
		expectedHeader string
	}{
		{"token sent bare in authorization header", "my-secret-token", true, "my-secret-token"},
		{"no header when token empty", "", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotValue string
			var gotPresent bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotValue, gotPresent = headerLookup(r.Header, "authorization")
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`[{"count":"5"}]`))
			}))
			defer server.Close()

			backend := newTestOpenCitations(server, tt.token)
			if _, err := backend.CitationCount(context.Background(), "10.1000/xyz"); err != nil {
				t.Fatalf("CitationCount: %v", err)
			}

			if gotPresent != tt.expectPresent {
				t.Fatalf("Expected authorization header present=%v, got present=%v (value %q)", tt.expectPresent, gotPresent, gotValue)
			}
			if tt.expectPresent && gotValue != tt.expectedHeader {
				t.Errorf("Expected authorization header %q, got %q", tt.expectedHeader, gotValue)
			}
			if tt.expectPresent && strings.HasPrefix(strings.ToLower(gotValue), "bearer ") {
				t.Errorf("Token must be sent bare, not as a Bearer scheme: %q", gotValue)
			}
		})
	}
}

func TestOpenCitations_HTTPError(t *testing.T) {
	tests := []struct {
		name           string
		status         int
		body           string
		expectedStatus int
	}{
		{"403 invalid token (live behaviour)", http.StatusForbidden, "Invalid token. Remove the authorization header or register a new token at https://opencitations.net/accesstoken", http.StatusForbidden},
		{"500 server error", http.StatusInternalServerError, "boom", http.StatusInternalServerError},
		{"429 rate limited", http.StatusTooManyRequests, "slow down", http.StatusTooManyRequests},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer server.Close()

			backend := newTestOpenCitations(server, "bad-token")
			ctx := context.Background()

			// A 429 exhausts the client's retry budget and benches the
			// backend in a package-global cooldown; clear it so later
			// tests aren't rejected before they reach the server.
			defer cooldowns.Delete("opencitations")

			dois, err := backend.FetchReferenceDOIs(ctx, "10.1038/nature12373", 5)
			if err == nil {
				t.Fatalf("Expected error on HTTP %d, got dois %v", tt.status, dois)
			}
			if dois != nil {
				t.Errorf("Expected nil slice on error, got %v", dois)
			}
			var httpErr *HTTPError
			if !errors.As(err, &httpErr) {
				t.Fatalf("Expected *HTTPError in chain, got %T: %v", err, err)
			}
			if httpErr.StatusCode != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, httpErr.StatusCode)
			}
			if !strings.Contains(err.Error(), tt.body) {
				t.Errorf("Expected error to carry the response body %q, got %q", tt.body, err.Error())
			}
			if tt.status == http.StatusTooManyRequests && !IsRateLimited(err) {
				t.Error("Expected IsRateLimited to recognise a 429 through the wrapped error")
			}

			if _, err := backend.FetchCitationDOIs(ctx, "10.1038/nature12373", 5); err == nil {
				t.Error("Expected FetchCitationDOIs to fail on HTTP error")
			}
			if _, err := backend.CitationCount(ctx, "10.1038/nature12373"); err == nil {
				t.Error("Expected CitationCount to fail on HTTP error")
			}
		})
	}
}

func TestOpenCitations_HealthCheck(t *testing.T) {
	tests := []struct {
		name            string
		status          int
		body            string
		expectHealthy   bool
		expectedMessage string
	}{
		{"healthy on non-zero count", http.StatusOK, `[{"count":"1806"}]`, true, "reachable"},
		{"unhealthy on zero count", http.StatusOK, `[{"count":"0"}]`, false, "zero citations"},
		{"unhealthy on http error", http.StatusForbidden, "Invalid token", false, "Invalid token"},
		{"unhealthy on malformed body", http.StatusOK, "not json", false, "API error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer server.Close()

			backend := newTestOpenCitations(server, "")
			status := backend.HealthCheck(context.Background())

			if gotPath != "/citation-count/doi:"+openCitationsHealthCheckDOI {
				t.Errorf("Expected health check path for %s, got %q", openCitationsHealthCheckDOI, gotPath)
			}
			if status.Healthy != tt.expectHealthy {
				t.Errorf("Expected healthy=%v, got healthy=%v (message %q)", tt.expectHealthy, status.Healthy, status.Message)
			}
			if !strings.Contains(status.Message, tt.expectedMessage) {
				t.Errorf("Expected message containing %q, got %q", tt.expectedMessage, status.Message)
			}
		})
	}
}

func TestOpenCitations_DOIPathEncoding(t *testing.T) {
	tests := []struct {
		name         string
		inputDOI     string
		expectedPath string // decoded path as the server sees it
		expectRawURI string // substring that must appear in the wire-level request URI
	}{
		{
			name:         "doi.org url prefix stripped and lower-cased",
			inputDOI:     "https://doi.org/10.1038/NATURE12373",
			expectedPath: "/citation-count/doi:10.1038/nature12373",
			expectRawURI: "/citation-count/doi:10.1038/nature12373",
		},
		{
			name:         "doi: scheme prefix stripped, not doubled",
			inputDOI:     "doi:10.1038/nature12373",
			expectedPath: "/citation-count/doi:10.1038/nature12373",
			expectRawURI: "/citation-count/doi:10.1038/nature12373",
		},
		{
			name:         "sici doi with angle brackets and semicolon is escaped, slash kept raw",
			inputDOI:     "10.1002/(SICI)1097-0258(19980430)17:8<857::AID-SIM777>3.0.CO;2-E",
			expectedPath: "/citation-count/doi:10.1002/(sici)1097-0258(19980430)17:8<857::aid-sim777>3.0.co;2-e",
			expectRawURI: "/citation-count/doi:10.1002/%28sici%291097-0258%2819980430%2917:8%3C857::aid-sim777%3E3.0.co%3B2-e",
		},
		{
			name:         "question mark and hash do not split the url",
			inputDOI:     "10.1000/what?is#this",
			expectedPath: "/citation-count/doi:10.1000/what?is#this",
			expectRawURI: "/citation-count/doi:10.1000/what%3Fis%23this",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath, gotRawQuery, gotRequestURI string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotRawQuery = r.URL.RawQuery
				gotRequestURI = r.RequestURI
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`[{"count":"1"}]`))
			}))
			defer server.Close()

			backend := newTestOpenCitations(server, "")
			if _, err := backend.CitationCount(context.Background(), tt.inputDOI); err != nil {
				t.Fatalf("CitationCount: %v", err)
			}

			if gotPath != tt.expectedPath {
				t.Errorf("Expected decoded path %q, got %q", tt.expectedPath, gotPath)
			}
			if gotRawQuery != "" {
				t.Errorf("Expected no query string, got %q (a ? in the DOI leaked into URL syntax)", gotRawQuery)
			}
			if !strings.Contains(gotRequestURI, tt.expectRawURI) {
				t.Errorf("Expected wire URI containing %q, got %q", tt.expectRawURI, gotRequestURI)
			}
		})
	}
}

// headerLookup reports whether a header is present at all (as opposed
// to present-but-empty), which http.Header.Get cannot distinguish.
func headerLookup(header http.Header, name string) (string, bool) {
	values, ok := header[http.CanonicalHeaderKey(name)]
	if !ok || len(values) == 0 {
		return "", false
	}
	return values[0], true
}
