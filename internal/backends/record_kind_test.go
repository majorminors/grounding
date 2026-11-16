package backends

import "testing"

func TestRecordKindFromPublicationType(t *testing.T) {
	for publicationType, want := range map[string]string{
		"journal-article": RecordKindPaper,
		"article":         RecordKindPaper,
		"posted-content":  RecordKindPaper,
		"":                RecordKindPaper,
		"book":            RecordKindBook,
		"monograph":       RecordKindBook,
		"edited-book":     RecordKindBook,
		"book_nonfiction": RecordKindBook,
		"book_fiction":    RecordKindBook,
		"book-chapter":    RecordKindBookChapter,
		"Book-Chapter":    RecordKindBookChapter,
		"book-part":       RecordKindBookChapter,
	} {
		if got := RecordKindFromPublicationType(publicationType); got != want {
			t.Errorf("%q: got %q, want %q", publicationType, got, want)
		}
	}
}

func TestOpenLibrarySearchGivesBooksWithISBNAndPublisherWithinTheYears(t *testing.T) {
	resp := openLibrarySearchResponse{Docs: []openLibraryDoc{
		{Title: "The New Monogamy", AuthorName: []string{"Tammy Nelson"}, FirstPublishYear: 2013, ISBN: []string{"1608823164", "9781608823161"}, Publisher: []string{"New Harbinger"}, Key: "/works/OL1W"},
		{Title: "The Monogamy Myth", AuthorName: []string{"Peggy Vaughan"}, FirstPublishYear: 1989},
		{Title: "Too new", FirstPublishYear: 2030},
	}}
	books := booksFromOpenLibrarySearch(resp, 5, SearchFilters{YearMin: 2000, YearMax: 2026})
	if len(books) != 1 {
		t.Fatalf("got %d books, want only the one within 2000–2026: %+v", len(books), books)
	}
	book := books[0]
	if book.Kind != RecordKindBook || book.ISBN != "9781608823161" || book.Publisher != "New Harbinger" || book.URL != "https://openlibrary.org/works/OL1W" || book.Source != "openlibrary" {
		t.Errorf("book = %+v", book)
	}
	if got := booksFromOpenLibrarySearch(resp, 1, SearchFilters{}); len(got) != 1 {
		t.Errorf("limit 1 gave %d books", len(got))
	}
}

func TestConsensusKeepsItsOwnSummaryOutOfTheAbstract(t *testing.T) {
	paper := consensusResultToPaper(consensusResult{Title: "Oxytocin and pair bonds", Abstract: "The authors' abstract.", Takeaway: "Oxytocin increased partner preference."})
	if paper.Abstract != "The authors' abstract." {
		t.Errorf("abstract = %q, want the authors' words alone", paper.Abstract)
	}
	if paper.SummaryWrittenByConsensus != "Oxytocin increased partner preference." {
		t.Errorf("summary_written_by_consensus = %q", paper.SummaryWrittenByConsensus)
	}
}
