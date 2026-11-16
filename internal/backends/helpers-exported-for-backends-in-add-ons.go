package backends

import "net/http"

// An add-on's backends live outside this package, so the helpers core's
// own backends share are exported here under names that say what they
// do, as thin wrappers: core's code keeps calling the originals.

// EnsureRateLimit installs a limiter for a backend unless one exists
// (ensureRateLimit).
func EnsureRateLimit(backend string, requestsPerSecond float64, burst int) {
	ensureRateLimit(backend, requestsPerSecond, burst)
}

// HTTPClientUnderneath is the client's own *http.Client, for a request
// the shared Do cannot make (a login that must not follow redirects).
// Use Do for everything else: it is what applies the rate limit.
func (c *Client) HTTPClientUnderneath() *http.Client {
	return c.http
}

// NormalizeDOI lowercases a DOI and strips any URL or "doi:" prefix.
func NormalizeDOI(doi string) string { return normalizeDOI(doi) }

// WordOverlapSimilarity is the Jaccard similarity of two strings'
// normalised words, 0 to 1.
func WordOverlapSimilarity(a, b string) float64 { return jaccardSimilarity(a, b) }

// Tokenize splits text into normalised words.
func Tokenize(text string) []string { return tokenize(text) }

// YearGapPenalty scales a match score down as the years diverge.
func YearGapPenalty(queryYear, workYear int) float64 { return yearPenalty(queryYear, workYear) }

// FamilyNameOf is an author's family name, from "Given Family" or
// "Family, Given".
func FamilyNameOf(author string) string { return extractFamilyName(author) }

// FirstAuthorMatchesAny is whether the query's first author's family
// name is among the work's (true when either side has none).
func FirstAuthorMatchesAny(queryAuthors, workFamilyNames []string) bool {
	return firstAuthorMatchesAny(queryAuthors, workFamilyNames)
}
