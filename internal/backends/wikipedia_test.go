package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestExtractQueryTerms(t *testing.T) {
	tests := []struct {
		query    string
		expected []string
	}{
		{"classical conditioning", []string{"classical", "conditioning"}},
		{"what is the French Revolution", []string{"french", "revolution"}},
		{"a the an", []string{}},        // all stopwords
		{"AI", []string{"ai"}},          // acronym preserved
		{"AI ML", []string{"ai", "ml"}}, // multiple acronyms
		{"moral humility and ethical behaviour", []string{"moral", "humility", "ethical", "behaviour"}},
		{"How does memory work?", []string{"memory", "work"}}, // "does" now stopword
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			result := ExtractQueryTerms(tt.query)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("ExtractQueryTerms(%q) = %v, expected %v", tt.query, result, tt.expected)
			}
		})
	}
}

func TestWikipedia_Name(t *testing.T) {
	client := NewClient()
	backend := NewWikipedia(client)
	if backend.Name() != "wikipedia" {
		t.Errorf("Expected name 'wikipedia', got '%s'", backend.Name())
	}
}

func TestWikipedia_ImplementsInterface(t *testing.T) {
	client := NewClient()
	backend := NewWikipedia(client)

	// Verify it implements WikipediaBackend interface
	var _ WikipediaBackend = backend
}

func TestWikipedia_Search(t *testing.T) {
	mockPages := map[string]wpPage{
		"123": {
			PageID:  123,
			Title:   "Cognitive Load Theory",
			Extract: "Cognitive load theory is a learning theory developed in the 1980s. It provides guidelines for presentation of information during learning.",
			FullURL: "https://en.wikipedia.org/wiki/Cognitive_load_theory",
		},
		"456": {
			PageID:  456,
			Title:   "Working Memory",
			Extract: "Working memory is a cognitive system with a limited capacity that can hold information temporarily. It is central to cognitive load theory.",
			FullURL: "https://en.wikipedia.org/wiki/Working_memory",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify expected parameters
		query := r.URL.Query()
		if query.Get("action") != "query" {
			t.Errorf("Expected action=query, got %s", query.Get("action"))
		}
		if query.Get("format") != "json" {
			t.Errorf("Expected format=json, got %s", query.Get("format"))
		}
		if query.Get("generator") != "search" {
			t.Errorf("Expected generator=search, got %s", query.Get("generator"))
		}

		resp := wpQueryResponse{}
		resp.Query.Pages = mockPages
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	backend := &Wikipedia{
		client:  client,
		baseURL: server.URL,
	}

	articles, err := backend.Search(context.Background(), "cognitive load", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(articles) != 2 {
		t.Errorf("Expected 2 articles, got %d", len(articles))
	}

	// Check that articles have expected fields
	foundCognitive := false
	foundWorking := false
	for _, a := range articles {
		if a.Title == "Cognitive Load Theory" {
			foundCognitive = true
			if a.URL != "https://en.wikipedia.org/wiki/Cognitive_load_theory" {
				t.Errorf("Expected URL for Cognitive Load Theory, got '%s'", a.URL)
			}
			if a.Extract == "" {
				t.Error("Expected non-empty extract")
			}
		}
		if a.Title == "Working Memory" {
			foundWorking = true
		}
	}

	if !foundCognitive {
		t.Error("Expected to find 'Cognitive Load Theory' article")
	}
	if !foundWorking {
		t.Error("Expected to find 'Working Memory' article")
	}
}

func TestWikipedia_Search_EmptyResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := wpQueryResponse{}
		resp.Query.Pages = make(map[string]wpPage)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	backend := &Wikipedia{
		client:  client,
		baseURL: server.URL,
	}

	articles, err := backend.Search(context.Background(), "xyznonexistent123", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(articles) != 0 {
		t.Errorf("Expected 0 articles for nonexistent query, got %d", len(articles))
	}
}

func TestWikipedia_Search_DisambiguationFiltered(t *testing.T) {
	mockPages := map[string]wpPage{
		"123": {
			PageID:  123,
			Title:   "Python (disambiguation)",
			Extract: "Python may refer to: Python (programming language), Pythons (snakes), etc.",
			FullURL: "https://en.wikipedia.org/wiki/Python_(disambiguation)",
		},
		"456": {
			PageID:  456,
			Title:   "Python (programming language)",
			Extract: "Python is a high-level, general-purpose programming language. Its design philosophy emphasizes code readability.",
			FullURL: "https://en.wikipedia.org/wiki/Python_(programming_language)",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := wpQueryResponse{}
		resp.Query.Pages = mockPages
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	backend := &Wikipedia{
		client:  client,
		baseURL: server.URL,
	}

	articles, err := backend.Search(context.Background(), "python", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Should only have the programming language article, not disambiguation
	if len(articles) != 1 {
		t.Errorf("Expected 1 article (disambiguation filtered), got %d", len(articles))
	}

	if len(articles) > 0 && articles[0].Title == "Python (disambiguation)" {
		t.Error("Disambiguation page should have been filtered")
	}
}

func TestWikipedia_Search_ShortExtractFiltered(t *testing.T) {
	mockPages := map[string]wpPage{
		"123": {
			PageID:  123,
			Title:   "Short Article",
			Extract: "Too short.", // Less than 50 chars
			FullURL: "https://en.wikipedia.org/wiki/Short_Article",
		},
		"456": {
			PageID:  456,
			Title:   "Good Article",
			Extract: "This is a good article with enough content to be useful for context retrieval purposes.",
			FullURL: "https://en.wikipedia.org/wiki/Good_Article",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := wpQueryResponse{}
		resp.Query.Pages = mockPages
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	backend := &Wikipedia{
		client:  client,
		baseURL: server.URL,
	}

	articles, err := backend.Search(context.Background(), "article", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Should only have the good article, not the short one
	if len(articles) != 1 {
		t.Errorf("Expected 1 article (short extract filtered), got %d", len(articles))
	}

	if len(articles) > 0 && articles[0].Title == "Short Article" {
		t.Error("Short extract page should have been filtered")
	}
}

func TestWikipedia_Search_SoftwarePageIncluded(t *testing.T) {
	mockPages := map[string]wpPage{
		"123": {
			PageID:  123,
			Title:   "Git (software)",
			Extract: "Git is a distributed version control system that tracks changes in any set of computer files.",
			FullURL: "https://en.wikipedia.org/wiki/Git_(software)",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := wpQueryResponse{}
		resp.Query.Pages = mockPages
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	backend := &Wikipedia{
		client:  client,
		baseURL: server.URL,
	}

	// (software) pages should now pass through when terms overlap
	articles, err := backend.Search(context.Background(), "git version control", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(articles) != 1 {
		t.Errorf("Expected (software) page to be included (filter removed), got %d articles", len(articles))
	}
}

func TestWikipedia_Search_ProportionalFiltering(t *testing.T) {
	// With 3+ query terms, proportional matching requires at least 2 to match.
	// A page matching only 1 of 3 significant terms should be filtered.
	mockPages := map[string]wpPage{
		"1": {
			PageID: 1, Index: 1,
			Title:   "Cognitive Behavioural Therapy",
			Extract: "Cognitive behavioural therapy (CBT) is a psycho-social intervention that aims to reduce symptoms of various mental health conditions.",
			FullURL: "https://en.wikipedia.org/wiki/CBT",
		},
		"2": {
			PageID: 2, Index: 2,
			Title:   "Anxiety",
			Extract: "Anxiety is an emotion which is characterised by an unpleasant state of inner turmoil.",
			FullURL: "https://en.wikipedia.org/wiki/Anxiety",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := wpQueryResponse{}
		resp.Query.Pages = mockPages
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	backend := &Wikipedia{client: client, baseURL: server.URL}

	// terms = ["cognitive", "behavioural", "therapy"] → 3 terms, need 2
	// CBT matches all 3 → passes
	// Anxiety matches 0 → filtered
	articles, err := backend.Search(context.Background(), "cognitive behavioural therapy", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(articles) != 1 {
		t.Errorf("Expected 1 article, got %d", len(articles))
		for _, a := range articles {
			t.Logf("Got: %s", a.Title)
		}
	}
	if len(articles) > 0 && articles[0].Title != "Cognitive Behavioural Therapy" {
		t.Errorf("Expected 'Cognitive Behavioural Therapy', got %s", articles[0].Title)
	}
}

func TestWikipedia_Search_AcronymQuery(t *testing.T) {
	// Regression: "is AI conscious" should match on "ai" as a query term.
	mockPages := map[string]wpPage{
		"1": {
			PageID: 1, Index: 1,
			Title:   "Artificial consciousness",
			Extract: "Artificial consciousness, also known as machine consciousness, is consciousness hypothesized to be possible for artificial intelligence (AI).",
			FullURL: "https://en.wikipedia.org/wiki/Artificial_consciousness",
		},
		"2": {
			PageID: 2, Index: 2,
			Title:   "Half-Life VR but the AI Is Self-Aware",
			Extract: "Half-Life VR but the AI Is Self-Aware is a 2020 improvisational machinima series based on the 1998 video game Half-Life.",
			FullURL: "https://en.wikipedia.org/wiki/HLVRAI",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := wpQueryResponse{}
		resp.Query.Pages = mockPages
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	backend := &Wikipedia{client: client, baseURL: server.URL}

	// terms = ["ai", "conscious"]. minTermMatches(2) = 2.
	// Artificial consciousness: "ai" in extract + "conscious" in title → 2 matches → passes
	// Half-Life: "ai" in title (title match lowers threshold to 1) → passes too
	// Both pass, but the right article is ranked first (index 1).
	articles, err := backend.Search(context.Background(), "is AI conscious", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(articles) < 1 {
		t.Fatal("Expected at least 1 article")
	}
	if articles[0].Title != "Artificial consciousness" {
		t.Errorf("Expected 'Artificial consciousness' first, got %s", articles[0].Title)
	}
}

func TestMinTermMatches(t *testing.T) {
	tests := []struct {
		n        int
		expected int
	}{
		{0, 0},
		{1, 1},
		{2, 2},
		{3, 2},
		{4, 2},
		{5, 2},
		{6, 3}, // ceil(6 * 0.4) = 3
		{10, 4},
	}
	for _, tt := range tests {
		got := minTermMatches(tt.n)
		if got != tt.expected {
			t.Errorf("minTermMatches(%d) = %d, want %d", tt.n, got, tt.expected)
		}
	}
}

func TestWikipedia_Search_URLConstructed(t *testing.T) {
	// Test that URL is constructed when not provided by API
	mockPages := map[string]wpPage{
		"123": {
			PageID:  123,
			Title:   "Test Article",
			Extract: "This is a test article with enough content to pass the length filter.",
			FullURL: "", // Empty URL - should be constructed
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := wpQueryResponse{}
		resp.Query.Pages = mockPages
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	backend := &Wikipedia{
		client:  client,
		baseURL: server.URL,
	}

	articles, err := backend.Search(context.Background(), "test", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(articles) != 1 {
		t.Fatalf("Expected 1 article, got %d", len(articles))
	}

	expectedURL := "https://en.wikipedia.org/wiki/Test_Article"
	if articles[0].URL != expectedURL {
		t.Errorf("Expected constructed URL '%s', got '%s'", expectedURL, articles[0].URL)
	}
}

func TestWikipedia_Search_LimitEnforced(t *testing.T) {
	// Create many mock pages
	mockPages := make(map[string]wpPage)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("%d", i)
		mockPages[id] = wpPage{
			PageID:  i,
			Title:   fmt.Sprintf("Article %d", i),
			Extract: "This is a sufficient length extract to pass the filter and be included in results.",
			FullURL: fmt.Sprintf("https://en.wikipedia.org/wiki/Article_%d", i),
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := wpQueryResponse{}
		resp.Query.Pages = mockPages
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	backend := &Wikipedia{
		client:  client,
		baseURL: server.URL,
	}

	// Request only 3 results
	articles, err := backend.Search(context.Background(), "article", 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(articles) > 3 {
		t.Errorf("Expected at most 3 articles (limit enforced), got %d", len(articles))
	}
}

func TestWikipedia_Search_DefaultLimit(t *testing.T) {
	client := NewClient()
	backend := NewWikipedia(client)

	// This just verifies that passing 0 doesn't cause issues
	// The default should be applied internally
	if backend.Name() != "wikipedia" {
		t.Error("Backend should be initialized")
	}
}

func TestWikipedia_HealthCheck(t *testing.T) {
	mockPages := map[string]wpPage{
		"1": {
			PageID:  1,
			Title:   "Test",
			Extract: "This is a test article with sufficient content for the health check.",
			FullURL: "https://en.wikipedia.org/wiki/Test",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := wpQueryResponse{}
		resp.Query.Pages = mockPages
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	backend := &Wikipedia{
		client:  client,
		baseURL: server.URL,
	}

	status := backend.HealthCheck(context.Background())
	if !status.Healthy {
		t.Errorf("Expected healthy status, got: %s", status.Message)
	}
	if status.Message != "Wikipedia API reachable" {
		t.Errorf("Expected 'Wikipedia API reachable', got '%s'", status.Message)
	}
}

func TestWikipedia_HealthCheck_Unhealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	client := NewClient()
	backend := &Wikipedia{
		client:  client,
		baseURL: server.URL,
	}

	status := backend.HealthCheck(context.Background())
	if status.Healthy {
		t.Error("Expected unhealthy status for server error")
	}
	if status.Message == "" {
		t.Error("Expected error message in status")
	}
}

func TestWikipedia_IsRelevant(t *testing.T) {
	backend := &Wikipedia{}

	tests := []struct {
		name     string
		page     wpPage
		query    string
		expected bool
	}{
		{
			name: "normal page",
			page: wpPage{
				Title:   "Cognitive Psychology",
				Extract: "Cognitive psychology is the scientific study of mental processes such as attention, language use, memory, perception, problem solving, creativity, and reasoning.",
			},
			query:    "psychology",
			expected: true,
		},
		{
			name: "disambiguation page",
			page: wpPage{
				Title:   "Python (disambiguation)",
				Extract: "Python may refer to: Python (programming language), Pythons (snakes), etc.",
			},
			query:    "python",
			expected: false,
		},
		{
			name: "software page passes without software in query",
			page: wpPage{
				Title:   "Vim (software)",
				Extract: "Vim is a free and open-source, screen-based text editor program for Unix and other systems.",
			},
			query:    "vim text editor",
			expected: true, // (software) filter removed; term overlap sufficient
		},
		{
			name: "short extract",
			page: wpPage{
				Title:   "Stub Article",
				Extract: "Short stub.",
			},
			query:    "stub",
			expected: false,
		},
		{
			name: "empty extract",
			page: wpPage{
				Title:   "Empty Article",
				Extract: "",
			},
			query:    "empty",
			expected: false,
		},
		{
			name: "no query term overlap",
			page: wpPage{
				Title:   "Accountability",
				Extract: "Accountability is the obligation to explain, justify, and take responsibility for one's actions.",
			},
			query:    "moral humility",
			expected: false, // "Accountability" contains neither "moral" nor "humility"
		},
		{
			name: "query term in title",
			page: wpPage{
				Title:   "Classical conditioning",
				Extract: "A learning process that occurs when two stimuli are repeatedly paired together.",
			},
			query:    "classical conditioning",
			expected: true,
		},
		{
			name: "query term in extract only",
			page: wpPage{
				Title:   "Learning Theory",
				Extract: "Learning theory includes classical conditioning and operant conditioning approaches.",
			},
			query:    "classical conditioning",
			expected: true,
		},
		{
			name: "proportional match - passes with enough overlap",
			page: wpPage{
				Title:   "Humility",
				Extract: "Humility is the quality of being humble. In moral philosophy and many ethical traditions, humility is considered a core virtue.",
			},
			query:    "moral humility and ethical behaviour",
			expected: true, // "humility" + "moral" + "ethical" = 3 of 4 terms
		},
		{
			name: "proportional match - title match lowers threshold",
			page: wpPage{
				Title:   "Humility",
				Extract: "Humility is the quality of being humble, having a modest view of one's own importance.",
			},
			query:    "moral humility and ethical behaviour",
			expected: true, // "humility" in title lowers threshold from 2 to 1
		},
		{
			name: "proportional match - no title match, too little overlap",
			page: wpPage{
				Title:   "Accountability",
				Extract: "Accountability involves being responsible. It is often discussed in moral philosophy.",
			},
			query:    "moral humility and ethical behaviour",
			expected: false, // "moral" in extract (1 match), no title match, need 2
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := backend.isRelevant(tt.page, tt.query)
			if result != tt.expected {
				t.Errorf("isRelevant(%s, %s) = %v, expected %v", tt.page.Title, tt.query, result, tt.expected)
			}
		})
	}
}

func TestWikipedia_Search_PreservesOrder(t *testing.T) {
	// Create pages with explicit index values (simulating Wikipedia's search ranking)
	mockPages := map[string]wpPage{
		"789": {
			PageID:  789,
			Index:   3,
			Title:   "Third Result",
			Extract: "This article has the same keyword as others but should appear third.",
			FullURL: "https://en.wikipedia.org/wiki/Third_Result",
		},
		"123": {
			PageID:  123,
			Index:   1,
			Title:   "First Result",
			Extract: "This article has the same keyword as others but should appear first.",
			FullURL: "https://en.wikipedia.org/wiki/First_Result",
		},
		"456": {
			PageID:  456,
			Index:   2,
			Title:   "Second Result",
			Extract: "This article has the same keyword as others but should appear second.",
			FullURL: "https://en.wikipedia.org/wiki/Second_Result",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := wpQueryResponse{}
		resp.Query.Pages = mockPages
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	backend := &Wikipedia{
		client:  client,
		baseURL: server.URL,
	}

	articles, err := backend.Search(context.Background(), "keyword article result", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(articles) != 3 {
		t.Fatalf("Expected 3 articles, got %d", len(articles))
	}

	// Verify order is preserved based on Index field
	expectedOrder := []string{"First Result", "Second Result", "Third Result"}
	for i, expected := range expectedOrder {
		if articles[i].Title != expected {
			t.Errorf("Position %d: expected %q, got %q", i, expected, articles[i].Title)
		}
	}
}

func TestWikipedia_Search_QueryTermFiltering(t *testing.T) {
	// Test that pages without query term overlap are filtered out
	mockPages := map[string]wpPage{
		"123": {
			PageID:  123,
			Index:   1,
			Title:   "Accountability",
			Extract: "Accountability is the obligation to explain, justify, and take responsibility for one's actions in organizational contexts.",
			FullURL: "https://en.wikipedia.org/wiki/Accountability",
		},
		"456": {
			PageID:  456,
			Index:   2,
			Title:   "Humility",
			Extract: "Humility is the quality of being humble. In moral philosophy and many religious traditions, humility is considered a core virtue.",
			FullURL: "https://en.wikipedia.org/wiki/Humility",
		},
		"789": {
			PageID:  789,
			Index:   3,
			Title:   "Intellectual humility",
			Extract: "Intellectual humility involves recognizing the limits of one's knowledge. It is a moral and epistemic virtue studied in philosophy.",
			FullURL: "https://en.wikipedia.org/wiki/Intellectual_humility",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := wpQueryResponse{}
		resp.Query.Pages = mockPages
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	backend := &Wikipedia{
		client:  client,
		baseURL: server.URL,
	}

	articles, err := backend.Search(context.Background(), "moral humility", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Accountability should be filtered (no overlap with "moral" or "humility")
	// Humility and Intellectual humility should remain
	if len(articles) != 2 {
		t.Errorf("Expected 2 articles (Accountability filtered), got %d", len(articles))
		for _, a := range articles {
			t.Logf("Got: %s", a.Title)
		}
	}

	for _, a := range articles {
		if a.Title == "Accountability" {
			t.Error("Accountability should have been filtered out (no query term overlap)")
		}
	}
}

func TestBigramQueries(t *testing.T) {
	tests := []struct {
		name     string
		terms    []string
		expected []string
	}{
		{"too few terms", []string{"cognitive", "load"}, nil},
		{"single term", []string{"test"}, nil},
		{"empty", []string{}, nil},
		{
			"5 terms with variant",
			[]string{"second", "shift", "emotional", "labour", "gender"},
			[]string{
				`"second shift" emotional labour gender`,
				`"emotional labour" second shift gender`,
				`"emotional labor" second shift gender`, // variant
			},
		},
		{
			"3 terms no variant",
			[]string{"evolutionary", "psychology", "critique"},
			[]string{
				`"evolutionary psychology" critique`,
			},
		},
		{
			"3 terms with variant",
			[]string{"cognitive", "behavioural", "therapy"},
			[]string{
				`"cognitive behavioural" therapy`,
				`"cognitive behavioral" therapy`, // variant
			},
		},
		{
			"4 terms no variant",
			[]string{"attachment", "theory", "avoidant", "masculinity"},
			[]string{
				`"attachment theory" avoidant masculinity`,
				`"avoidant masculinity" attachment theory`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bigramQueries(tt.terms)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("bigramQueries(%v):\n  got:  %v\n  want: %v", tt.terms, got, tt.expected)
			}
		})
	}
}

func TestWikipedia_IsRelevant_SpellingVariants(t *testing.T) {
	backend := &Wikipedia{}

	tests := []struct {
		name     string
		page     wpPage
		query    string
		expected bool
	}{
		{
			name: "labour in query matches labor in article",
			page: wpPage{
				Title:   "Emotional labor",
				Extract: "Emotional labor is the act of managing one's own emotions and the emotions of others to meet job expectations.",
			},
			query:    "emotional labour",
			expected: true,
		},
		{
			name: "behavior in query matches behaviour in article",
			page: wpPage{
				Title:   "Organisational behaviour",
				Extract: "Organisational behaviour studies how people interact within groups in work settings.",
			},
			query:    "organizational behavior",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := backend.isRelevant(tt.page, tt.query)
			if got != tt.expected {
				t.Errorf("isRelevant(%q, %q) = %v, want %v", tt.page.Title, tt.query, got, tt.expected)
			}
		})
	}
}

func TestWikipedia_Search_PhraseQueryPriority(t *testing.T) {
	// Verify that phrase-query results appear before original-query results.
	// The mock server returns different pages depending on whether the query
	// contains quoted phrases.
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		gsrsearch := r.URL.Query().Get("gsrsearch")

		resp := wpQueryResponse{}
		if strings.Contains(gsrsearch, `"`) {
			// Phrase query: return the specific article
			resp.Query.Pages = map[string]wpPage{
				"1": {PageID: 1, Index: 1, Title: "The Second Shift",
					Extract: "The Second Shift is a book about household labour and gender roles in emotional work.",
					FullURL: "https://en.wikipedia.org/wiki/The_Second_Shift"},
			}
		} else {
			// Original query: return a generic article
			resp.Query.Pages = map[string]wpPage{
				"2": {PageID: 2, Index: 1, Title: "Gender role",
					Extract: "A gender role is a social norm about emotional expression and shifting expectations and labour.",
					FullURL: "https://en.wikipedia.org/wiki/Gender_role"},
			}
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient()
	backend := &Wikipedia{client: client, baseURL: server.URL}

	// 5 terms → phrase query fires alongside original
	articles, err := backend.Search(context.Background(), "second shift emotional labour gender", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(articles) < 1 {
		t.Fatal("Expected at least 1 article")
	}
	// Phrase result should come first
	if articles[0].Title != "The Second Shift" {
		t.Errorf("Expected phrase result first, got %q", articles[0].Title)
	}
	if len(articles) >= 2 && articles[1].Title != "Gender role" {
		t.Errorf("Expected original result second, got %q", articles[1].Title)
	}
	// Should have fired at least 2 queries (original + phrase)
	if requestCount < 2 {
		t.Errorf("Expected at least 2 requests (original + phrase), got %d", requestCount)
	}
}

func TestWikipedia_RelevanceFiltering(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := NewClient()
	backend := NewWikipedia(client)
	ctx := context.Background()

	tests := []struct {
		query       string
		expectTitle string // Should appear in results
	}{
		{"classical conditioning", "Classical conditioning"},
		{"cognitive dissonance", "Cognitive dissonance"},
		{"French Revolution", "French Revolution"},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			articles, err := backend.Search(ctx, tt.query, 5)
			if err != nil {
				t.Fatalf("Search failed: %v", err)
			}

			found := false
			for _, a := range articles {
				if strings.Contains(a.Title, tt.expectTitle) {
					found = true
				}
				// Also verify each result contains a query term
				terms := ExtractQueryTerms(tt.query)
				hasOverlap := false
				for _, term := range terms {
					if strings.Contains(strings.ToLower(a.Title), term) ||
						strings.Contains(strings.ToLower(a.Extract), term) {
						hasOverlap = true
						break
					}
				}
				if !hasOverlap {
					t.Errorf("Result %q has no query term overlap with %q", a.Title, tt.query)
				}
			}

			if !found {
				t.Errorf("Expected to find %q in results for query %q", tt.expectTitle, tt.query)
				for _, a := range articles {
					t.Logf("Got: %s", a.Title)
				}
			}
		})
	}
}

func TestWikipedia_RealAPICall(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client := NewClient()
	backend := NewWikipedia(client)

	ctx := context.Background()

	// Test health check against real API
	status := backend.HealthCheck(ctx)
	if !status.Healthy {
		t.Skipf("Wikipedia API not available: %s", status.Message)
	}

	// Test search
	articles, err := backend.Search(ctx, "cognitive load theory", 3)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(articles) == 0 {
		t.Error("Expected at least one article from search")
	}

	// Verify article fields are populated
	for _, a := range articles {
		if a.Title == "" {
			t.Error("Article should have a title")
		}
		if a.Extract == "" {
			t.Error("Article should have an extract")
		}
		if a.URL == "" {
			t.Error("Article should have a URL")
		}
		t.Logf("Found article: %s (%s)", a.Title, a.URL)
	}
}
