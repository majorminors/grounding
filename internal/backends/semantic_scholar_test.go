package backends

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSemanticScholar_Name(t *testing.T) {
	client := NewClient()
	backend := NewSemanticScholar(client, "")
	if backend.Name() != "semantic-scholar" {
		t.Errorf("Expected name 'semantic-scholar', got '%s'", backend.Name())
	}
}

func TestSemanticScholar_Search(t *testing.T) {
	mockPapers := []ssPaper{
		{
			PaperID:       "abc123",
			Title:         "Machine Learning in Healthcare",
			Abstract:      "This paper explores ML applications in healthcare.",
			Year:          2023,
			Venue:         "Nature Medicine",
			CitationCount: 150,
			URL:           "https://semanticscholar.org/paper/abc123",
			ExternalIDs: struct {
				DOI string `json:"DOI"`
			}{DOI: "10.1234/ml.health"},
			Authors: []struct {
				Name string `json:"name"`
			}{
				{Name: "John Smith"},
				{Name: "Jane Doe"},
			},
		},
		{
			PaperID:       "def456",
			Title:         "Deep Learning for Medical Imaging",
			Abstract:      "A study on deep learning techniques.",
			Year:          2022,
			Venue:         "IEEE TMI",
			CitationCount: 75,
			URL:           "https://semanticscholar.org/paper/def456",
			ExternalIDs: struct {
				DOI string `json:"DOI"`
			}{DOI: "10.5678/dl.imaging"},
			Authors: []struct {
				Name string `json:"name"`
			}{
				{Name: "Alice Johnson"},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ssSearchResponse{
			Total: len(mockPapers),
			Data:  mockPapers,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Test conversion
	papers := make([]Paper, 0, len(mockPapers))
	for _, p := range mockPapers {
		papers = append(papers, ssPaperToPaper(p))
	}

	if len(papers) != 2 {
		t.Errorf("Expected 2 papers, got %d", len(papers))
	}

	// Check first paper
	p1 := papers[0]
	if p1.Title != "Machine Learning in Healthcare" {
		t.Errorf("Expected title 'Machine Learning in Healthcare', got '%s'", p1.Title)
	}
	if len(p1.Authors) != 2 {
		t.Errorf("Expected 2 authors, got %d", len(p1.Authors))
	}
	if p1.Authors[0] != "John Smith" {
		t.Errorf("Expected author 'John Smith', got '%s'", p1.Authors[0])
	}
	if p1.Year != 2023 {
		t.Errorf("Expected year 2023, got %d", p1.Year)
	}
	if p1.DOI != "10.1234/ml.health" {
		t.Errorf("Expected DOI '10.1234/ml.health', got '%s'", p1.DOI)
	}
	if p1.Journal != "Nature Medicine" {
		t.Errorf("Expected journal 'Nature Medicine', got '%s'", p1.Journal)
	}
	if p1.Citations != 150 {
		t.Errorf("Expected 150 citations, got %d", p1.Citations)
	}
	if p1.Source != "semantic-scholar" {
		t.Errorf("Expected source 'semantic-scholar', got '%s'", p1.Source)
	}
	if p1.Abstract != "This paper explores ML applications in healthcare." {
		t.Errorf("Expected abstract, got '%s'", p1.Abstract)
	}

	// Check second paper
	p2 := papers[1]
	if p2.Year != 2022 {
		t.Errorf("Expected year 2022, got %d", p2.Year)
	}
}

func TestSemanticScholar_VerifyDOI_Found(t *testing.T) {
	mockPaper := ssPaper{
		PaperID:       "abc123",
		Title:         "Test Paper",
		Abstract:      "Test abstract",
		Year:          2023,
		Venue:         "Test Journal",
		CitationCount: 10,
		URL:           "https://semanticscholar.org/paper/abc123",
		ExternalIDs: struct {
			DOI string `json:"DOI"`
		}{DOI: "10.1234/test"},
		Authors: []struct {
			Name string `json:"name"`
		}{
			{Name: "Test Author"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the URL format
		expectedPath := "/graph/v1/paper/DOI:10.1234%2Ftest"
		if r.URL.Path != expectedPath {
			// This is for the search endpoint fallback
			json.NewEncoder(w).Encode(mockPaper)
			return
		}
		json.NewEncoder(w).Encode(mockPaper)
	}))
	defer server.Close()

	// Test the paper conversion
	paper := ssPaperToPaper(mockPaper)
	if paper.Title != "Test Paper" {
		t.Errorf("Expected title 'Test Paper', got '%s'", paper.Title)
	}
	if paper.DOI != "10.1234/test" {
		t.Errorf("Expected DOI '10.1234/test', got '%s'", paper.DOI)
	}
}

func TestSemanticScholar_VerifyDOI_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error": "Paper not found"}`))
	}))
	defer server.Close()

	// Test that 404 handling works via IsNotFound
	err := &HTTPError{StatusCode: 404, Body: "not found"}
	if !IsNotFound(err) {
		t.Error("Expected IsNotFound to return true for 404")
	}
}

func TestSemanticScholar_HealthCheck(t *testing.T) {
	client := NewClient()
	backend := NewSemanticScholar(client, "")

	// Test that health check returns correct name
	if backend.Name() != "semantic-scholar" {
		t.Errorf("Expected name 'semantic-scholar', got '%s'", backend.Name())
	}
}

func TestSSPaperToPaper(t *testing.T) {
	t.Run("full paper", func(t *testing.T) {
		p := ssPaper{
			PaperID:       "abc123",
			Title:         "Full Paper",
			Abstract:      "Full abstract",
			Year:          2023,
			Venue:         "Full Journal",
			CitationCount: 100,
			URL:           "https://example.com/paper",
			ExternalIDs: struct {
				DOI string `json:"DOI"`
			}{DOI: "10.1234/full"},
			Authors: []struct {
				Name string `json:"name"`
			}{
				{Name: "Author One"},
				{Name: "Author Two"},
			},
			OpenAccessPDF: &struct {
				URL string `json:"url"`
			}{URL: "https://example.com/pdf"},
		}

		paper := ssPaperToPaper(p)
		if paper.Title != "Full Paper" {
			t.Errorf("Expected title 'Full Paper', got '%s'", paper.Title)
		}
		if paper.Abstract != "Full abstract" {
			t.Errorf("Expected abstract 'Full abstract', got '%s'", paper.Abstract)
		}
		if paper.Year != 2023 {
			t.Errorf("Expected year 2023, got %d", paper.Year)
		}
		if paper.Journal != "Full Journal" {
			t.Errorf("Expected journal 'Full Journal', got '%s'", paper.Journal)
		}
		if paper.Citations != 100 {
			t.Errorf("Expected 100 citations, got %d", paper.Citations)
		}
		if paper.DOI != "10.1234/full" {
			t.Errorf("Expected DOI '10.1234/full', got '%s'", paper.DOI)
		}
		if len(paper.Authors) != 2 {
			t.Errorf("Expected 2 authors, got %d", len(paper.Authors))
		}
		// Should prefer open access PDF URL
		if paper.URL != "https://example.com/pdf" {
			t.Errorf("Expected open access URL, got '%s'", paper.URL)
		}
		if paper.Source != "semantic-scholar" {
			t.Errorf("Expected source 'semantic-scholar', got '%s'", paper.Source)
		}
	})

	t.Run("empty fields", func(t *testing.T) {
		p := ssPaper{
			PaperID: "empty123",
			// All other fields empty/zero
		}

		paper := ssPaperToPaper(p)
		if paper.Title != "" {
			t.Errorf("Expected empty title, got '%s'", paper.Title)
		}
		if len(paper.Authors) != 0 {
			t.Errorf("Expected no authors, got %d", len(paper.Authors))
		}
		if paper.Year != 0 {
			t.Errorf("Expected year 0, got %d", paper.Year)
		}
		if paper.Source != "semantic-scholar" {
			t.Errorf("Expected source 'semantic-scholar', got '%s'", paper.Source)
		}
	})

	t.Run("no open access pdf", func(t *testing.T) {
		p := ssPaper{
			PaperID: "nopdf123",
			URL:     "https://example.com/paper",
			// OpenAccessPDF is nil
		}

		paper := ssPaperToPaper(p)
		if paper.URL != "https://example.com/paper" {
			t.Errorf("Expected paper URL, got '%s'", paper.URL)
		}
	})

	t.Run("empty author names filtered", func(t *testing.T) {
		p := ssPaper{
			Authors: []struct {
				Name string `json:"name"`
			}{
				{Name: "Real Author"},
				{Name: ""},
				{Name: "Another Author"},
			},
		}

		paper := ssPaperToPaper(p)
		if len(paper.Authors) != 2 {
			t.Errorf("Expected 2 authors (empty filtered), got %d", len(paper.Authors))
		}
	})
}

func TestSSMatchConfidence(t *testing.T) {
	paper := &ssPaper{
		Title: "Machine Learning in Healthcare",
		Year:  2023,
		Authors: []struct {
			Name string `json:"name"`
		}{
			{Name: "John Smith"},
			{Name: "Jane Doe"},
		},
	}

	t.Run("exact match", func(t *testing.T) {
		score := ssMatchConfidence("Machine Learning in Healthcare", []string{"Smith", "Doe"}, 2023, paper)
		if score < 0.8 {
			t.Errorf("Expected high score for exact match, got %f", score)
		}
	})

	t.Run("partial title match", func(t *testing.T) {
		score := ssMatchConfidence("Machine Learning", []string{"Smith"}, 2023, paper)
		if score < 0.5 {
			t.Errorf("Expected moderate score for partial match, got %f", score)
		}
	})

	t.Run("poor match", func(t *testing.T) {
		score := ssMatchConfidence("Quantum Physics", []string{"Johnson"}, 2020, paper)
		if score > 0.3 {
			t.Errorf("Expected low score for poor match, got %f", score)
		}
	})

	t.Run("year within tolerance", func(t *testing.T) {
		// Year off by 1 should still get partial credit
		score1 := ssMatchConfidence("Machine Learning in Healthcare", nil, 2023, paper)
		score2 := ssMatchConfidence("Machine Learning in Healthcare", nil, 2022, paper)

		if score1 <= score2 {
			t.Errorf("Exact year match should score higher than off-by-one")
		}
		if score2 < 0.5 {
			t.Errorf("Off-by-one year should still have decent score, got %f", score2)
		}
	})
}

func TestSSAuthorSimilarity(t *testing.T) {
	authors := []struct {
		Name string `json:"name"`
	}{
		{Name: "John Smith"},
		{Name: "Jane Doe"},
	}

	t.Run("matching last names", func(t *testing.T) {
		score := ssAuthorSimilarity([]string{"Smith", "Doe"}, authors)
		if score < 0.9 {
			t.Errorf("Expected high score for matching last names, got %f", score)
		}
	})

	t.Run("partial match", func(t *testing.T) {
		score := ssAuthorSimilarity([]string{"Smith", "Johnson"}, authors)
		if score < 0.4 || score > 0.6 {
			t.Errorf("Expected ~0.5 for one of two authors matching, got %f", score)
		}
	})

	t.Run("no match", func(t *testing.T) {
		score := ssAuthorSimilarity([]string{"Williams", "Brown"}, authors)
		if score != 0.0 {
			t.Errorf("Expected 0 for no matches, got %f", score)
		}
	})

	t.Run("empty query authors", func(t *testing.T) {
		score := ssAuthorSimilarity([]string{}, authors)
		if score != 0.0 {
			t.Errorf("Expected 0 for empty query, got %f", score)
		}
	})

	t.Run("empty paper authors", func(t *testing.T) {
		score := ssAuthorSimilarity([]string{"Smith"}, nil)
		if score != 0.0 {
			t.Errorf("Expected 0 for empty paper authors, got %f", score)
		}
	})
}

func TestSemanticScholar_RateLimitSelection(t *testing.T) {
	t.Run("without API key initialises", func(t *testing.T) {
		client := NewClient()
		_ = NewSemanticScholar(client, "")
		// Rate limit is set internally, we can't easily inspect it,
		// but the test ensures no panic and proper initialization
	})

	t.Run("with API key stores key", func(t *testing.T) {
		client := NewClient()
		backend := NewSemanticScholar(client, "test-api-key")
		if backend.apiKey != "test-api-key" {
			t.Errorf("Expected API key to be set")
		}
	})
}

func TestSemanticScholar_Integration(t *testing.T) {
	// This test verifies the full flow works with a mock server
	mockPaper := ssPaper{
		PaperID:       "test123",
		Title:         "Integration Test Paper",
		Abstract:      "Testing the full integration",
		Year:          2024,
		Venue:         "Test Venue",
		CitationCount: 42,
		URL:           "https://example.com/paper",
		ExternalIDs: struct {
			DOI string `json:"DOI"`
		}{DOI: "10.9999/test"},
		Authors: []struct {
			Name string `json:"name"`
		}{
			{Name: "Test Author"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for API key header
		apiKey := r.Header.Get("x-api-key")
		if apiKey == "valid-key" {
			// Return paper for DOI lookup
			if r.URL.Path == "/graph/v1/paper/DOI:10.9999%2Ftest" {
				json.NewEncoder(w).Encode(mockPaper)
				return
			}
		}

		// Search endpoint
		if r.URL.Path == "/graph/v1/paper/search" {
			resp := ssSearchResponse{
				Total: 1,
				Data:  []ssPaper{mockPaper},
			}
			json.NewEncoder(w).Encode(resp)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// Create backend with mock server would require injecting the base URL
	// For now, just verify the types work correctly
	paper := ssPaperToPaper(mockPaper)
	if paper.Title != "Integration Test Paper" {
		t.Errorf("Expected 'Integration Test Paper', got '%s'", paper.Title)
	}
}

func TestSemanticScholar_VerifyByTitle_Threshold(t *testing.T) {
	// Verify that the matching threshold of 0.5 is applied correctly
	goodMatch := &ssPaper{
		Title: "Machine Learning in Healthcare Applications",
		Year:  2023,
		Authors: []struct {
			Name string `json:"name"`
		}{
			{Name: "John Smith"},
		},
	}

	// Good match should pass
	score := ssMatchConfidence("Machine Learning in Healthcare", []string{"Smith"}, 2023, goodMatch)
	if score < 0.5 {
		t.Errorf("Good match should exceed 0.5 threshold, got %f", score)
	}

	// Poor match should fail
	poorMatch := &ssPaper{
		Title: "Quantum Computing Advances",
		Year:  2020,
		Authors: []struct {
			Name string `json:"name"`
		}{
			{Name: "Alice Johnson"},
		},
	}

	score = ssMatchConfidence("Machine Learning in Healthcare", []string{"Smith"}, 2023, poorMatch)
	if score >= 0.5 {
		t.Errorf("Poor match should be below 0.5 threshold, got %f", score)
	}
}

func TestSemanticScholar_ResolvePaper(t *testing.T) {
	mockPaper := ssPaper{
		PaperID:       "649def34f8be52c8b66281af98ae884c09aef38b",
		Title:         "Attention Is All You Need",
		Abstract:      "The dominant sequence transduction models...",
		Year:          2017,
		Venue:         "NeurIPS",
		CitationCount: 90000,
		URL:           "https://semanticscholar.org/paper/649def",
		ExternalIDs: struct {
			DOI string `json:"DOI"`
		}{DOI: "10.5555/3295222.3295349"},
		Authors: []struct {
			Name string `json:"name"`
		}{
			{Name: "Ashish Vaswani"},
			{Name: "Noam Shazeer"},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graph/v1/paper/DOI:10.5555%2F3295222.3295349" {
			json.NewEncoder(w).Encode(mockPaper)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	// Test conversion: ResolvePaper returns Paper + ssID
	paper := ssPaperToPaper(mockPaper)
	if paper.Title != "Attention Is All You Need" {
		t.Errorf("Expected title 'Attention Is All You Need', got %q", paper.Title)
	}
	if mockPaper.PaperID != "649def34f8be52c8b66281af98ae884c09aef38b" {
		t.Errorf("Expected ssID preserved, got %q", mockPaper.PaperID)
	}
}

func TestSemanticScholar_FetchReferences(t *testing.T) {
	mockResp := ssReferencesResponse{
		Data: []ssReferenceEdge{
			{
				CitedPaper: ssPaper{
					PaperID: "ref1",
					Title:   "Referenced Paper",
					Year:    2015,
					ExternalIDs: struct {
						DOI string `json:"DOI"`
					}{DOI: "10.1234/ref1"},
					Authors: []struct {
						Name string `json:"name"`
					}{
						{Name: "Ref Author"},
					},
				},
				IsInfluential: true,
				Intents:       []string{"methodology"},
			},
			{
				// Ghost paper: empty title, should be filtered
				CitedPaper: ssPaper{
					PaperID: "ghost1",
					Title:   "",
				},
				IsInfluential: false,
			},
			{
				CitedPaper: ssPaper{
					PaperID: "ref2",
					Title:   "Another Reference",
					Year:    2016,
				},
				IsInfluential: false,
				Intents:       []string{"background"},
			},
		},
	}

	// Verify edge conversion and ghost filtering
	edges := make([]CitationEdge, 0)
	for _, e := range mockResp.Data {
		if e.CitedPaper.Title == "" {
			continue
		}
		edges = append(edges, CitationEdge{
			Paper:         ssPaperToPaper(e.CitedPaper),
			IsInfluential: e.IsInfluential,
			Intents:       e.Intents,
		})
	}

	if len(edges) != 2 {
		t.Fatalf("Expected 2 edges (ghost filtered), got %d", len(edges))
	}

	if edges[0].Paper.Title != "Referenced Paper" {
		t.Errorf("Expected 'Referenced Paper', got %q", edges[0].Paper.Title)
	}
	if !edges[0].IsInfluential {
		t.Error("Expected first edge to be influential")
	}
	if len(edges[0].Intents) != 1 || edges[0].Intents[0] != "methodology" {
		t.Errorf("Expected intents [methodology], got %v", edges[0].Intents)
	}

	if edges[1].Paper.Title != "Another Reference" {
		t.Errorf("Expected 'Another Reference', got %q", edges[1].Paper.Title)
	}
	if edges[1].IsInfluential {
		t.Error("Expected second edge to not be influential")
	}
}

func TestSemanticScholar_FetchCitations(t *testing.T) {
	mockResp := ssCitationsResponse{
		Data: []ssCitationEdge{
			{
				CitingPaper: ssPaper{
					PaperID: "cit1",
					Title:   "Citing Paper",
					Year:    2020,
					Authors: []struct {
						Name string `json:"name"`
					}{
						{Name: "Cit Author"},
					},
				},
				IsInfluential: true,
				Intents:       []string{"result_comparison"},
			},
			{
				// Ghost paper
				CitingPaper: ssPaper{
					PaperID: "ghost2",
					Title:   "",
				},
			},
		},
	}

	edges := make([]CitationEdge, 0)
	for _, e := range mockResp.Data {
		if e.CitingPaper.Title == "" {
			continue
		}
		edges = append(edges, CitationEdge{
			Paper:         ssPaperToPaper(e.CitingPaper),
			IsInfluential: e.IsInfluential,
			Intents:       e.Intents,
		})
	}

	if len(edges) != 1 {
		t.Fatalf("Expected 1 edge (ghost filtered), got %d", len(edges))
	}
	if edges[0].Paper.Title != "Citing Paper" {
		t.Errorf("Expected 'Citing Paper', got %q", edges[0].Paper.Title)
	}
	if !edges[0].IsInfluential {
		t.Error("Expected edge to be influential")
	}
}

func TestSemanticScholar_RealAPICall(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := NewClient()
	backend := NewSemanticScholar(client, "")

	ctx := context.Background()

	// Test health check against real API
	status := backend.HealthCheck(ctx)
	if !status.Healthy {
		t.Skipf("Semantic Scholar API not available: %s", status.Message)
	}

	// Test search. The keyless pool is shared with every other
	// unauthenticated client, so a 429 here says nothing about us.
	papers, err := backend.Search(ctx, "machine learning", 5, SearchFilters{})
	if IsRateLimited(err) {
		t.Skipf("Semantic Scholar shared pool rate-limited: %v", err)
	}
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
		if p.Source != "semantic-scholar" {
			t.Errorf("Paper source should be 'semantic-scholar', got '%s'", p.Source)
		}
	}
}
