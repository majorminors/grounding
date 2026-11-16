package backends

import "context"

// Paper represents an academic paper from any backend.
type Paper struct {
	Title string `json:"title"`
	// Kind is what the record is: RecordKindPaper, RecordKindBook or
	// RecordKindBookChapter (record_kind.go).
	Kind      string   `json:"kind,omitempty"`
	Authors   []string `json:"authors"`
	Year      int      `json:"year,omitempty"`
	DOI       string   `json:"doi,omitempty"`
	Abstract  string   `json:"abstract,omitempty"`
	URL       string   `json:"url,omitempty"`
	Source    string   `json:"source"` // Backend that provided this result
	Journal   string   `json:"journal,omitempty"`
	Citations int      `json:"citations,omitempty"`
	Domain    string   `json:"domain,omitempty"`
	ISBN      string   `json:"isbn,omitempty"`
	Publisher string   `json:"publisher,omitempty"` // books (OpenLibrary)
	// SummaryWrittenByConsensus is Consensus's own one-line finding for
	// the study: its model's words, not the authors', so it is kept out
	// of Abstract.
	SummaryWrittenByConsensus string `json:"summary_written_by_consensus,omitempty"`

	// Availability says whether and how the record itself can be had;
	// nil when no provider knows (search_catalogue fills it).
	Availability *Availability `json:"availability,omitempty"`
	MD5          string        `json:"md5,omitempty"`          // a file's hash, from a source that holds files
	FileType     string        `json:"file_type,omitempty"`    // File format, e.g. "pdf", "epub" (from a source that holds files)
	ContentType  string        `json:"content_type,omitempty"` // e.g. "book_nonfiction", "journal_article"
	Language     string        `json:"language,omitempty"`     // Primary language code, e.g. "en"
	FileSize     int64         `json:"file_size,omitempty"`    // File size in bytes (from a source that holds files)
}

// Availability is how a catalogue record can be had.
type Availability struct {
	// OpenAccessURL is a legal free copy anyone may open (Unpaywall):
	// a PDF, or a landing page when no file is known.
	OpenAccessURL     string `json:"open_access_url,omitempty"`
	OpenAccessURLKind string `json:"open_access_url_kind,omitempty"` // "pdf" or "landing_page"
	OpenAccessLicense string `json:"open_access_license,omitempty"`

	// DownloadableFileMD5 identifies a file find_download_link can fetch
	// by MD5, for a record with no DOI route (a book, say).
	DownloadableFileMD5 string `json:"downloadable_file_md5,omitempty"`

	// FindDownloadLinkArguments are the exact arguments to call
	// find_download_link with, set only when this caller has that tool.
	FindDownloadLinkArguments map[string]string `json:"find_download_link_arguments,omitempty"`
}

// WikipediaArticle represents an article from Wikipedia.
type WikipediaArticle struct {
	Title   string `json:"title"`
	Extract string `json:"extract"`
	URL     string `json:"url"`
}

// ResultSummary provides high-level metadata about search results so
// the LLM can spot temporal and influence patterns without scanning
// every paper record.
type ResultSummary struct {
	TotalPapers int      `json:"total_papers"`
	YearRange   [2]int   `json:"year_range,omitempty"`
	TopCited    []string `json:"top_cited,omitempty"`
}

// Diagnostic reports what happened in a single backend during a search.
// Always populated so the caller (LLM or human) can see why results
// are missing: backend error, zero upstream hits, or relevance filter.
type Diagnostic struct {
	Backend       string `json:"backend"`
	RawCount      int    `json:"raw_count"`      // results before filtering
	FilteredCount int    `json:"filtered_count"` // results after relevance filter; a zero beside raw_count is the news
	Error         string `json:"error,omitempty"`
	Skipped       bool   `json:"skipped,omitempty"`
	SkippedReason string `json:"skipped_reason,omitempty"`

	// CostUSD is what this call cost, for backends that report it per
	// call (Exa); CostNote is the standing price for those that don't
	// (Consensus). Either is the caller's cue to spend deliberately.
	CostUSD  float64 `json:"cost_usd,omitempty"`
	CostNote string  `json:"cost_note,omitempty"`
}

// ContextResult holds search results from multiple backends.
type ContextResult struct {
	Papers   []Paper            `json:"papers,omitempty"`
	Books    []Paper            `json:"books,omitempty"` // records of kind book, ranked and limited apart from papers
	Articles []WikipediaArticle `json:"articles,omitempty"`
	Query    string             `json:"query"`
	Summary  *ResultSummary     `json:"summary,omitempty"`
	// Diagnostics are per-backend counts and errors: always logged,
	// shown to a caller only when it asks (debug).
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`

	// Warnings are the one line each a caller always sees when a source
	// it relied on failed, was resting after a rate limit, or was named
	// but could not run: results may be incomplete.
	Warnings []string `json:"warnings,omitempty"`

	// EmptyResultExplanation says why nothing came back when the reason
	// is not simply that the backends had nothing: records filtered out,
	// a backend failed, or none could run. Empty otherwise.
	EmptyResultExplanation string `json:"empty_result_explanation,omitempty"`
}

// VerificationResult represents the outcome of verifying a citation.
type VerificationResult struct {
	Verified   bool    `json:"verified"`
	Paper      *Paper  `json:"paper,omitempty"`
	Source     string  `json:"source"`               // Backend that verified it
	Confidence float64 `json:"confidence,omitempty"` // Match confidence (0-1) for title-based matches
	Error      string  `json:"error,omitempty"`
	Suggestion string  `json:"suggestion,omitempty"` // Guidance when verification fails
}

// HealthStatus represents the health of a backend.
type HealthStatus struct {
	Healthy bool   `json:"healthy"`
	Message string `json:"message,omitempty"`
}

// SearchBackend can search for papers or articles by query.
type SearchBackend interface {
	// Name returns the backend identifier (e.g., "crossref", "semantic-scholar").
	Name() string

	// Search returns papers matching the query. Filters are applied as
	// native API parameters where possible, post-retrieval otherwise.
	Search(ctx context.Context, query string, limit int, filters SearchFilters) ([]Paper, error)

	// HealthCheck tests if the backend is reachable.
	HealthCheck(ctx context.Context) HealthStatus
}

// CitationBackend can verify that citations exist.
type CitationBackend interface {
	// Name returns the backend identifier.
	Name() string

	// VerifyDOI checks if a DOI exists and returns its metadata.
	VerifyDOI(ctx context.Context, doi string) (*VerificationResult, error)

	// VerifyByTitle searches for a paper by title/authors/year and scores the match.
	VerifyByTitle(ctx context.Context, title string, authors []string, year int) (*VerificationResult, error)

	// HealthCheck tests if the backend is reachable.
	HealthCheck(ctx context.Context) HealthStatus
}

// CitationEdge represents a single reference or citation link with metadata.
type CitationEdge struct {
	Paper         Paper    `json:"paper"`
	IsInfluential bool     `json:"is_influential"`
	Intents       []string `json:"intents,omitempty"`
}

// CitationTrend summarises the temporal distribution of a citation
// graph's edges. Built from Semantic Scholar or OpenAlex data; year
// coverage is near-complete for published work but may omit some
// pre-prints.
//
// The histogram reflects the subset of edges actually returned, which
// is capped at the request limit (max 1000). For papers with many
// thousands of citations the distribution is a sample, not a census,
// and becomes less representative as total citation count grows.
type CitationTrend struct {
	// Counts maps publication year to the number of edges in that year.
	Counts map[int]int `json:"counts"`
	// YearRange is [earliest, latest] among edges with a known year.
	YearRange [2]int `json:"year_range"`
	// MissingYear is the number of edges where year was unavailable.
	MissingYear int `json:"missing_year,omitempty"`
	// Sampled is true when the graph was capped by the limit parameter,
	// meaning the histogram covers a subset of the full graph.
	Sampled bool `json:"sampled,omitempty"`
}

// CitationGraphResult holds the seed paper and its citation graph.
type CitationGraphResult struct {
	Seed            Paper          `json:"seed"`
	References      []CitationEdge `json:"references,omitempty"`
	Citations       []CitationEdge `json:"citations,omitempty"`
	TotalReferences int            `json:"total_references"`
	TotalCitations  int            `json:"total_citations"`
	Direction       string         `json:"direction"`
	Limit           int            `json:"limit"`
	Trend           *CitationTrend `json:"trend,omitempty"`
}

// SearchFilters holds optional constraints for search queries.
// Backends apply these as native API filters where possible;
// remaining filters are applied post-retrieval.
type SearchFilters struct {
	YearMin      int    `json:"year_min,omitempty"`
	YearMax      int    `json:"year_max,omitempty"`
	MinCitations int    `json:"min_citations,omitempty"`
	Domain       string `json:"domain,omitempty"`
}

// IsEmpty returns true when no filters are set.
func (f SearchFilters) IsEmpty() bool {
	return f.YearMin == 0 && f.YearMax == 0 && f.MinCitations == 0 && f.Domain == ""
}

// WikipediaBackend searches Wikipedia for general knowledge.
type WikipediaBackend interface {
	// Name returns "wikipedia".
	Name() string

	// Search returns articles matching the query.
	Search(ctx context.Context, query string, limit int) ([]WikipediaArticle, error)

	// HealthCheck tests if the backend is reachable.
	HealthCheck(ctx context.Context) HealthStatus
}
