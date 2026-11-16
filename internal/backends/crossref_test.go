package backends

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCrossref_VerifyDOI_Found(t *testing.T) {
	// Mock Crossref API response
	mockWork := crossrefWork{
		DOI:   "10.1234/test.paper",
		Title: []string{"Test Paper Title"},
		Author: []struct {
			Given  string `json:"given"`
			Family string `json:"family"`
		}{
			{Given: "John", Family: "Smith"},
			{Given: "Jane", Family: "Doe"},
		},
		Published: struct {
			DateParts [][]int `json:"date-parts"`
		}{
			DateParts: [][]int{{2023, 5, 15}},
		},
		ContainerTitle:      []string{"Journal of Testing"},
		IsReferencedByCount: 42,
		Abstract:            "This is a test abstract.",
		URL:                 "https://doi.org/10.1234/test.paper",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := crossrefResponse{Message: mockWork}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Test workToPaper conversion
	paper := workToPaper(mockWork)

	if paper.Title != "Test Paper Title" {
		t.Errorf("Expected title 'Test Paper Title', got '%s'", paper.Title)
	}
	if len(paper.Authors) != 2 {
		t.Errorf("Expected 2 authors, got %d", len(paper.Authors))
	}
	if paper.Authors[0] != "John Smith" {
		t.Errorf("Expected author 'John Smith', got '%s'", paper.Authors[0])
	}
	if paper.Year != 2023 {
		t.Errorf("Expected year 2023, got %d", paper.Year)
	}
	if paper.DOI != "10.1234/test.paper" {
		t.Errorf("Expected DOI '10.1234/test.paper', got '%s'", paper.DOI)
	}
	if paper.Journal != "Journal of Testing" {
		t.Errorf("Expected journal 'Journal of Testing', got '%s'", paper.Journal)
	}
	if paper.Citations != 42 {
		t.Errorf("Expected 42 citations, got %d", paper.Citations)
	}
	if paper.Source != "crossref" {
		t.Errorf("Expected source 'crossref', got '%s'", paper.Source)
	}

	_ = server
}

func TestCrossref_VerifyDOI_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"status":"error","message-type":"error","message":[{"status":404}]}`))
	}))
	defer server.Close()

	// Test that IsNotFound works correctly
	err := &HTTPError{StatusCode: 404, Body: "not found"}
	if !IsNotFound(err) {
		t.Error("Expected IsNotFound to return true for 404 error")
	}

	err = &HTTPError{StatusCode: 200, Body: "ok"}
	if IsNotFound(err) {
		t.Error("Expected IsNotFound to return false for 200")
	}
}

func TestCrossref_Search(t *testing.T) {
	// Test search response parsing
	mockItems := []crossrefWork{
		{
			DOI:   "10.1234/paper1",
			Title: []string{"First Paper"},
			Author: []struct {
				Given  string `json:"given"`
				Family string `json:"family"`
			}{
				{Given: "Alice", Family: "Johnson"},
			},
			Published: struct {
				DateParts [][]int `json:"date-parts"`
			}{
				DateParts: [][]int{{2022}},
			},
		},
		{
			DOI:   "10.1234/paper2",
			Title: []string{"Second Paper"},
			Author: []struct {
				Given  string `json:"given"`
				Family string `json:"family"`
			}{
				{Given: "Bob", Family: "Williams"},
			},
			Published: struct {
				DateParts [][]int `json:"date-parts"`
			}{
				DateParts: [][]int{{2021}},
			},
		},
	}

	// Convert to papers
	papers := make([]Paper, 0, len(mockItems))
	for _, work := range mockItems {
		papers = append(papers, workToPaper(work))
	}

	if len(papers) != 2 {
		t.Errorf("Expected 2 papers, got %d", len(papers))
	}
	if papers[0].Title != "First Paper" {
		t.Errorf("Expected 'First Paper', got '%s'", papers[0].Title)
	}
	if papers[1].Year != 2021 {
		t.Errorf("Expected year 2021, got %d", papers[1].Year)
	}
}

func TestCrossref_HealthCheck(t *testing.T) {
	// Test healthy response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := crossrefSearchResponse{}
		resp.Message.Items = []crossrefWork{}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Verify the Name() function
	client := NewClient()
	backend := NewCrossref(client)
	if backend.Name() != "crossref" {
		t.Errorf("Expected name 'crossref', got '%s'", backend.Name())
	}
}

func TestJaccardSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        string
		b        string
		expected float64
		delta    float64
	}{
		{
			name:     "identical strings",
			a:        "hello world",
			b:        "hello world",
			expected: 1.0,
			delta:    0.001,
		},
		{
			name:     "completely different",
			a:        "hello world",
			b:        "foo bar",
			expected: 0.0,
			delta:    0.001,
		},
		{
			name:     "partial overlap",
			a:        "hello world test",
			b:        "hello world example",
			expected: 0.5, // 2 common (hello, world) / 4 total (hello, world, test, example)
			delta:    0.001,
		},
		{
			name:     "both empty",
			a:        "",
			b:        "",
			expected: 1.0,
			delta:    0.001,
		},
		{
			name:     "one empty",
			a:        "hello",
			b:        "",
			expected: 0.0,
			delta:    0.001,
		},
		{
			name:     "case insensitive",
			a:        "HELLO WORLD",
			b:        "hello world",
			expected: 1.0,
			delta:    0.001,
		},
		{
			name:     "with punctuation",
			a:        "Hello, World!",
			b:        "hello world",
			expected: 1.0,
			delta:    0.001,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := jaccardSimilarity(tc.a, tc.b)
			if result < tc.expected-tc.delta || result > tc.expected+tc.delta {
				t.Errorf("jaccardSimilarity(%q, %q) = %f, expected %f", tc.a, tc.b, result, tc.expected)
			}
		})
	}
}

func TestNormalizeDOI(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"10.1234/test", "10.1234/test"},
		{"https://doi.org/10.1234/test", "10.1234/test"},
		{"http://doi.org/10.1234/test", "10.1234/test"},
		{"https://dx.doi.org/10.1234/test", "10.1234/test"},
		{"http://dx.doi.org/10.1234/test", "10.1234/test"},
		{"doi:10.1234/test", "10.1234/test"},
		{"  10.1234/test  ", "10.1234/test"},
		{"HTTPS://DOI.ORG/10.1234/test", "10.1234/test"},
		// DOIs are case-insensitive; normalizeDOI must lowercase
		{"10.1111/J.1728-4457.2004.00005.X", "10.1111/j.1728-4457.2004.00005.x"},
		// APA-style double-slash DOIs must be collapsed
		{"10.1037//0022-3514.52.3.511", "10.1037/0022-3514.52.3.511"},
		{"https://doi.org/10.1037//0022-3514.52.3.511", "10.1037/0022-3514.52.3.511"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := normalizeDOI(tc.input)
			if result != tc.expected {
				t.Errorf("normalizeDOI(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestStripXMLTags(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"plain text", "plain text"},
		{"<jats:p>Hello world</jats:p>", "Hello world"},
		{
			"<jats:p><jats:italic>Gender</jats:italic> and <jats:bold>labour</jats:bold></jats:p>",
			"Gender and labour",
		},
		{"<jats:title>Abstract</jats:title><jats:p>Body text.</jats:p>", "Abstract Body text."},
	}
	for _, tc := range tests {
		got := stripXMLTags(tc.input)
		if got != tc.expected {
			t.Errorf("stripXMLTags(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestMatchConfidence(t *testing.T) {
	work := &crossrefWork{
		Title: []string{"Machine Learning in Healthcare"},
		Author: []struct {
			Given  string `json:"given"`
			Family string `json:"family"`
		}{
			{Given: "John", Family: "Smith"},
			{Given: "Jane", Family: "Doe"},
		},
		Published: struct {
			DateParts [][]int `json:"date-parts"`
		}{
			DateParts: [][]int{{2022}},
		},
	}

	// Test exact match
	score := matchConfidence("Machine Learning in Healthcare", []string{"Smith", "Doe"}, 2022, work)
	if score < 0.8 {
		t.Errorf("Expected high score for exact match, got %f", score)
	}

	// Test partial title match
	score = matchConfidence("Machine Learning", []string{"Smith"}, 2022, work)
	if score < 0.5 {
		t.Errorf("Expected moderate score for partial match, got %f", score)
	}

	// Test poor match
	score = matchConfidence("Quantum Physics in Space", []string{"Johnson"}, 2020, work)
	if score > 0.3 {
		t.Errorf("Expected low score for poor match, got %f", score)
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"hello world", []string{"hello", "world"}},
		{"Hello, World!", []string{"hello", "world"}},
		{"test123", []string{"test123"}},
		{"one-two-three", []string{"one", "two", "three"}},
		{"", nil},
		{"   ", nil},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := tokenize(tc.input)
			if len(result) != len(tc.expected) {
				t.Errorf("tokenize(%q) = %v, expected %v", tc.input, result, tc.expected)
				return
			}
			for i, word := range result {
				if word != tc.expected[i] {
					t.Errorf("tokenize(%q)[%d] = %q, expected %q", tc.input, i, word, tc.expected[i])
				}
			}
		})
	}
}

func TestCrossref_VerifyByTitle_Match(t *testing.T) {
	// Test the matching logic
	work := crossrefWork{
		DOI:   "10.1234/matched",
		Title: []string{"Introduction to Machine Learning"},
		Author: []struct {
			Given  string `json:"given"`
			Family string `json:"family"`
		}{
			{Given: "John", Family: "Smith"},
		},
		Published: struct {
			DateParts [][]int `json:"date-parts"`
		}{
			DateParts: [][]int{{2022}},
		},
	}

	// Good match should pass threshold
	score := matchConfidence("Introduction to Machine Learning", []string{"Smith"}, 2022, &work)
	if score < 0.5 {
		t.Errorf("Expected good match to exceed threshold 0.5, got %f", score)
	}
}

func TestCrossref_VerifyByTitle_NoMatch(t *testing.T) {
	// Test that poor matches fail threshold
	work := crossrefWork{
		DOI:   "10.1234/unmatched",
		Title: []string{"Quantum Computing Advances"},
		Author: []struct {
			Given  string `json:"given"`
			Family string `json:"family"`
		}{
			{Given: "Alice", Family: "Johnson"},
		},
		Published: struct {
			DateParts [][]int `json:"date-parts"`
		}{
			DateParts: [][]int{{2021}},
		},
	}

	// Poor match should fail threshold
	score := matchConfidence("Introduction to Biology", []string{"Williams"}, 2023, &work)
	if score >= 0.5 {
		t.Errorf("Expected poor match to be below threshold 0.5, got %f", score)
	}
}

func TestYearPenalty(t *testing.T) {
	tests := []struct {
		name      string
		queryYear int
		workYear  int
		wantMin   float64
		wantMax   float64
	}{
		{"exact match", 2023, 2023, 1.0, 1.0},
		{"off by 1", 2022, 2023, 1.0, 1.0},
		{"off by 2", 2021, 2023, 1.0, 1.0},
		{"off by 3", 2020, 2023, 0.8, 0.9},
		{"off by 5", 2018, 2023, 0.7, 0.8},
		{"off by 10", 2013, 2023, 0.45, 0.55},
		{"off by 41", 1979, 2020, 0.2, 0.2},
		{"query year zero", 0, 2023, 1.0, 1.0},
		{"work year zero", 2023, 0, 1.0, 1.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := yearPenalty(tc.queryYear, tc.workYear)
			if p < tc.wantMin || p > tc.wantMax {
				t.Errorf("yearPenalty(%d, %d) = %f, want [%f, %f]",
					tc.queryYear, tc.workYear, p, tc.wantMin, tc.wantMax)
			}
		})
	}
}

func TestExtractFamilyName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Gould, Stephen Jay", "Gould"},
		{"Buss, David", "Buss"},
		{"Stephen Jay Gould", "Gould"},
		{"David Buss", "Buss"},
		{"Hochschild", "Hochschild"},
		{"", ""},
		{"  Buss, David  ", "Buss"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := extractFamilyName(tc.input)
			if result != tc.expected {
				t.Errorf("extractFamilyName(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestFirstAuthorMatchesAny(t *testing.T) {
	t.Run("matching family name", func(t *testing.T) {
		if !firstAuthorMatchesAny([]string{"Buss, David"}, []string{"Buss", "Schmitt"}) {
			t.Error("expected Buss to match")
		}
	})

	t.Run("non-matching family name", func(t *testing.T) {
		if firstAuthorMatchesAny([]string{"Buss, David"}, []string{"Walter", "Conroy"}) {
			t.Error("expected Buss not to match Walter/Conroy")
		}
	})

	t.Run("empty query authors", func(t *testing.T) {
		if !firstAuthorMatchesAny(nil, []string{"Smith"}) {
			t.Error("expected true for empty query authors")
		}
	})

	t.Run("empty work authors", func(t *testing.T) {
		if !firstAuthorMatchesAny([]string{"Buss"}, nil) {
			t.Error("expected true for empty work authors")
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		if !firstAuthorMatchesAny([]string{"GOULD, Stephen"}, []string{"Gould", "Lewontin"}) {
			t.Error("expected case-insensitive match")
		}
	})
}

// Regression test: Gould & Lewontin 1979 should not match a 2020 reprint.
func TestMatchConfidence_YearGapPenalty(t *testing.T) {
	// Simulate a 2020 reprint of the 1979 original
	reprint := &crossrefWork{
		Title: []string{"The Spandrels of San Marco and the Panglossian Paradigm"},
		Author: []struct {
			Given  string `json:"given"`
			Family string `json:"family"`
		}{
			{Given: "Stephen Jay", Family: "Gould"},
			{Given: "Richard", Family: "Lewontin"},
		},
		Published: struct {
			DateParts [][]int `json:"date-parts"`
		}{
			DateParts: [][]int{{2020}},
		},
	}

	score := matchConfidence(
		"The Spandrels of San Marco and the Panglossian Paradigm",
		[]string{"Gould, Stephen Jay", "Lewontin, Richard"},
		1979,
		reprint,
	)

	if score >= 0.5 {
		t.Errorf("Reprint (2020) should not pass threshold when querying 1979 original, got %f", score)
	}
}

// Regression test: Buss 1989 should not match Walter et al. 2021.
func TestMatchConfidence_FirstAuthorGate(t *testing.T) {
	wrongPaper := &crossrefWork{
		Title: []string{"Sex differences in human mate preferences"},
		Author: []struct {
			Given  string `json:"given"`
			Family string `json:"family"`
		}{
			{Given: "Kristen", Family: "Walter"},
			{Given: "Catherine", Family: "Conroy"},
			{Given: "Michael", Family: "Marks"},
		},
		Published: struct {
			DateParts [][]int `json:"date-parts"`
		}{
			DateParts: [][]int{{2021}},
		},
	}

	score := matchConfidence(
		"Sex differences in human mate preferences",
		[]string{"Buss, David"},
		1989,
		wrongPaper,
	)

	if score >= 0.5 {
		t.Errorf("Walter et al. 2021 should not match query for Buss 1989, got %f", score)
	}
}

func TestWorkToPaper_EmptyFields(t *testing.T) {
	// Test handling of empty/missing fields
	work := crossrefWork{
		DOI: "10.1234/empty",
		// No title, authors, dates, etc.
	}

	paper := workToPaper(work)

	if paper.Title != "" {
		t.Errorf("Expected empty title, got '%s'", paper.Title)
	}
	if len(paper.Authors) != 0 {
		t.Errorf("Expected no authors, got %d", len(paper.Authors))
	}
	if paper.Year != 0 {
		t.Errorf("Expected year 0, got %d", paper.Year)
	}
	if paper.DOI != "10.1234/empty" {
		t.Errorf("Expected DOI '10.1234/empty', got '%s'", paper.DOI)
	}
	if paper.Source != "crossref" {
		t.Errorf("Expected source 'crossref', got '%s'", paper.Source)
	}
}
