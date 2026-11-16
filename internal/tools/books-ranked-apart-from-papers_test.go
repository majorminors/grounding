package tools

import (
	"testing"

	"btr.mt/grounding/internal/backends"
)

func TestBooksAreLimitedApartFromPapersSoTheyAreNotTrimmedAway(t *testing.T) {
	ranked := []backends.Paper{
		{Title: "p1"}, {Title: "p2", Kind: backends.RecordKindBookChapter}, {Title: "p3"},
		{Title: "b1", Kind: backends.RecordKindBook}, {Title: "b2", Kind: backends.RecordKindBook}, {Title: "b3", Kind: backends.RecordKindBook},
	}
	kept := keepTheFirstOfEachKind(ranked, 2)
	papers, books := splitBooksFromPapers(kept)
	if len(papers) != 2 || papers[0].Title != "p1" || papers[1].Title != "p2" {
		t.Errorf("papers = %+v, want p1 and p2 in rank order", papers)
	}
	if len(books) != 2 || books[0].Title != "b1" || books[1].Title != "b2" {
		t.Errorf("books = %+v, want b1 and b2 in rank order", books)
	}
	if papers[0].Kind != backends.RecordKindPaper {
		t.Errorf("a record with no kind was not given one: %+v", papers[0])
	}
}

func TestABookWithoutAnAbstractPassesTheRelevanceFilterOnItsTitle(t *testing.T) {
	records := []backends.Paper{
		{Title: "The New Monogamy", Kind: backends.RecordKindBook, Source: "openlibrary"},
		{Title: "Monogamy", Kind: backends.RecordKindPaper, Source: "crossref"},
		{Title: "Polyamory explained", Kind: backends.RecordKindBook, Source: "openlibrary"},
	}
	kept := FilterByRelevance("the new monogamy", records, minRelevanceScore)
	if len(kept) != 1 || kept[0].Title != "The New Monogamy" {
		t.Errorf("kept %+v, want the book whose title carries the query's one term, and not the abstract-less paper or the unrelated book", kept)
	}
	two := FilterByRelevance("household labour gender", []backends.Paper{
		{Title: "Gender at work", Kind: backends.RecordKindBook},
		{Title: "Household labour and gender", Kind: backends.RecordKindBook},
	}, minRelevanceScore)
	if len(two) != 1 || two[0].Title != "Household labour and gender" {
		t.Errorf("kept %+v, want only the book with two of three terms in its title", two)
	}
}
