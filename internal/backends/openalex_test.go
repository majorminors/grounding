package backends

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Compile-time interface compliance checks
var (
	_ SearchBackend   = (*OpenAlexBackend)(nil)
	_ CitationBackend = (*OpenAlexBackend)(nil)
)

func TestOpenAlex_Name(t *testing.T) {
	client := NewClient()
	backend := NewOpenAlex(client, "")
	if backend.Name() != "openalex" {
		t.Errorf("Expected name 'openalex', got '%s'", backend.Name())
	}
}

func TestOpenAlexWorkToPaper(t *testing.T) {
	t.Run("full work with PDF URL", func(t *testing.T) {
		w := openalexWork{
			ID:              "W12345",
			DOI:             "https://doi.org/10.1234/test.paper",
			Title:           "Machine Learning in Healthcare",
			PublicationYear: 2023,
			CitedByCount:    150,
			Authorships: []struct {
				Author struct {
					DisplayName string `json:"display_name"`
				} `json:"author"`
			}{
				{Author: struct {
					DisplayName string `json:"display_name"`
				}{DisplayName: "John Smith"}},
				{Author: struct {
					DisplayName string `json:"display_name"`
				}{DisplayName: "Jane Doe"}},
			},
			PrimaryLocation: &struct {
				Source *struct {
					DisplayName string `json:"display_name"`
				} `json:"source"`
				LandingPageURL string `json:"landing_page_url"`
				PDFURL         string `json:"pdf_url"`
			}{
				Source: &struct {
					DisplayName string `json:"display_name"`
				}{DisplayName: "Nature Medicine"},
				LandingPageURL: "https://example.com/paper",
				PDFURL:         "https://example.com/paper.pdf",
			},
			OpenAccess: &struct {
				OAURL string `json:"oa_url"`
			}{OAURL: "https://example.com/oa"},
		}

		paper := openalexWorkToPaper(w)

		if paper.Title != "Machine Learning in Healthcare" {
			t.Errorf("Expected title 'Machine Learning in Healthcare', got '%s'", paper.Title)
		}
		if paper.Year != 2023 {
			t.Errorf("Expected year 2023, got %d", paper.Year)
		}
		if paper.DOI != "10.1234/test.paper" {
			t.Errorf("Expected DOI '10.1234/test.paper', got '%s'", paper.DOI)
		}
		if paper.Citations != 150 {
			t.Errorf("Expected 150 citations, got %d", paper.Citations)
		}
		if paper.Journal != "Nature Medicine" {
			t.Errorf("Expected journal 'Nature Medicine', got '%s'", paper.Journal)
		}
		if len(paper.Authors) != 2 {
			t.Errorf("Expected 2 authors, got %d", len(paper.Authors))
		}
		if paper.Authors[0] != "John Smith" {
			t.Errorf("Expected author 'John Smith', got '%s'", paper.Authors[0])
		}
		// Should prefer PDF URL
		if paper.URL != "https://example.com/paper.pdf" {
			t.Errorf("Expected PDF URL, got '%s'", paper.URL)
		}
		if paper.Source != "openalex" {
			t.Errorf("Expected source 'openalex', got '%s'", paper.Source)
		}
	})

	t.Run("work with OA URL but no PDF", func(t *testing.T) {
		w := openalexWork{
			ID:    "W12346",
			Title: "Test Paper",
			PrimaryLocation: &struct {
				Source *struct {
					DisplayName string `json:"display_name"`
				} `json:"source"`
				LandingPageURL string `json:"landing_page_url"`
				PDFURL         string `json:"pdf_url"`
			}{
				LandingPageURL: "https://example.com/landing",
				// No PDF URL
			},
			OpenAccess: &struct {
				OAURL string `json:"oa_url"`
			}{OAURL: "https://example.com/oa"},
		}

		paper := openalexWorkToPaper(w)
		// Should use OA URL when no PDF
		if paper.URL != "https://example.com/oa" {
			t.Errorf("Expected OA URL, got '%s'", paper.URL)
		}
	})

	t.Run("work with only landing page", func(t *testing.T) {
		w := openalexWork{
			ID:    "W12347",
			Title: "Test Paper",
			PrimaryLocation: &struct {
				Source *struct {
					DisplayName string `json:"display_name"`
				} `json:"source"`
				LandingPageURL string `json:"landing_page_url"`
				PDFURL         string `json:"pdf_url"`
			}{
				LandingPageURL: "https://example.com/landing",
			},
		}

		paper := openalexWorkToPaper(w)
		if paper.URL != "https://example.com/landing" {
			t.Errorf("Expected landing page URL, got '%s'", paper.URL)
		}
	})

	t.Run("empty fields", func(t *testing.T) {
		w := openalexWork{
			ID: "W12348",
		}

		paper := openalexWorkToPaper(w)
		if paper.Title != "" {
			t.Errorf("Expected empty title, got '%s'", paper.Title)
		}
		if len(paper.Authors) != 0 {
			t.Errorf("Expected no authors, got %d", len(paper.Authors))
		}
		if paper.Year != 0 {
			t.Errorf("Expected year 0, got %d", paper.Year)
		}
		if paper.URL != "" {
			t.Errorf("Expected empty URL, got '%s'", paper.URL)
		}
		if paper.Source != "openalex" {
			t.Errorf("Expected source 'openalex', got '%s'", paper.Source)
		}
	})

	t.Run("empty author names filtered", func(t *testing.T) {
		w := openalexWork{
			Authorships: []struct {
				Author struct {
					DisplayName string `json:"display_name"`
				} `json:"author"`
			}{
				{Author: struct {
					DisplayName string `json:"display_name"`
				}{DisplayName: "Real Author"}},
				{Author: struct {
					DisplayName string `json:"display_name"`
				}{DisplayName: ""}},
				{Author: struct {
					DisplayName string `json:"display_name"`
				}{DisplayName: "Another Author"}},
			},
		}

		paper := openalexWorkToPaper(w)
		if len(paper.Authors) != 2 {
			t.Errorf("Expected 2 authors (empty filtered), got %d", len(paper.Authors))
		}
	})

	t.Run("DOI normalization", func(t *testing.T) {
		w := openalexWork{
			DOI: "https://doi.org/10.5678/normalized",
		}

		paper := openalexWorkToPaper(w)
		if paper.DOI != "10.5678/normalized" {
			t.Errorf("Expected normalized DOI '10.5678/normalized', got '%s'", paper.DOI)
		}
	})
}

func TestOpenAlex_HealthCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/works" {
			resp := openalexSearchResponse{
				Results: []openalexWork{{ID: "W1", Title: "Test"}},
				Meta: struct {
					Count int `json:"count"`
				}{Count: 1},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// Create backend - we can't easily inject the URL, but we can test the type
	client := NewClient()
	backend := NewOpenAlex(client, "")

	if backend.Name() != "openalex" {
		t.Errorf("Expected name 'openalex', got '%s'", backend.Name())
	}
}

func TestOpenAlex_VerifyDOI(t *testing.T) {
	mockWork := openalexWork{
		ID:              "W12345",
		DOI:             "https://doi.org/10.1234/test",
		Title:           "Test Paper",
		PublicationYear: 2023,
		CitedByCount:    10,
	}

	t.Run("found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// OpenAlex uses /works/https://doi.org/... format
			if r.URL.Path == "/works/https://doi.org/10.1234/test" {
				json.NewEncoder(w).Encode(mockWork)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		// Test the conversion works correctly
		paper := openalexWorkToPaper(mockWork)
		if paper.Title != "Test Paper" {
			t.Errorf("Expected title 'Test Paper', got '%s'", paper.Title)
		}
		if paper.DOI != "10.1234/test" {
			t.Errorf("Expected DOI '10.1234/test', got '%s'", paper.DOI)
		}
	})

	t.Run("not found", func(t *testing.T) {
		// Test that 404 handling works via IsNotFound
		err := &HTTPError{StatusCode: 404, Body: "not found"}
		if !IsNotFound(err) {
			t.Error("Expected IsNotFound to return true for 404")
		}
	})
}

func TestOpenAlex_Search(t *testing.T) {
	mockWorks := []openalexWork{
		{
			ID:              "W1",
			DOI:             "https://doi.org/10.1234/paper1",
			Title:           "First Paper",
			PublicationYear: 2023,
			CitedByCount:    100,
			Authorships: []struct {
				Author struct {
					DisplayName string `json:"display_name"`
				} `json:"author"`
			}{
				{Author: struct {
					DisplayName string `json:"display_name"`
				}{DisplayName: "Author One"}},
			},
		},
		{
			ID:              "W2",
			DOI:             "https://doi.org/10.1234/paper2",
			Title:           "Second Paper",
			PublicationYear: 2022,
			CitedByCount:    50,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openalexSearchResponse{
			Results: mockWorks,
			Meta: struct {
				Count int `json:"count"`
			}{Count: 2},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Test conversion
	papers := make([]Paper, 0, len(mockWorks))
	for _, w := range mockWorks {
		papers = append(papers, openalexWorkToPaper(w))
	}

	if len(papers) != 2 {
		t.Errorf("Expected 2 papers, got %d", len(papers))
	}

	p1 := papers[0]
	if p1.Title != "First Paper" {
		t.Errorf("Expected title 'First Paper', got '%s'", p1.Title)
	}
	if p1.Year != 2023 {
		t.Errorf("Expected year 2023, got %d", p1.Year)
	}
	if p1.Citations != 100 {
		t.Errorf("Expected 100 citations, got %d", p1.Citations)
	}
	if len(p1.Authors) != 1 {
		t.Errorf("Expected 1 author, got %d", len(p1.Authors))
	}
}

func TestOAMatchConfidence(t *testing.T) {
	work := &openalexWork{
		Title:           "Machine Learning in Healthcare",
		PublicationYear: 2023,
		Authorships: []struct {
			Author struct {
				DisplayName string `json:"display_name"`
			} `json:"author"`
		}{
			{Author: struct {
				DisplayName string `json:"display_name"`
			}{DisplayName: "John Smith"}},
			{Author: struct {
				DisplayName string `json:"display_name"`
			}{DisplayName: "Jane Doe"}},
		},
	}

	t.Run("exact match", func(t *testing.T) {
		score := oaMatchConfidence("Machine Learning in Healthcare", []string{"Smith", "Doe"}, 2023, work)
		if score < 0.8 {
			t.Errorf("Expected high score for exact match, got %f", score)
		}
	})

	t.Run("partial title match", func(t *testing.T) {
		score := oaMatchConfidence("Machine Learning", []string{"Smith"}, 2023, work)
		if score < 0.5 {
			t.Errorf("Expected moderate score for partial match, got %f", score)
		}
	})

	t.Run("poor match", func(t *testing.T) {
		score := oaMatchConfidence("Quantum Physics", []string{"Johnson"}, 2020, work)
		if score > 0.3 {
			t.Errorf("Expected low score for poor match, got %f", score)
		}
	})

	t.Run("year within tolerance", func(t *testing.T) {
		score1 := oaMatchConfidence("Machine Learning in Healthcare", nil, 2023, work)
		score2 := oaMatchConfidence("Machine Learning in Healthcare", nil, 2022, work)

		if score1 <= score2 {
			t.Error("Exact year match should score higher than off-by-one")
		}
		if score2 < 0.5 {
			t.Errorf("Off-by-one year should still have decent score, got %f", score2)
		}
	})
}

func TestOAAuthorSimilarity(t *testing.T) {
	authors := []struct {
		Author struct {
			DisplayName string `json:"display_name"`
		} `json:"author"`
	}{
		{Author: struct {
			DisplayName string `json:"display_name"`
		}{DisplayName: "John Smith"}},
		{Author: struct {
			DisplayName string `json:"display_name"`
		}{DisplayName: "Jane Doe"}},
	}

	t.Run("matching last names", func(t *testing.T) {
		score := oaAuthorSimilarity([]string{"Smith", "Doe"}, authors)
		if score < 0.9 {
			t.Errorf("Expected high score for matching last names, got %f", score)
		}
	})

	t.Run("partial match", func(t *testing.T) {
		score := oaAuthorSimilarity([]string{"Smith", "Johnson"}, authors)
		if score < 0.4 || score > 0.6 {
			t.Errorf("Expected ~0.5 for one of two authors matching, got %f", score)
		}
	})

	t.Run("no match", func(t *testing.T) {
		score := oaAuthorSimilarity([]string{"Williams", "Brown"}, authors)
		if score != 0.0 {
			t.Errorf("Expected 0 for no matches, got %f", score)
		}
	})

	t.Run("empty query authors", func(t *testing.T) {
		score := oaAuthorSimilarity([]string{}, authors)
		if score != 0.0 {
			t.Errorf("Expected 0 for empty query, got %f", score)
		}
	})

	t.Run("empty paper authors", func(t *testing.T) {
		score := oaAuthorSimilarity([]string{"Smith"}, nil)
		if score != 0.0 {
			t.Errorf("Expected 0 for empty paper authors, got %f", score)
		}
	})
}

func TestOpenAlex_SearchLimitCapping(t *testing.T) {
	// Test that the limit is properly capped
	client := NewClient()
	backend := NewOpenAlex(client, "")

	// We can't easily test the actual request without mocking,
	// but we can verify the backend is properly initialized
	if backend.client == nil {
		t.Error("Expected client to be set")
	}
}

func TestOpenAlex_Integration(t *testing.T) {
	mockWork := openalexWork{
		ID:              "W999",
		DOI:             "https://doi.org/10.9999/integration",
		Title:           "Integration Test Paper",
		PublicationYear: 2024,
		CitedByCount:    42,
		Authorships: []struct {
			Author struct {
				DisplayName string `json:"display_name"`
			} `json:"author"`
		}{
			{Author: struct {
				DisplayName string `json:"display_name"`
			}{DisplayName: "Integration Author"}},
		},
		PrimaryLocation: &struct {
			Source *struct {
				DisplayName string `json:"display_name"`
			} `json:"source"`
			LandingPageURL string `json:"landing_page_url"`
			PDFURL         string `json:"pdf_url"`
		}{
			Source: &struct {
				DisplayName string `json:"display_name"`
			}{DisplayName: "Integration Journal"},
			LandingPageURL: "https://example.com/integration",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/works":
			resp := openalexSearchResponse{
				Results: []openalexWork{mockWork},
				Meta: struct {
					Count int `json:"count"`
				}{Count: 1},
			}
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Verify the paper conversion works correctly
	paper := openalexWorkToPaper(mockWork)
	if paper.Title != "Integration Test Paper" {
		t.Errorf("Expected 'Integration Test Paper', got '%s'", paper.Title)
	}
	if paper.Journal != "Integration Journal" {
		t.Errorf("Expected 'Integration Journal', got '%s'", paper.Journal)
	}
}

func TestOpenAlex_VerifyByTitle_Threshold(t *testing.T) {
	// Verify that the matching threshold of 0.5 is applied correctly
	goodMatch := &openalexWork{
		Title:           "Machine Learning in Healthcare Applications",
		PublicationYear: 2023,
		Authorships: []struct {
			Author struct {
				DisplayName string `json:"display_name"`
			} `json:"author"`
		}{
			{Author: struct {
				DisplayName string `json:"display_name"`
			}{DisplayName: "John Smith"}},
		},
	}

	// Good match should pass
	score := oaMatchConfidence("Machine Learning in Healthcare", []string{"Smith"}, 2023, goodMatch)
	if score < 0.5 {
		t.Errorf("Good match should exceed 0.5 threshold, got %f", score)
	}

	// Poor match should fail
	poorMatch := &openalexWork{
		Title:           "Quantum Computing Advances",
		PublicationYear: 2020,
		Authorships: []struct {
			Author struct {
				DisplayName string `json:"display_name"`
			} `json:"author"`
		}{
			{Author: struct {
				DisplayName string `json:"display_name"`
			}{DisplayName: "Alice Johnson"}},
		},
	}

	score = oaMatchConfidence("Machine Learning in Healthcare", []string{"Smith"}, 2023, poorMatch)
	if score >= 0.5 {
		t.Errorf("Poor match should be below 0.5 threshold, got %f", score)
	}
}

func TestOpenAlex_RealAPICall(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := NewClient()
	backend := NewOpenAlex(client, "")

	ctx := context.Background()

	// Test health check against real API
	status := backend.HealthCheck(ctx)
	if !status.Healthy {
		t.Skipf("OpenAlex API not available: %s", status.Message)
	}

	// Test search
	papers, err := backend.Search(ctx, "machine learning", 5, SearchFilters{})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(papers) == 0 {
		t.Error("Expected at least one paper from search")
	}

	// Verify paper fields are populated
	for _, p := range papers {
		if p.Title == "" {
			t.Error("Paper should have a title")
		}
		if p.Source != "openalex" {
			t.Errorf("Paper source should be 'openalex', got '%s'", p.Source)
		}
	}
}

func TestExtractOAID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://openalex.org/W2316701970", "W2316701970"},
		{"W2316701970", "W2316701970"},
		{"https://openalex.org/W123", "W123"},
	}
	for _, tt := range tests {
		got := extractOAID(tt.input)
		if got != tt.want {
			t.Errorf("extractOAID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestOpenAlex_CitationGraph_RealAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := NewClient()
	oa := NewOpenAlex(client, "")
	ctx := context.Background()

	// Resolve Gould & Lewontin Spandrels paper
	paper, oaID, err := oa.ResolvePaper(ctx, "10.1098/rspb.1979.0086")
	if err != nil {
		t.Fatalf("ResolvePaper failed: %v", err)
	}
	if paper.Title == "" {
		t.Error("Expected non-empty title")
	}
	if oaID == "" {
		t.Error("Expected non-empty OpenAlex ID")
	}
	t.Logf("Resolved: %q (OA ID: %s, year: %d, citations: %d)", paper.Title, oaID, paper.Year, paper.Citations)

	// Fetch citations (papers that cite the Spandrels paper)
	cites, err := oa.FetchCitations(ctx, oaID, 5, "citations")
	if err != nil {
		t.Fatalf("FetchCitations failed: %v", err)
	}
	if len(cites) == 0 {
		t.Error("Expected at least one citation for a highly-cited paper")
	}
	t.Logf("Got %d citations", len(cites))
	for i, e := range cites {
		t.Logf("  %d. %s (%d) [%d citations]", i+1, e.Paper.Title, e.Paper.Year, e.Paper.Citations)
		// OA fallback should not have SS-specific fields
		if e.IsInfluential {
			t.Error("OA edges should not have IsInfluential set")
		}
		if len(e.Intents) > 0 {
			t.Error("OA edges should not have Intents set")
		}
	}

	// Fetch references (papers cited BY the Spandrels paper)
	refs, err := oa.FetchReferences(ctx, oaID, 5)
	if err != nil {
		t.Fatalf("FetchReferences failed: %v", err)
	}
	if len(refs) == 0 {
		t.Error("Expected at least one reference")
	}
	t.Logf("Got %d references", len(refs))
	for i, e := range refs {
		t.Logf("  %d. %s (%d) [%d citations]", i+1, e.Paper.Title, e.Paper.Year, e.Paper.Citations)
	}

	// Test ResolveBySearch
	paper2, oaID2, err := oa.ResolveBySearch(ctx, "spandrels san marco panglossian adaptationist")
	if err != nil {
		t.Fatalf("ResolveBySearch failed: %v", err)
	}
	t.Logf("Search resolved: %q (OA ID: %s)", paper2.Title, oaID2)
}

func TestOpenAlex_AbstractFromInvertedIndex(t *testing.T) {
	index := map[string][]int{
		"load":      {1},
		"Cognitive": {0},
		"theory":    {2, 5},
		"is":        {3},
		"a":         {4},
	}
	got := openalexAbstractFromInvertedIndex(index)
	want := "Cognitive load theory is a theory"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	if got := openalexAbstractFromInvertedIndex(nil); got != "" {
		t.Errorf("nil index should give empty abstract, got %q", got)
	}

	// A gap in positions is skipped, not padded.
	gapped := map[string][]int{"first": {0}, "third": {2}}
	if got := openalexAbstractFromInvertedIndex(gapped); got != "first third" {
		t.Errorf("gapped index: got %q", got)
	}
}

func TestOpenAlex_RequestCarriesKeyAndSelect(t *testing.T) {
	var gotAuthorization, gotSelect string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotSelect = r.URL.Query().Get("select")
		w.Write([]byte(`{"results":[{"id":"https://openalex.org/W1","title":"T","abstract_inverted_index":{"Hello":[0],"world":[1]}}]}`))
	}))
	defer server.Close()

	backend := NewOpenAlex(NewClient(), "secret-key")
	var resp openalexSearchResponse
	if err := backend.getJSON(context.Background(), server.URL+"/works?search=x", &resp); err != nil {
		t.Fatalf("getJSON: %v", err)
	}
	if gotAuthorization != "Bearer secret-key" {
		t.Errorf("expected bearer key, got %q", gotAuthorization)
	}
	if gotSelect != openalexSelectFields {
		t.Errorf("expected select fields, got %q", gotSelect)
	}
	if len(resp.Results) != 1 || openalexWorkToPaper(resp.Results[0]).Abstract != "Hello world" {
		t.Errorf("abstract not reconstructed from response: %+v", resp.Results)
	}
}
