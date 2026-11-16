package tools

import (
	"strings"
	"testing"

	"btr.mt/grounding/internal/backends"
)

func TestRelevanceScore(t *testing.T) {
	tests := []struct {
		name     string
		terms    []string
		paper    backends.Paper
		minScore float64
		maxScore float64
	}{
		{
			name:  "empty terms returns 1.0",
			terms: []string{},
			paper: backends.Paper{
				Title:    "Any title",
				Abstract: "Any abstract",
			},
			minScore: 1.0,
			maxScore: 1.0,
		},
		{
			name:  "title match gives high score",
			terms: []string{"humility"},
			paper: backends.Paper{
				Title:    "Moral Humility in Leadership",
				Abstract: "This paper explores leadership.",
			},
			minScore: 0.20, // 1 title match * 0.25
			maxScore: 0.30,
		},
		{
			name:  "abstract match gives lower score",
			terms: []string{"humility"},
			paper: backends.Paper{
				Title:    "Moral Virtues in Leadership",
				Abstract: "We examine humility as a key trait.",
			},
			minScore: 0.1, // 0.15 / 1 term
			maxScore: 0.2,
		},
		{
			name:  "both title and abstract match",
			terms: []string{"humility"},
			paper: backends.Paper{
				Title:    "The Role of Humility",
				Abstract: "Humility is important for ethical behavior.",
			},
			minScore: 0.30, // 1 title * 0.25 + 1 abstract * 0.10
			maxScore: 0.40,
		},
		{
			name:  "no match gives zero",
			terms: []string{"humility"},
			paper: backends.Paper{
				Title:    "Moral Sensitivity Training",
				Abstract: "This paper discusses sensitivity and awareness.",
			},
			minScore: 0.0,
			maxScore: 0.0,
		},
		{
			name:  "multiple terms partial match",
			terms: []string{"moral", "humility"},
			paper: backends.Paper{
				Title:    "Moral Sensitivity",
				Abstract: "Discussing moral development.",
			},
			minScore: 0.2, // (0.4 + 0.15) / 2 terms = 0.275
			maxScore: 0.35,
		},
		{
			name:  "multiple terms full match",
			terms: []string{"moral", "humility"},
			paper: backends.Paper{
				Title:    "Moral Humility: A Virtue",
				Abstract: "This paper examines moral humility in detail.",
			},
			minScore: 0.5, // high score for both terms in both places
			maxScore: 0.7,
		},
		{
			name:  "score capped components then normalized",
			terms: []string{"a", "b"},
			paper: backends.Paper{
				Title:    "a b c d e f g h", // both terms in title: 2 * 0.25 = 0.50
				Abstract: "a b c d e f g h", // both terms in abstract: 2 * 0.10 = 0.20
			},
			// titleScore=0.50, abstractScore=0.20, total=0.70
			minScore: 0.65,
			maxScore: 0.75,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := RelevanceScore(tt.terms, tt.paper)
			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("RelevanceScore() = %v, want between %v and %v", score, tt.minScore, tt.maxScore)
			}
		})
	}
}

func TestFilterByRelevance(t *testing.T) {
	papers := []backends.Paper{
		{Title: "Moral Sensitivity Training", Abstract: "Discusses moral sensitivity."},
		{Title: "Moral Humility Research", Abstract: "Examines humility in ethics."},
		{Title: "Ethical Humility", Abstract: "A study of humility and ethics."},
		{Title: "Unrelated Paper", Abstract: "Nothing about the topic."},
		{Title: "Moral Courage", Abstract: "About courage and morality."},
	}

	tests := []struct {
		name         string
		query        string
		minScore     float64
		wantCount    int
		wantFirstHas string // substring that should be in first result's title
		wantExcluded string // substring that should NOT be in any result title
	}{
		{
			name:         "ranks humility papers first",
			query:        "moral humility",
			minScore:     0.2,
			wantCount:    4,                // Papers with "moral" OR "humility" (Unrelated excluded)
			wantFirstHas: "Moral Humility", // Best match should be first
			wantExcluded: "",
		},
		{
			name:         "empty query returns all",
			query:        "",
			minScore:     0.2,
			wantCount:    5,  // All papers returned when no terms
			wantFirstHas: "", // Don't check
		},
		{
			name:         "only stopwords returns all",
			query:        "the is and of",
			minScore:     0.2,
			wantCount:    5,
			wantFirstHas: "",
		},
		{
			name:         "zero threshold keeps all with any match",
			query:        "moral",
			minScore:     0.0,
			wantCount:    5, // Even 0 scores pass with 0 threshold
			wantFirstHas: "",
		},
		{
			name:         "high threshold filters to best matches",
			query:        "moral humility",
			minScore:     0.4,
			wantCount:    1, // Only paper with both terms (score ~0.475)
			wantFirstHas: "Moral Humility",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterByRelevance(tt.query, papers, tt.minScore)

			if len(result) != tt.wantCount {
				titles := make([]string, len(result))
				for i, p := range result {
					titles[i] = p.Title
				}
				t.Errorf("FilterByRelevance() returned %d papers, want %d. Got: %v", len(result), tt.wantCount, titles)
			}

			if tt.wantFirstHas != "" && len(result) > 0 {
				if !contains(result[0].Title, tt.wantFirstHas) {
					t.Errorf("First result title %q should contain %q", result[0].Title, tt.wantFirstHas)
				}
			}

			if tt.wantExcluded != "" {
				for _, p := range result {
					if contains(p.Title, tt.wantExcluded) {
						t.Errorf("Result should not contain paper with %q in title, but got %q", tt.wantExcluded, p.Title)
					}
				}
			}
		})
	}
}

func TestQualityScore(t *testing.T) {
	tests := []struct {
		name         string
		relevance    float64
		paper        backends.Paper
		maxCitations int
		wantMin      float64
		wantMax      float64
	}{
		{
			name:         "high relevance only",
			relevance:    1.0,
			paper:        backends.Paper{Title: "Test", Abstract: "", Citations: 0},
			maxCitations: 0,
			wantMin:      0.69,
			wantMax:      0.71, // ~0.70 (relevance only)
		},
		{
			name:         "high relevance with abstract",
			relevance:    1.0,
			paper:        backends.Paper{Title: "Test", Abstract: "Has content", Citations: 0},
			maxCitations: 0,
			wantMin:      0.84,
			wantMax:      0.86, // ~0.85 (0.70 + 0.15)
		},
		{
			name:         "high relevance with abstract and max citations",
			relevance:    1.0,
			paper:        backends.Paper{Title: "Test", Abstract: "Has content", Citations: 100},
			maxCitations: 100,
			wantMin:      0.99,
			wantMax:      1.01, // ~1.0 (0.70 + 0.15 + 0.15)
		},
		{
			name:         "zero relevance with quality signals",
			relevance:    0.0,
			paper:        backends.Paper{Title: "Test", Abstract: "Has content", Citations: 100},
			maxCitations: 100,
			wantMin:      0.29,
			wantMax:      0.31, // ~0.30 (0 + 0.15 + 0.15)
		},
		{
			name:         "log scaling for citations",
			relevance:    1.0,
			paper:        backends.Paper{Title: "Test", Abstract: "Has content", Citations: 10},
			maxCitations: 100,
			// log10(11)/log10(101) ≈ 1.04/2.0 ≈ 0.52
			// score = 0.70 + 0.15 + 0.15*0.52 ≈ 0.93
			wantMin: 0.91,
			wantMax: 0.95,
		},
		{
			name:         "whitespace-only abstract not counted",
			relevance:    1.0,
			paper:        backends.Paper{Title: "Test", Abstract: "   \t\n  ", Citations: 0},
			maxCitations: 0,
			wantMin:      0.69,
			wantMax:      0.71, // ~0.70 (relevance only, no abstract boost)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := QualityScore(tt.relevance, tt.paper, tt.maxCitations)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("QualityScore() = %v, want between %v and %v", score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestFilterByRelevance_QualityRanking(t *testing.T) {
	// Papers with same relevance but different quality signals.
	// Query uses two terms so the no-abstract paper (which needs
	// 2+ title matches to pass noAbstractThreshold) still qualifies.
	papers := []backends.Paper{
		{Title: "Moral Humility Study", Abstract: "", Citations: 0},                                     // relevance only
		{Title: "Moral Humility Research", Abstract: "Detailed study of moral humility.", Citations: 0}, // relevance + abstract
		{Title: "Moral Humility Analysis", Abstract: "An analysis of moral humility.", Citations: 50},   // relevance + abstract + citations
	}

	result := FilterByRelevance("moral humility", papers, 0.1)

	if len(result) != 3 {
		titles := make([]string, len(result))
		for i, p := range result {
			titles[i] = p.Title
		}
		t.Fatalf("Expected 3 results, got %d: %v", len(result), titles)
	}

	// Paper with abstract + citations should rank first
	if result[0].Title != "Moral Humility Analysis" {
		t.Errorf("First result should be 'Moral Humility Analysis' (has abstract + citations), got %q", result[0].Title)
	}

	// Paper with abstract only should rank second
	if result[1].Title != "Moral Humility Research" {
		t.Errorf("Second result should be 'Moral Humility Research' (has abstract), got %q", result[1].Title)
	}

	// Paper with no abstract should rank last
	if result[2].Title != "Moral Humility Study" {
		t.Errorf("Third result should be 'Moral Humility Study' (no abstract), got %q", result[2].Title)
	}
}

func TestFilterByRelevance_Sorting(t *testing.T) {
	papers := []backends.Paper{
		{Title: "Low Match Paper", Abstract: "Only mentions humility once."},
		{Title: "Humility Research", Abstract: "Extensive discussion of humility."},
		{Title: "The Humility Handbook", Abstract: "All about humility in practice."},
	}

	result := FilterByRelevance("humility", papers, 0.1)

	// Should be sorted by relevance score descending
	// Papers with "humility" in title should rank higher
	if len(result) < 2 {
		t.Fatalf("Expected at least 2 results, got %d", len(result))
	}

	// The papers with "Humility" in title should come first
	if !contains(result[0].Title, "Humility") {
		t.Errorf("First result should have 'Humility' in title, got %q", result[0].Title)
	}
}

func TestFilterByRelevance_NoAbstractThreshold(t *testing.T) {
	// Papers without abstracts need 2+ title matches to pass.
	// This prevents Crossref keyword garbage where a single common
	// word ("second", "class") matches a highly-cited STEM paper.
	papers := []backends.Paper{
		// 1 title match, no abstract → score 0.25 < 0.3 → filtered
		{Title: "Second-generation PLINK: rising to the challenge of larger datasets", Citations: 11000},
		// 2+ title matches, no abstract → score 0.50 >= 0.3 → passes
		{Title: "The Second Shift: Working Parents and the Revolution at Home", Citations: 3000},
		// 1 title match but has abstract → regular threshold 0.1 → passes
		{Title: "Working Mothers and Childcare", Abstract: "Examines second shift dynamics among working mothers.", Citations: 50},
	}

	result := FilterByRelevance("second shift working mothers", papers, 0.1)

	titles := make([]string, len(result))
	for i, p := range result {
		titles[i] = p.Title
	}

	// PLINK should be filtered out (1 title match, no abstract)
	for _, p := range result {
		if strings.Contains(p.Title, "PLINK") {
			t.Errorf("PLINK should be filtered out (1 title match, no abstract), but found in results: %v", titles)
		}
	}

	// The Second Shift and Working Mothers should remain
	if len(result) != 2 {
		t.Errorf("Expected 2 results, got %d: %v", len(result), titles)
	}
}

func TestFilterByRelevance_CitationFloorTitleMatches(t *testing.T) {
	// Citation floor bypass now requires 2+ title matches,
	// not just 2+ matches anywhere (title or abstract).
	papers := []backends.Paper{
		// 1 title match + 1 abstract match = 2 distinct but only 1 title → no floor bypass
		{Title: "Social Network Effects", Abstract: "Analysis of media influence on social networks.", Citations: 500},
		// 2 title matches → floor bypass applies
		{Title: "Social Media Gender Representation", Abstract: "Studies of gender in social media.", Citations: 200},
	}

	// With low relevance threshold, both might pass regular filter.
	// The key test: only the 2-title-match paper gets the citation floor bypass.
	result := FilterByRelevance("social media gender", papers, 0.1)

	// Both should pass regular threshold (they have enough matches).
	// But if we used a higher threshold, only the floor-bypassed one
	// would pass. Let's verify both are present.
	if len(result) != 2 {
		titles := make([]string, len(result))
		for i, p := range result {
			titles[i] = p.Title
		}
		t.Errorf("Expected 2 results, got %d: %v", len(result), titles)
	}
}

func TestDedupeBookChapters(t *testing.T) {
	papers := []backends.Paper{
		{Title: "Masculinity Besieged? Inside Incels", DOI: "10.1234/ch1", Journal: "Masculinity Besieged?", Citations: 0},
		{Title: "Masculinity Besieged? The Red Pill", DOI: "10.1234/ch2", Journal: "Masculinity Besieged?", Abstract: "Chapter about the red pill.", Citations: 5},
		{Title: "Masculinity Besieged? Conclusion", DOI: "10.1234/ch3", Journal: "Masculinity Besieged?", Citations: 0},
		{Title: "Hegemonic Masculinity", DOI: "10.5678/hm", Journal: "Gender & Society", Citations: 6399},
		{Title: "Doing Gender", DOI: "10.5678/dg", Journal: "Gender & Society", Citations: 7847},
	}

	result := dedupeBookChapters(papers)

	// The three "Masculinity Besieged?" chapters share Journal + long title prefix.
	// Only the one with the best metadata (ch2, has abstract) should survive.
	// The two Gender & Society papers have different titles (no shared prefix), both survive.
	if len(result) != 3 {
		titles := make([]string, len(result))
		for i, p := range result {
			titles[i] = p.Title
		}
		t.Fatalf("Expected 3 results after chapter dedup, got %d: %v", len(result), titles)
	}

	// Check that the chapter with the abstract survived
	foundChapter := false
	for _, p := range result {
		if strings.Contains(p.Title, "Red Pill") {
			foundChapter = true
		}
	}
	if !foundChapter {
		t.Error("Expected the chapter with abstract ('The Red Pill') to survive dedup")
	}

	// Check both Gender & Society papers survived
	gsPapers := 0
	for _, p := range result {
		if p.Journal == "Gender & Society" {
			gsPapers++
		}
	}
	if gsPapers != 2 {
		t.Errorf("Expected 2 Gender & Society papers, got %d", gsPapers)
	}
}

func TestDedupeBookChapters_NoDupes(t *testing.T) {
	papers := []backends.Paper{
		{Title: "Paper A", Journal: "Journal X", Citations: 10},
		{Title: "Paper B", Journal: "Journal Y", Citations: 20},
		{Title: "Paper C", Journal: "Journal X", Citations: 5},
	}

	result := dedupeBookChapters(papers)

	// No shared title prefixes, nothing should be deduped
	if len(result) != 3 {
		t.Errorf("Expected 3 results (no dedup), got %d", len(result))
	}
}

func TestContainsWord(t *testing.T) {
	tests := []struct {
		text, word string
		want       bool
	}{
		{"social media", "media", true},
		{"the mediator variable", "media", false}, // not a word boundary
		{"male initiations", "initiation", true},  // plural 's' accepted
		{"translation initiation site", "initiation", true},
		{"indymedia and gender", "media", false}, // compound word
		{"prosocial behaviour", "social", false}, // prefix of compound
		{"anti-social policy", "social", true},   // hyphen = boundary
		{"household-labor division", "labor", true},
		{"modelling approaches", "model", false}, // different word
		{"the class system", "class", true},
		{"classifications of", "class", false}, // not a word boundary
		{"", "test", false},
		{"test", "test", true},
		{"tests", "test", true}, // plural
	}

	for _, tt := range tests {
		got := containsWord(tt.text, tt.word)
		if got != tt.want {
			t.Errorf("containsWord(%q, %q) = %v, want %v", tt.text, tt.word, got, tt.want)
		}
	}
}

func TestNormTitle(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Doing Gender", "doing gender"},
		{"Rites de passage, rites d'initiation", "rites de passage rites dinitiation"},
		// Smart quote variant should normalise identically
		{"Rites de passage, rites d\u2019initiation", "rites de passage rites dinitiation"},
		{"Employment rates: women", "employment rates women"},
		{"  lots   of   spaces  ", "lots of spaces"},
		{"Title—with—em-dashes", "titlewithemdashes"}, // em-dash and hyphen stripped
		{"", ""},
	}
	for _, tt := range tests {
		got := normTitle(tt.input)
		if got != tt.want {
			t.Errorf("normTitle(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDedupeTitleVariants(t *testing.T) {
	t.Run("encoding variants dedup", func(t *testing.T) {
		papers := []backends.Paper{
			{Title: "Rites de passage, rites d'initiation", DOI: "10.1/a", Abstract: "Has abstract", Citations: 50},
			{Title: "Rites de passage, rites d\u2019initiation", DOI: "10.1/b", Citations: 10},
		}
		result := dedupeTitleVariants(papers)
		if len(result) != 1 {
			t.Fatalf("expected 1, got %d", len(result))
		}
		if result[0].DOI != "10.1/a" {
			t.Errorf("kept wrong paper: DOI=%s (expected 10.1/a with abstract)", result[0].DOI)
		}
	})

	t.Run("identical titles different DOIs dedup", func(t *testing.T) {
		papers := []backends.Paper{
			{Title: "Employment rates: women", DOI: "10.1/oecd1", Citations: 0},
			{Title: "Employment rates: women", DOI: "10.1/oecd2", Citations: 5},
			{Title: "Employment rates: women", DOI: "10.1/oecd3", Citations: 0},
		}
		result := dedupeTitleVariants(papers)
		if len(result) != 1 {
			t.Fatalf("expected 1, got %d", len(result))
		}
		if result[0].DOI != "10.1/oecd2" {
			t.Errorf("kept wrong paper: DOI=%s (expected 10.1/oecd2 with most citations)", result[0].DOI)
		}
	})

	t.Run("different titles preserved", func(t *testing.T) {
		papers := []backends.Paper{
			{Title: "Doing Gender", DOI: "10.1/a"},
			{Title: "Undoing Gender", DOI: "10.1/b"},
		}
		result := dedupeTitleVariants(papers)
		if len(result) != 2 {
			t.Errorf("expected 2, got %d", len(result))
		}
	})

	t.Run("empty and single pass through", func(t *testing.T) {
		if len(dedupeTitleVariants(nil)) != 0 {
			t.Error("nil should return nil")
		}
		single := []backends.Paper{{Title: "Solo"}}
		if len(dedupeTitleVariants(single)) != 1 {
			t.Error("single should pass through")
		}
	})
}

func contains(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
