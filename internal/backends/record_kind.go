package backends

import "strings"

// The kinds of record a catalogue search returns (Paper.Kind).
const (
	RecordKindPaper       = "paper"
	RecordKindBook        = "book"
	RecordKindBookChapter = "book_chapter"
)

// RecordKindFromPublicationType maps a backend's own type word
// (Crossref's "book-chapter", OpenAlex's "book", a book catalogue's
// "book_nonfiction") to a record kind. Anything that is not a book or
// a part of one is a paper: articles, preprints, reviews, datasets.
func RecordKindFromPublicationType(publicationType string) string {
	t := strings.ToLower(strings.TrimSpace(publicationType))
	switch {
	case t == "book-chapter", t == "book-section", t == "book-part", t == "book_chapter":
		return RecordKindBookChapter
	case t == "book", t == "monograph", t == "edited-book", t == "reference-book", t == "book-set",
		strings.HasPrefix(t, "book_"):
		return RecordKindBook
	}
	return RecordKindPaper
}
