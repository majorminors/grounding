package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

const schachterSingerDOI = "10.1037/h0046234"

// recordsACatalogueReturnedForSchachterSingerDOI are the five records a
// catalogue's text search returned for 10.1037/h0046234 on 2026-11-06. None is
// the paper: they share only the publisher's 10.1037 prefix, which is
// why a DOI query must not simply bypass the filter.
func recordsACatalogueReturnedForSchachterSingerDOI() []backends.Paper {
	return []backends.Paper{
		{Title: "Gain without pain? Expressive writing and self-regulation.", DOI: "10.1037/10451-006", Source: "fake_catalogue"},
		{Title: "Treating Incompleteness, Ordering, and Arranging Concerns.", DOI: "10.1037/11543-008", Source: "fake_catalogue"},
		{Title: "Substance abuse and intimate partner violence.", DOI: "10.1037/11880-012", Source: "fake_catalogue"},
		{Title: "Essentials of qualitative meta-analysis.", DOI: "10.1037/0000313-000", Source: "fake_catalogue"},
		{Title: "Constructing self in career theory and counseling interventions.", DOI: "10.1037/12348-006", Source: "fake_catalogue"},
	}
}

func schachterSingerPaper() backends.Paper {
	return backends.Paper{
		Title:  "Cognitive, social, and physiological determinants of emotional state.",
		DOI:    schachterSingerDOI,
		Source: "fake_catalogue",
	}
}

func TestFilterByRelevanceKeepsTheRecordCarryingAQueriedDOI(t *testing.T) {
	for _, query := range []string{
		schachterSingerDOI,
		"10.1037/H0046234",
		"https://doi.org/10.1037/h0046234",
		"doi:10.1037/h0046234",
		"10.1037//h0046234",
	} {
		t.Run(query, func(t *testing.T) {
			papers := append(recordsACatalogueReturnedForSchachterSingerDOI(), schachterSingerPaper())
			kept := FilterByRelevance(query, papers, minRelevanceScore)
			if len(kept) != 1 || kept[0].DOI != schachterSingerDOI {
				t.Errorf("want only the record carrying the DOI, got %+v", kept)
			}
		})
	}
}

func TestFilterByRelevanceComparesARecordsDOICaseInsensitively(t *testing.T) {
	paper := schachterSingerPaper()
	paper.DOI = "https://doi.org/10.1037/H0046234"
	kept := FilterByRelevance(schachterSingerDOI, []backends.Paper{paper}, minRelevanceScore)
	if len(kept) != 1 {
		t.Errorf("want the record kept, got %+v", kept)
	}
}

// Guards against the tempting fix: waving DOI queries through would
// present five unrelated papers as hits for the DOI.
func TestFilterByRelevanceDropsRecordsSharingOnlyTheDOIPrefix(t *testing.T) {
	kept := FilterByRelevance(schachterSingerDOI, recordsACatalogueReturnedForSchachterSingerDOI(), minRelevanceScore)
	if len(kept) != 0 {
		t.Errorf("want nothing kept, got %+v", kept)
	}
}

// A query that merely contains a DOI among other words stays a keyword
// query, scored as before.
func TestFilterByRelevanceTreatsADOIAmongOtherWordsAsKeywords(t *testing.T) {
	papers := []backends.Paper{{Title: "Emotional state and social determinants", Abstract: "Some abstract."}}
	kept := FilterByRelevance("emotional state "+schachterSingerDOI, papers, minRelevanceScore)
	if len(kept) != 1 {
		t.Errorf("want the keyword match kept, got %+v", kept)
	}
}

// searchACatalogueReturning searches only a fake source that returns
// papers, as a catalogue's text search did for the DOI.
func searchACatalogueReturning(t *testing.T, papers []backends.Paper, query string) *backends.ContextResult {
	t.Helper()
	t.Setenv("GROUNDING_EMAIL", "") // availability would ask the real Unpaywall for the fixture's DOI
	registerFakeAddOnForThisTest(t, PaperSearchSource{
		Name: "fake_catalogue",
		Search: func(ctx context.Context, client *backends.Client, cfg config.Config, query string, limit int, filters backends.SearchFilters) ([]backends.Paper, error) {
			return papers, nil
		},
	})
	result, err := searchCatalogueUnrecorded(context.Background(), query, "academic", 5, []string{"fake_catalogue"}, backends.SearchFilters{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return result
}

// The reported symptom, from fixtures: five raw records, none the paper.
func TestSearchCatalogueExplainsThatEveryRawRecordWasFilteredOut(t *testing.T) {
	result := searchACatalogueReturning(t, recordsACatalogueReturnedForSchachterSingerDOI(), schachterSingerDOI)

	if len(result.Papers) != 0 {
		t.Fatalf("want no papers, got %+v", result.Papers)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].RawCount != 5 || result.Diagnostics[0].FilteredCount != 0 {
		t.Fatalf("want one diagnostic of 5 raw and 0 kept, got %+v", result.Diagnostics)
	}
	for _, want := range []string{"5 results", schachterSingerDOI, "check_citation"} {
		if !strings.Contains(result.EmptyResultExplanation, want) {
			t.Errorf("explanation should contain %q, got %q", want, result.EmptyResultExplanation)
		}
	}
}

func TestSearchCatalogueReturnsTheRecordCarryingTheQueriedDOI(t *testing.T) {
	records := append([]backends.Paper{schachterSingerPaper()}, recordsACatalogueReturnedForSchachterSingerDOI()[:4]...)
	result := searchACatalogueReturning(t, records, schachterSingerDOI)

	if len(result.Papers) != 1 || result.Papers[0].DOI != schachterSingerDOI {
		t.Fatalf("want only the record carrying the DOI, got %+v", result.Papers)
	}
	if result.EmptyResultExplanation != "" {
		t.Errorf("a result with papers needs no explanation, got %q", result.EmptyResultExplanation)
	}
}

func TestEmptyResultExplanationForEachWayOfReturningNothing(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		diagnostics []backends.Diagnostic
		wantAll     []string
	}{
		{
			name:        "keyword query filtered out",
			query:       "emotional state",
			diagnostics: []backends.Diagnostic{{Backend: "crossref", RawCount: 1}},
			wantAll:     []string{"1 result ", "filter"},
		},
		{
			name:        "backend failed",
			query:       schachterSingerDOI,
			diagnostics: []backends.Diagnostic{{Backend: "fake_catalogue", Error: "login request: no such host"}},
			wantAll:     []string{"fake_catalogue", "failed"},
		},
		{
			name:        "every backend skipped",
			query:       "emotional state",
			diagnostics: []backends.Diagnostic{{Backend: "fake_catalogue", Skipped: true, SkippedReason: "not configured"}},
			wantAll:     []string{"fake_catalogue", "skipped"},
		},
		{
			name:        "a clean empty answer needs no explanation",
			query:       "emotional state",
			diagnostics: []backends.Diagnostic{{Backend: "crossref"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := explanationWhenNothingIsReturned(tt.query, tt.diagnostics)
			if len(tt.wantAll) == 0 && got != "" {
				t.Errorf("want no explanation, got %q", got)
			}
			if len(tt.wantAll) > 0 && got == "" {
				t.Errorf("want an explanation, got none")
			}
			for _, want := range tt.wantAll {
				if !strings.Contains(got, want) {
					t.Errorf("explanation should contain %q, got %q", want, got)
				}
			}
		})
	}
}

// omitempty used to drop a zero here, so a JSON caller saw raw_count 5
// and nothing saying that none survived.
func TestDiagnosticJSONStatesThatNothingSurvivedTheFilter(t *testing.T) {
	encoded, err := json.Marshal(backends.Diagnostic{Backend: "fake_catalogue", RawCount: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"filtered_count":0`) {
		t.Errorf("want filtered_count stated, got %s", encoded)
	}
}
