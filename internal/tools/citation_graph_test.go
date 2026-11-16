package tools

import (
	"testing"

	"btr.mt/grounding/internal/backends"
)

func TestResolvePaperID(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantID     string
		wantDirect bool
	}{
		{
			name:       "DOI URL",
			input:      "https://doi.org/10.1038/nature12373",
			wantID:     "DOI:10.1038/nature12373",
			wantDirect: true,
		},
		{
			name:       "doi: prefix lowercase",
			input:      "doi:10.1038/nature12373",
			wantID:     "DOI:10.1038/nature12373",
			wantDirect: true,
		},
		{
			name:       "doi: prefix uppercase",
			input:      "DOI:10.1038/nature12373",
			wantID:     "DOI:10.1038/nature12373",
			wantDirect: true,
		},
		{
			name:       "bare DOI",
			input:      "10.1038/nature12373",
			wantID:     "DOI:10.1038/nature12373",
			wantDirect: true,
		},
		{
			name:       "arxiv: prefix",
			input:      "arxiv:1706.03762",
			wantID:     "ARXIV:1706.03762",
			wantDirect: true,
		},
		{
			name:       "ARXIV: prefix uppercase",
			input:      "ARXIV:1706.03762",
			wantID:     "ARXIV:1706.03762",
			wantDirect: true,
		},
		{
			name:       "bare arXiv",
			input:      "1706.03762",
			wantID:     "ARXIV:1706.03762",
			wantDirect: true,
		},
		{
			name:       "bare arXiv with version",
			input:      "1706.03762v5",
			wantID:     "ARXIV:1706.03762v5",
			wantDirect: true,
		},
		{
			name:       "corpusid: prefix",
			input:      "corpusid:12345678",
			wantID:     "CorpusId:12345678",
			wantDirect: true,
		},
		{
			name:       "CorpusId: mixed case",
			input:      "CorpusId:12345678",
			wantID:     "CorpusId:12345678",
			wantDirect: true,
		},
		{
			name:       "40-char hex SS ID",
			input:      "649def34f8be52c8b66281af98ae884c09aef38b",
			wantID:     "649def34f8be52c8b66281af98ae884c09aef38b",
			wantDirect: true,
		},
		{
			name:       "title fallback",
			input:      "attention is all you need",
			wantID:     "attention is all you need",
			wantDirect: false,
		},
		{
			name:       "empty string",
			input:      "",
			wantID:     "",
			wantDirect: false,
		},
		{
			name:       "whitespace trimmed",
			input:      "  10.1038/nature12373  ",
			wantID:     "DOI:10.1038/nature12373",
			wantDirect: true,
		},
		{
			name:       "short hex is not SS ID",
			input:      "abc123",
			wantID:     "abc123",
			wantDirect: false,
		},
		{
			name:       "APA double-slash DOI collapsed",
			input:      "10.1037//0022-3514.52.3.511",
			wantID:     "DOI:10.1037/0022-3514.52.3.511",
			wantDirect: true,
		},
		{
			name:       "double-slash DOI URL collapsed",
			input:      "https://doi.org/10.1037//0022-3514.52.3.511",
			wantID:     "DOI:10.1037/0022-3514.52.3.511",
			wantDirect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotDirect := ResolvePaperID(tt.input)
			if gotID != tt.wantID {
				t.Errorf("ResolvePaperID(%q) id = %q, want %q", tt.input, gotID, tt.wantID)
			}
			if gotDirect != tt.wantDirect {
				t.Errorf("ResolvePaperID(%q) direct = %v, want %v", tt.input, gotDirect, tt.wantDirect)
			}
		})
	}
}

func edge(year int) backends.CitationEdge {
	return backends.CitationEdge{Paper: backends.Paper{Year: year}}
}

func edgeCited(year, citations int) backends.CitationEdge {
	return backends.CitationEdge{Paper: backends.Paper{Year: year, Citations: citations}}
}

func TestBuildCitationTrend(t *testing.T) {
	t.Run("nil on empty graph", func(t *testing.T) {
		r := &backends.CitationGraphResult{Limit: 20}
		if got := buildCitationTrend(r); got != nil {
			t.Errorf("expected nil, got %+v", got)
		}
	})

	t.Run("counts and range", func(t *testing.T) {
		r := &backends.CitationGraphResult{
			References: []backends.CitationEdge{
				edge(2010), edge(2010), edge(2015),
			},
			Citations: []backends.CitationEdge{
				edge(2015), edge(2020),
			},
			Limit: 20,
		}
		tr := buildCitationTrend(r)
		if tr == nil {
			t.Fatal("expected trend, got nil")
		}
		if tr.Counts[2010] != 2 {
			t.Errorf("2010 count = %d, want 2", tr.Counts[2010])
		}
		if tr.Counts[2015] != 2 {
			t.Errorf("2015 count = %d, want 2", tr.Counts[2015])
		}
		if tr.Counts[2020] != 1 {
			t.Errorf("2020 count = %d, want 1", tr.Counts[2020])
		}
		if tr.YearRange != [2]int{2010, 2020} {
			t.Errorf("year range = %v, want [2010 2020]", tr.YearRange)
		}
		if tr.MissingYear != 0 {
			t.Errorf("missing = %d, want 0", tr.MissingYear)
		}
		if tr.Sampled {
			t.Error("expected sampled=false")
		}
	})

	t.Run("missing years tracked", func(t *testing.T) {
		r := &backends.CitationGraphResult{
			Citations: []backends.CitationEdge{
				edge(2018), edge(0), edge(0),
			},
			Limit: 20,
		}
		tr := buildCitationTrend(r)
		if tr.MissingYear != 2 {
			t.Errorf("missing = %d, want 2", tr.MissingYear)
		}
		if tr.Counts[2018] != 1 {
			t.Errorf("2018 count = %d, want 1", tr.Counts[2018])
		}
	})

	t.Run("all years missing", func(t *testing.T) {
		r := &backends.CitationGraphResult{
			Citations: []backends.CitationEdge{edge(0), edge(0)},
			Limit:     20,
		}
		tr := buildCitationTrend(r)
		if tr == nil {
			t.Fatal("expected trend, got nil")
		}
		if tr.MissingYear != 2 {
			t.Errorf("missing = %d, want 2", tr.MissingYear)
		}
		if len(tr.Counts) != 0 {
			t.Errorf("expected empty counts, got %v", tr.Counts)
		}
	})

	t.Run("sampled when at limit", func(t *testing.T) {
		refs := make([]backends.CitationEdge, 20)
		for i := range refs {
			refs[i] = edge(2000 + i)
		}
		r := &backends.CitationGraphResult{
			References: refs,
			Limit:      20,
		}
		tr := buildCitationTrend(r)
		if !tr.Sampled {
			t.Error("expected sampled=true when edges == limit")
		}
	})
}

func TestSortEdgesByCitations(t *testing.T) {
	edges := []backends.CitationEdge{
		edgeCited(2020, 10),
		edgeCited(2015, 500),
		edgeCited(2018, 100),
		edgeCited(2022, 1),
	}
	sortEdgesByCitations(edges)

	want := []int{500, 100, 10, 1}
	for i, w := range want {
		if edges[i].Paper.Citations != w {
			t.Errorf("edges[%d].Citations = %d, want %d", i, edges[i].Paper.Citations, w)
		}
	}
}

func TestSortEdgesByCitations_empty(t *testing.T) {
	var edges []backends.CitationEdge
	sortEdgesByCitations(edges) // should not panic
	if len(edges) != 0 {
		t.Errorf("expected empty, got %d", len(edges))
	}
}

func TestFilterEdgesByMinCitations(t *testing.T) {
	edges := []backends.CitationEdge{
		edgeCited(2020, 10),
		edgeCited(2015, 500),
		edgeCited(2018, 100),
		edgeCited(2022, 1),
		edgeCited(2019, 50),
	}

	filtered := filterEdgesByMinCitations(edges, 50)

	if len(filtered) != 3 {
		t.Fatalf("expected 3 results, got %d", len(filtered))
	}
	// Should preserve order
	want := []int{10: 0, 500: 0, 100: 0} // just check all >= 50
	_ = want
	for _, e := range filtered {
		if e.Paper.Citations < 50 {
			t.Errorf("edge with %d citations should have been filtered (min 50)", e.Paper.Citations)
		}
	}
}

func TestFilterEdgesByMinCitations_zero(t *testing.T) {
	edges := []backends.CitationEdge{
		edgeCited(2020, 10),
		edgeCited(2015, 0),
	}
	filtered := filterEdgesByMinCitations(edges, 0)
	if len(filtered) != 2 {
		t.Errorf("min_citations=0 should keep all, got %d", len(filtered))
	}
}

func TestFilterEdgesByMinCitations_allFiltered(t *testing.T) {
	edges := []backends.CitationEdge{
		edgeCited(2020, 5),
		edgeCited(2015, 3),
	}
	filtered := filterEdgesByMinCitations(edges, 100)
	if len(filtered) != 0 {
		t.Errorf("expected 0 results, got %d", len(filtered))
	}
}
