package backends

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Compile-time interface compliance checks
var (
	_ SearchBackend   = (*EuropePMCBackend)(nil)
	_ CitationBackend = (*EuropePMCBackend)(nil)
)

// europePMCSearchFixture mirrors the shape of a live resultType=core
// response (captured 2026-09-17), trimmed to the fields the backend
// reads. pubYear is a string, the abstract carries HTML headings, the
// open-access record lists doi/html/pdf full-text links, and the second
// record has no structured authorList or journalInfo.
const europePMCSearchFixture = `{
  "version": "6.9",
  "hitCount": 2,
  "request": {"queryString": "cognitive load theory", "resultType": "core", "pageSize": 2},
  "resultList": {"result": [
    {
      "id": "42725165", "source": "MED", "pmid": "42725165", "pmcid": "PMC13560642",
      "doi": "10.3389/fpsyg.2026.1921504",
      "title": "Cognitive load in online learning: a randomised trial.",
      "authorString": "Islam MA, Yang S, Abu Bakar YI.",
      "authorList": {"author": [
        {"fullName": "Islam MA", "lastName": "Islam", "firstName": "Mohammed A", "initials": "MA"},
        {"fullName": "Yang S", "lastName": "Yang", "firstName": "Suhui", "initials": "S"},
        {"fullName": "Abu Bakar YI", "lastName": "Abu Bakar", "firstName": "Yasrul Izad", "initials": "YI"}
      ]},
      "journalInfo": {"yearOfPublication": 2026, "journal": {"title": "Frontiers in psychology", "medlineAbbreviation": "Front Psychol"}},
      "pubYear": "2026",
      "abstractText": "<h4>Objectives</h4>Cognitive load theory (CLT) provides a framework.<h4>Findings</h4>Load was reduced.",
      "fullTextUrlList": {"fullTextUrl": [
        {"availability": "Subscription required", "availabilityCode": "S", "documentStyle": "doi", "site": "DOI", "url": "https://doi.org/10.3389/fpsyg.2026.1921504"},
        {"availability": "Open access", "availabilityCode": "OA", "documentStyle": "html", "site": "Europe_PMC", "url": "https://europepmc.org/articles/PMC13560642"},
        {"availability": "Open access", "availabilityCode": "OA", "documentStyle": "pdf", "site": "Europe_PMC", "url": "https://europepmc.org/articles/PMC13560642?pdf=render"}
      ]},
      "isOpenAccess": "Y", "inEPMC": "Y", "citedByCount": 12
    },
    {
      "id": "42210042", "source": "MED", "pmid": "42210042",
      "doi": "10.1002/ase.70266",
      "title": "Unload the load: Optimizing anatomy instruction with cognitive load theory.",
      "authorString": "Rizvi FA, Choudhury B, Hadie SNH.",
      "journalTitle": "Anat Sci Educ",
      "pubYear": "2025",
      "abstractText": "Anatomy is a demanding subject.",
      "fullTextUrlList": {"fullTextUrl": [
        {"availability": "Subscription required", "availabilityCode": "S", "documentStyle": "doi", "site": "DOI", "url": "https://doi.org/10.1002/ase.70266"}
      ]},
      "isOpenAccess": "N", "inEPMC": "N", "citedByCount": 3
    }
  ]}
}`

// europePMCEmptyFixture is the live shape of a search with no hits.
const europePMCEmptyFixture = `{"version":"6.9","hitCount":0,"request":{"queryString":"x","resultType":"core","pageSize":1},"resultList":{"result":[]}}`

// newEuropePMCTestBackend starts an httptest server driven by handler
// and returns a backend pointed at it. The server closes with the test.
func newEuropePMCTestBackend(t *testing.T, handler http.HandlerFunc) *EuropePMCBackend {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &EuropePMCBackend{
		client:  NewClient(),
		baseURL: server.URL,
	}
}

// europePMCFixedResponse returns a handler that appends each request's
// query parameters to requests and replies with status and body.
func europePMCFixedResponse(status int, body string, requests *[]url.Values) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if requests != nil {
			*requests = append(*requests, r.URL.Query())
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestEuropePMC_Name(t *testing.T) {
	backend := NewEuropePMC(NewClient())
	if backend.Name() != "europepmc" {
		t.Errorf("Expected name 'europepmc', got '%s'", backend.Name())
	}
	if backend.baseURL != europePMCBaseURL {
		t.Errorf("Expected default base URL %q, got %q", europePMCBaseURL, backend.baseURL)
	}
}

func TestEuropePMC_Search_MapsCoreResults(t *testing.T) {
	var requests []url.Values
	backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusOK, europePMCSearchFixture, &requests))

	papers, err := backend.Search(context.Background(), "cognitive load theory", 2, SearchFilters{})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(requests) != 1 {
		t.Fatalf("Expected 1 request, got %d", len(requests))
	}
	query := requests[0]
	if query.Get("query") != "cognitive load theory" {
		t.Errorf("Expected bare query without filters, got %q", query.Get("query"))
	}
	if query.Get("resultType") != "core" {
		t.Errorf("Expected resultType=core, got %q", query.Get("resultType"))
	}
	if query.Get("format") != "json" {
		t.Errorf("Expected format=json, got %q", query.Get("format"))
	}
	if query.Get("pageSize") != "2" {
		t.Errorf("Expected pageSize=2, got %q", query.Get("pageSize"))
	}

	if len(papers) != 2 {
		t.Fatalf("Expected 2 papers, got %d", len(papers))
	}

	openAccess := papers[0]
	if openAccess.Title != "Cognitive load in online learning: a randomised trial" {
		t.Errorf("Expected trailing full stop dropped from title, got %q", openAccess.Title)
	}
	if openAccess.Year != 2026 {
		t.Errorf("Expected string pubYear parsed to 2026, got %d", openAccess.Year)
	}
	wantAuthors := []string{"Islam MA", "Yang S", "Abu Bakar YI"}
	if strings.Join(openAccess.Authors, "|") != strings.Join(wantAuthors, "|") {
		t.Errorf("Expected authors %v from authorList, got %v", wantAuthors, openAccess.Authors)
	}
	if openAccess.DOI != "10.3389/fpsyg.2026.1921504" {
		t.Errorf("Expected DOI '10.3389/fpsyg.2026.1921504', got %q", openAccess.DOI)
	}
	wantAbstract := "Objectives Cognitive load theory (CLT) provides a framework. Findings Load was reduced."
	if openAccess.Abstract != wantAbstract {
		t.Errorf("Expected HTML stripped from abstract, got %q", openAccess.Abstract)
	}
	if openAccess.URL != "https://europepmc.org/articles/PMC13560642?pdf=render" {
		t.Errorf("Expected open-access PDF URL preferred, got %q", openAccess.URL)
	}
	if openAccess.Journal != "Frontiers in psychology" {
		t.Errorf("Expected journal from journalInfo.journal.title, got %q", openAccess.Journal)
	}
	if openAccess.Citations != 12 {
		t.Errorf("Expected 12 citations, got %d", openAccess.Citations)
	}
	if openAccess.Source != "europepmc" {
		t.Errorf("Expected source 'europepmc', got %q", openAccess.Source)
	}

	subscription := papers[1]
	wantFallbackAuthors := []string{"Rizvi FA", "Choudhury B", "Hadie SNH"}
	if strings.Join(subscription.Authors, "|") != strings.Join(wantFallbackAuthors, "|") {
		t.Errorf("Expected authors split from authorString without trailing full stop, got %v", subscription.Authors)
	}
	if subscription.Year != 2025 {
		t.Errorf("Expected year 2025, got %d", subscription.Year)
	}
	if subscription.URL != "https://europepmc.org/article/MED/42210042" {
		t.Errorf("Expected Europe PMC landing page when no open full text, got %q", subscription.URL)
	}
	if subscription.Journal != "Anat Sci Educ" {
		t.Errorf("Expected journal from journalTitle fallback, got %q", subscription.Journal)
	}
	if subscription.Abstract != "Anatomy is a demanding subject." {
		t.Errorf("Expected plain abstract untouched, got %q", subscription.Abstract)
	}
}

func TestEuropePMC_Search_LimitClamping(t *testing.T) {
	tests := []struct {
		name         string
		limit        int
		wantPageSize string
	}{
		{"zero uses default", 0, "10"},
		{"negative uses default", -3, "10"},
		{"within range passes through", 7, "7"},
		{"above maximum clamps to 25", 100, "25"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests []url.Values
			backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusOK, europePMCEmptyFixture, &requests))
			if _, err := backend.Search(context.Background(), "test", tt.limit, SearchFilters{}); err != nil {
				t.Fatalf("Search failed: %v", err)
			}
			if got := requests[0].Get("pageSize"); got != tt.wantPageSize {
				t.Errorf("limit %d: expected pageSize %q, got %q", tt.limit, tt.wantPageSize, got)
			}
		})
	}
}

func TestEuropePMCSearchQuery_Filters(t *testing.T) {
	tests := []struct {
		name    string
		filters SearchFilters
		want    string
	}{
		{"no filters leaves query bare", SearchFilters{}, "cognitive load"},
		{"year range", SearchFilters{YearMin: 2010, YearMax: 2020}, "(cognitive load) AND PUB_YEAR:[2010 TO 2020]"},
		{"year minimum only", SearchFilters{YearMin: 2010}, "(cognitive load) AND PUB_YEAR:[2010 TO *]"},
		{"year maximum only", SearchFilters{YearMax: 2020}, "(cognitive load) AND PUB_YEAR:[* TO 2020]"},
		{"citation floor", SearchFilters{MinCitations: 50}, "(cognitive load) AND CITED:[50 TO *]"},
		{"all together", SearchFilters{YearMin: 2010, YearMax: 2020, MinCitations: 50}, "(cognitive load) AND PUB_YEAR:[2010 TO 2020] AND CITED:[50 TO *]"},
		{"domain has no Europe PMC equivalent", SearchFilters{Domain: "Life Sciences"}, "cognitive load"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := europePMCSearchQuery("cognitive load", tt.filters)
			if got != tt.want {
				t.Errorf("europePMCSearchQuery() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEuropePMC_Search_SendsNativeFilters(t *testing.T) {
	var requests []url.Values
	backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusOK, europePMCSearchFixture, &requests))

	filters := SearchFilters{YearMin: 2010, YearMax: 2020, MinCitations: 50}
	if _, err := backend.Search(context.Background(), "cognitive load", 5, filters); err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	want := "(cognitive load) AND PUB_YEAR:[2010 TO 2020] AND CITED:[50 TO *]"
	if got := requests[0].Get("query"); got != want {
		t.Errorf("Expected native filter clauses in query, got %q", got)
	}
}

func TestEuropePMC_Search_HTTPError(t *testing.T) {
	backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusInternalServerError, `{"error":"boom"}`, nil))

	papers, err := backend.Search(context.Background(), "cognitive load", 5, SearchFilters{})
	if err == nil {
		t.Fatal("Expected error from 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "europepmc") {
		t.Errorf("Expected error wrapped with backend name, got %q", err.Error())
	}
	if papers != nil {
		t.Errorf("Expected nil papers on error, got %v", papers)
	}
}

func TestEuropePMC_Search_SkipsUntitledResults(t *testing.T) {
	body := `{"hitCount":2,"resultList":{"result":[{"id":"1","source":"MED","title":""},{"id":"2","source":"MED","title":"Kept."}]}}`
	backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusOK, body, nil))

	papers, err := backend.Search(context.Background(), "kept", 5, SearchFilters{})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(papers) != 1 || papers[0].Title != "Kept" {
		t.Errorf("Expected only the titled result, got %v", papers)
	}
}

func TestEuropePMCPaperURL(t *testing.T) {
	t.Run("prefers open-access PDF over HTML", func(t *testing.T) {
		result := europePMCResult{
			ID: "1", Source: "MED",
			FullTextURLList: &europePMCFullTextURLList{FullTextURL: []europePMCFullTextURL{
				{AvailabilityCode: "OA", DocumentStyle: "html", URL: "https://example.org/html"},
				{AvailabilityCode: "OA", DocumentStyle: "pdf", URL: "https://example.org/pdf"},
			}},
		}
		if got := europePMCPaperURL(result); got != "https://example.org/pdf" {
			t.Errorf("Expected PDF URL, got %q", got)
		}
	})

	t.Run("falls back to open-access HTML", func(t *testing.T) {
		result := europePMCResult{
			ID: "1", Source: "MED",
			FullTextURLList: &europePMCFullTextURLList{FullTextURL: []europePMCFullTextURL{
				{AvailabilityCode: "S", DocumentStyle: "pdf", URL: "https://example.org/paywalled.pdf"},
				{AvailabilityCode: "OA", DocumentStyle: "html", URL: "https://example.org/html"},
			}},
		}
		if got := europePMCPaperURL(result); got != "https://example.org/html" {
			t.Errorf("Expected open-access HTML over paywalled PDF, got %q", got)
		}
	})

	t.Run("free availability counts as open", func(t *testing.T) {
		result := europePMCResult{
			ID: "1", Source: "MED",
			FullTextURLList: &europePMCFullTextURLList{FullTextURL: []europePMCFullTextURL{
				{AvailabilityCode: "F", DocumentStyle: "pdf", URL: "https://example.org/free.pdf"},
			}},
		}
		if got := europePMCPaperURL(result); got != "https://example.org/free.pdf" {
			t.Errorf("Expected free PDF URL, got %q", got)
		}
	})

	t.Run("landing page for preprints", func(t *testing.T) {
		result := europePMCResult{
			ID: "PPR1127006", Source: "PPR",
			FullTextURLList: &europePMCFullTextURLList{FullTextURL: []europePMCFullTextURL{
				{AvailabilityCode: "F", DocumentStyle: "doi", URL: "https://doi.org/10.21203/rs.3.rs-7845516/v1"},
			}},
		}
		if got := europePMCPaperURL(result); got != "https://europepmc.org/article/PPR/PPR1127006" {
			t.Errorf("Expected Europe PMC landing page, got %q", got)
		}
	})

	t.Run("DOI resolver when no source or id", func(t *testing.T) {
		result := europePMCResult{DOI: "10.1234/Orphan"}
		if got := europePMCPaperURL(result); got != "https://doi.org/10.1234/orphan" {
			t.Errorf("Expected doi.org fallback, got %q", got)
		}
	})

	t.Run("empty when nothing to link", func(t *testing.T) {
		if got := europePMCPaperURL(europePMCResult{}); got != "" {
			t.Errorf("Expected empty URL, got %q", got)
		}
	})
}

func TestEuropePMCAuthors(t *testing.T) {
	t.Run("collective name fills a missing fullName", func(t *testing.T) {
		result := europePMCResult{AuthorList: &europePMCAuthorList{Author: []europePMCAuthor{
			{FullName: "Smith J", LastName: "Smith"},
			{CollectiveName: "WHO Working Group"},
			{},
		}}}
		got := europePMCAuthors(result)
		if strings.Join(got, "|") != "Smith J|WHO Working Group" {
			t.Errorf("Expected fullName then collectiveName with blanks dropped, got %v", got)
		}
	})

	t.Run("authorString with stray spacing", func(t *testing.T) {
		result := europePMCResult{AuthorString: "  Smith J ,  Doe JA. "}
		got := europePMCAuthors(result)
		if strings.Join(got, "|") != "Smith J|Doe JA" {
			t.Errorf("Expected trimmed names without trailing full stop, got %v", got)
		}
	})

	t.Run("no authors at all", func(t *testing.T) {
		if got := europePMCAuthors(europePMCResult{}); len(got) != 0 {
			t.Errorf("Expected no authors, got %v", got)
		}
	})
}

func TestEuropePMCFamilyName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Abu Bakar YI", "Abu Bakar"},
		{"Yang S", "Yang"},
		{"Hadie SNH.", "Hadie"},
		{"Smith", "Smith"},
		{"WHO Working Group", "WHO Working Group"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := europePMCFamilyName(tt.input); got != tt.want {
			t.Errorf("europePMCFamilyName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEuropePMCLooseTitleQuery(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Unload the load: Optimizing anatomy", "TITLE:(unload OR the OR load OR optimizing OR anatomy)"},
		{"Trials AND Errors NOT TO Repeat", "TITLE:(trials OR and OR errors OR not OR to OR repeat)"},
		{"???", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := europePMCLooseTitleQuery(tt.input); got != tt.want {
			t.Errorf("europePMCLooseTitleQuery(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEuropePMCYear(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"2026", 2026},
		{" 1999 ", 1999},
		{"", 0},
		{"n/a", 0},
		{"-5", 0},
	}
	for _, tt := range tests {
		if got := europePMCYear(tt.input); got != tt.want {
			t.Errorf("europePMCYear(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestEuropePMC_VerifyDOI(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		var requests []url.Values
		backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusOK, europePMCSearchFixture, &requests))

		result, err := backend.VerifyDOI(context.Background(), "https://doi.org/10.3389/FPSYG.2026.1921504")
		if err != nil {
			t.Fatalf("VerifyDOI failed: %v", err)
		}

		if got := requests[0].Get("query"); got != `DOI:"10.3389/fpsyg.2026.1921504"` {
			t.Errorf("Expected normalised DOI phrase query, got %q", got)
		}
		if got := requests[0].Get("pageSize"); got != "1" {
			t.Errorf("Expected pageSize=1, got %q", got)
		}
		if !result.Verified {
			t.Fatal("Expected DOI to verify")
		}
		if result.Confidence != 1.0 {
			t.Errorf("Expected confidence 1.0, got %f", result.Confidence)
		}
		if result.Source != "europepmc" {
			t.Errorf("Expected source 'europepmc', got %q", result.Source)
		}
		if result.Paper == nil || result.Paper.DOI != "10.3389/fpsyg.2026.1921504" {
			t.Errorf("Expected paper with matching DOI, got %+v", result.Paper)
		}
	})

	t.Run("not found", func(t *testing.T) {
		backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusOK, europePMCEmptyFixture, nil))

		result, err := backend.VerifyDOI(context.Background(), "10.9999/does.not.exist")
		if err != nil {
			t.Fatalf("Expected not-verified result rather than error, got %v", err)
		}
		if result.Verified {
			t.Error("Expected Verified=false for empty result list")
		}
		if result.Paper != nil {
			t.Errorf("Expected no paper, got %+v", result.Paper)
		}
		if result.Source != "europepmc" {
			t.Errorf("Expected source 'europepmc', got %q", result.Source)
		}
	})

	t.Run("hit with a different DOI is not verified", func(t *testing.T) {
		backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusOK, europePMCSearchFixture, nil))

		result, err := backend.VerifyDOI(context.Background(), "10.1234/unrelated")
		if err != nil {
			t.Fatalf("VerifyDOI failed: %v", err)
		}
		if result.Verified {
			t.Error("Expected Verified=false when the returned DOI does not match the request")
		}
	})

	t.Run("HTTP error", func(t *testing.T) {
		backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusServiceUnavailable, "down", nil))

		result, err := backend.VerifyDOI(context.Background(), "10.1002/ase.70266")
		if err == nil {
			t.Fatal("Expected error from 503 response, got nil")
		}
		if !strings.Contains(err.Error(), "europepmc") {
			t.Errorf("Expected error wrapped with backend name, got %q", err.Error())
		}
		if result != nil {
			t.Errorf("Expected nil result on error, got %+v", result)
		}
	})
}

func TestEuropePMC_VerifyByTitle(t *testing.T) {
	t.Run("above threshold on phrase query", func(t *testing.T) {
		var requests []url.Values
		backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusOK, europePMCSearchFixture, &requests))

		result, err := backend.VerifyByTitle(context.Background(), "Cognitive load in online learning: a randomised trial", []string{"Islam MA", "Yang S"}, 2026)
		if err != nil {
			t.Fatalf("VerifyByTitle failed: %v", err)
		}

		if len(requests) != 1 {
			t.Fatalf("Expected a single phrase request, got %d", len(requests))
		}
		if got := requests[0].Get("query"); got != `TITLE:"Cognitive load in online learning: a randomised trial"` {
			t.Errorf("Expected TITLE phrase query, got %q", got)
		}
		if got := requests[0].Get("pageSize"); got != "5" {
			t.Errorf("Expected pageSize=5, got %q", got)
		}
		if !result.Verified {
			t.Fatalf("Expected verification, got confidence %f", result.Confidence)
		}
		if result.Confidence < 0.9 {
			t.Errorf("Expected high confidence for exact title/year/authors, got %f", result.Confidence)
		}
		if result.Paper == nil || result.Paper.DOI != "10.3389/fpsyg.2026.1921504" {
			t.Errorf("Expected the matching paper, got %+v", result.Paper)
		}
	})

	t.Run("below threshold", func(t *testing.T) {
		backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusOK, europePMCSearchFixture, nil))

		result, err := backend.VerifyByTitle(context.Background(), "Quantum entanglement in superconducting qubits", []string{"Nakamura"}, 1999)
		if err != nil {
			t.Fatalf("VerifyByTitle failed: %v", err)
		}
		if result.Verified {
			t.Errorf("Expected Verified=false for unrelated title, got confidence %f", result.Confidence)
		}
		if result.Paper != nil {
			t.Errorf("Expected no paper, got %+v", result.Paper)
		}
	})

	t.Run("falls back to loose query when the phrase misses", func(t *testing.T) {
		var requests []url.Values
		backend := newEuropePMCTestBackend(t, func(w http.ResponseWriter, r *http.Request) {
			requests = append(requests, r.URL.Query())
			w.Header().Set("Content-Type", "application/json")
			if strings.HasPrefix(r.URL.Query().Get("query"), `TITLE:"`) {
				_, _ = w.Write([]byte(europePMCEmptyFixture))
				return
			}
			_, _ = w.Write([]byte(europePMCSearchFixture))
		})

		// One word differs from the indexed title ("optimising" vs "Optimizing").
		result, err := backend.VerifyByTitle(context.Background(), "Unload the load: optimising anatomy instruction with cognitive load theory", []string{"Rizvi"}, 2025)
		if err != nil {
			t.Fatalf("VerifyByTitle failed: %v", err)
		}

		if len(requests) != 2 {
			t.Fatalf("Expected phrase request then loose fallback, got %d requests", len(requests))
		}
		wantLoose := "TITLE:(unload OR the OR load OR optimising OR anatomy OR instruction OR with OR cognitive OR load OR theory)"
		if got := requests[1].Get("query"); got != wantLoose {
			t.Errorf("Expected loose TITLE query %q, got %q", wantLoose, got)
		}
		if !result.Verified {
			t.Fatalf("Expected fallback hit to verify, got confidence %f", result.Confidence)
		}
		if result.Paper == nil || result.Paper.DOI != "10.1002/ase.70266" {
			t.Errorf("Expected the anatomy paper, got %+v", result.Paper)
		}
	})

	t.Run("nothing found on either query", func(t *testing.T) {
		var requests []url.Values
		backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusOK, europePMCEmptyFixture, &requests))

		result, err := backend.VerifyByTitle(context.Background(), "No such paper", nil, 0)
		if err != nil {
			t.Fatalf("VerifyByTitle failed: %v", err)
		}
		if len(requests) != 2 {
			t.Errorf("Expected two requests before giving up, got %d", len(requests))
		}
		if result.Verified {
			t.Error("Expected Verified=false when both queries return nothing")
		}
	})

	t.Run("title with no tokens skips the loose query", func(t *testing.T) {
		var requests []url.Values
		backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusOK, europePMCEmptyFixture, &requests))

		result, err := backend.VerifyByTitle(context.Background(), "???", nil, 0)
		if err != nil {
			t.Fatalf("VerifyByTitle failed: %v", err)
		}
		if len(requests) != 1 {
			t.Errorf("Expected only the phrase request for a token-less title, got %d", len(requests))
		}
		if result.Verified {
			t.Error("Expected Verified=false")
		}
	})

	t.Run("HTTP error", func(t *testing.T) {
		backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusInternalServerError, "boom", nil))

		result, err := backend.VerifyByTitle(context.Background(), "Anything", nil, 0)
		if err == nil {
			t.Fatal("Expected error from 500 response, got nil")
		}
		if !strings.Contains(err.Error(), "europepmc") {
			t.Errorf("Expected error wrapped with backend name, got %q", err.Error())
		}
		if result != nil {
			t.Errorf("Expected nil result on error, got %+v", result)
		}
	})
}

func TestEuropePMC_HealthCheck(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		var requests []url.Values
		backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusOK, europePMCSearchFixture, &requests))

		status := backend.HealthCheck(context.Background())
		if !status.Healthy {
			t.Errorf("Expected healthy, got %+v", status)
		}
		if got := requests[0].Get("pageSize"); got != "1" {
			t.Errorf("Expected a pageSize=1 probe, got %q", got)
		}
		if got := requests[0].Get("resultType"); got != "lite" {
			t.Errorf("Expected a lite probe, got %q", got)
		}
	})

	t.Run("unhealthy", func(t *testing.T) {
		backend := newEuropePMCTestBackend(t, europePMCFixedResponse(http.StatusBadGateway, "bad gateway", nil))

		status := backend.HealthCheck(context.Background())
		if status.Healthy {
			t.Error("Expected unhealthy on 502")
		}
		if !strings.Contains(status.Message, "Europe PMC") {
			t.Errorf("Expected message to name the backend, got %q", status.Message)
		}
	})
}

func TestEuropePMCMatchConfidence(t *testing.T) {
	result := &europePMCResult{
		Title:   "Machine learning in healthcare.",
		PubYear: "2023",
		AuthorList: &europePMCAuthorList{Author: []europePMCAuthor{
			{FullName: "Smith J", LastName: "Smith"},
			{FullName: "Doe JA", LastName: "Doe"},
		}},
	}

	t.Run("exact match", func(t *testing.T) {
		score := europePMCMatchConfidence("Machine learning in healthcare", []string{"Smith", "Doe"}, 2023, result)
		if score < 0.95 {
			t.Errorf("Expected near-perfect score for exact match, got %f", score)
		}
	})

	t.Run("year within tolerance", func(t *testing.T) {
		exact := europePMCMatchConfidence("Machine learning in healthcare", nil, 2023, result)
		offByOne := europePMCMatchConfidence("Machine learning in healthcare", nil, 2022, result)
		if exact <= offByOne {
			t.Error("Exact year match should score higher than off-by-one")
		}
		if offByOne < europePMCVerifyThreshold {
			t.Errorf("Off-by-one year should still clear the threshold, got %f", offByOne)
		}
	})

	t.Run("first-author gate caps the score", func(t *testing.T) {
		score := europePMCMatchConfidence("Machine learning in healthcare", []string{"Johnson"}, 2023, result)
		if score > 0.4 {
			t.Errorf("Expected first-author gate to cap at 0.4, got %f", score)
		}
	})

	t.Run("poor match", func(t *testing.T) {
		score := europePMCMatchConfidence("Quantum physics", []string{"Johnson"}, 2010, result)
		if score > 0.3 {
			t.Errorf("Expected low score for poor match, got %f", score)
		}
	})

	t.Run("authorString-only record still passes the gate", func(t *testing.T) {
		stringOnly := &europePMCResult{
			Title:        "Machine learning in healthcare.",
			PubYear:      "2023",
			AuthorString: "Abu Bakar YI, Smith J.",
		}
		score := europePMCMatchConfidence("Machine learning in healthcare", []string{"Abu Bakar"}, 2023, stringOnly)
		if score < europePMCVerifyThreshold {
			t.Errorf("Expected multi-word family name derived from authorString to match, got %f", score)
		}
	})
}

func TestEuropePMCAuthorSimilarity(t *testing.T) {
	workAuthors := []string{"Smith J", "Doe JA"}

	t.Run("matching family names", func(t *testing.T) {
		if score := europePMCAuthorSimilarity([]string{"Smith", "Doe"}, workAuthors); score < 0.99 {
			t.Errorf("Expected 1.0 for both authors matching, got %f", score)
		}
	})

	t.Run("initials never match", func(t *testing.T) {
		if score := europePMCAuthorSimilarity([]string{"J. A. Nobody"}, workAuthors); score != 0.0 {
			t.Errorf("Expected 0 when only initials coincide, got %f", score)
		}
	})

	t.Run("partial match", func(t *testing.T) {
		score := europePMCAuthorSimilarity([]string{"Smith", "Johnson"}, workAuthors)
		if score < 0.4 || score > 0.6 {
			t.Errorf("Expected ~0.5 for one of two authors matching, got %f", score)
		}
	})

	t.Run("empty inputs", func(t *testing.T) {
		if score := europePMCAuthorSimilarity(nil, workAuthors); score != 0.0 {
			t.Errorf("Expected 0 for empty query authors, got %f", score)
		}
		if score := europePMCAuthorSimilarity([]string{"Smith"}, nil); score != 0.0 {
			t.Errorf("Expected 0 for empty work authors, got %f", score)
		}
	})
}
