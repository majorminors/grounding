package tools

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"btr.mt/grounding/internal/backends"
)

// Quality score weights
const (
	weightRelevance = 0.70
	weightAbstract  = 0.15
	weightCitations = 0.15
)

// RelevanceScore calculates how well a paper matches query terms.
// Returns 0.0-1.0 based on term overlap in title and abstract.
//
// The score uses the number of matched terms rather than normalising
// by total query length, so that long multi-term queries don't penalise
// papers that match on a meaningful subset of terms.
func RelevanceScore(terms []string, paper backends.Paper) float64 {
	if len(terms) == 0 {
		return 1.0 // No terms to match = everything passes
	}

	titleLower := strings.ToLower(paper.Title)
	abstractLower := strings.ToLower(paper.Abstract)

	titleMatches := 0
	abstractMatches := 0

	for _, term := range terms {
		if containsTermOrVariant(titleLower, term) {
			titleMatches++
		}
		if containsTermOrVariant(abstractLower, term) {
			abstractMatches++
		}
	}

	// Score based on match counts, not normalised by total terms.
	// 2+ title matches or 3+ abstract matches should comfortably pass.
	titleScore := float64(titleMatches) * 0.25
	abstractScore := float64(abstractMatches) * 0.10

	// Cap component scores
	if titleScore > 1.0 {
		titleScore = 1.0
	}
	if abstractScore > 0.6 {
		abstractScore = 0.6
	}

	score := titleScore + abstractScore
	if score > 1.0 {
		score = 1.0
	}

	return score
}

// QualityScore combines relevance with quality signals (abstract presence, citations).
// Returns 0.0-1.0 combining:
//   - 70% relevance (query term matching)
//   - 15% abstract presence (binary boost)
//   - 15% citations (log-scaled, normalized to maxCitations)
func QualityScore(relevance float64, paper backends.Paper, maxCitations int) float64 {
	score := weightRelevance * relevance

	if strings.TrimSpace(paper.Abstract) != "" {
		score += weightAbstract
	}

	if maxCitations > 0 && paper.Citations > 0 {
		citationScore := math.Log10(1+float64(paper.Citations)) / math.Log10(1+float64(maxCitations))
		score += weightCitations * citationScore
	}

	return score
}

// citationFloor is the citation count above which a paper may pass the
// relevance filter with a reduced threshold. Requires 2+ title matches
// to prevent single-common-word STEM papers from leaking through.
const citationFloor = 100

// noAbstractThreshold is the relevance threshold for papers without
// abstracts (primarily Crossref results). Without an abstract to
// corroborate, a single title-word match (score 0.25) is insufficient
// signal — it produces cross-discipline garbage on common English words
// like "second", "class", "social". 0.3 effectively requires 2+ title
// matches (2 matches = 0.50, which clears the threshold).
const noAbstractThreshold = 0.3

// FilterByRelevance removes papers below threshold and sorts by quality score.
// Quality score combines relevance with abstract presence and citation count.
// Papers above citationFloor citations pass with a reduced threshold.
//
// A query that is a DOI asks for one work, not for a topic: only records
// carrying that DOI are kept, since a DOI never appears in the title or
// abstract that term matching reads.
func FilterByRelevance(query string, papers []backends.Paper, minScore float64) []backends.Paper {
	if doi := doiWhenTheWholeQueryIsOne(query); doi != "" {
		return papersCarryingDOI(papers, doi)
	}

	terms := backends.ExtractQueryTerms(query)

	// If no meaningful terms after filtering, return papers as-is
	if len(terms) == 0 {
		return papers
	}

	// First pass: compute relevance scores, filter by threshold, find max citations
	type scored struct {
		paper     backends.Paper
		relevance float64
		quality   float64
	}

	scoredPapers := make([]scored, 0, len(papers))
	maxCitations := 0
	for _, p := range papers {
		relevance := RelevanceScore(terms, p)
		threshold := minScore
		// Papers without abstracts need stronger title evidence.
		// Crossref keyword matching produces cross-discipline garbage
		// when a single common word matches a highly-cited STEM paper.
		if strings.TrimSpace(p.Abstract) == "" && threshold < noAbstractThreshold {
			threshold = noAbstractThreshold
		}
		// Highly-cited papers get a lower threshold, but only if they
		// match 2+ query terms in the TITLE specifically (not just
		// abstract). A paper whose title shares only one common word
		// with the query is almost certainly a false positive,
		// regardless of citation count.
		if p.Citations >= citationFloor {
			titleMatches := countTitleMatches(terms, p)
			if titleMatches >= 2 {
				threshold = 0.01
			}
		}
		// A book never has an abstract to match, so the stricter bar
		// above would hold every book of a one-term query out. A book
		// whose title carries two of the terms (or all of them, when
		// there are fewer) is relevant evidence enough.
		if p.Kind == backends.RecordKindBook && strings.TrimSpace(p.Abstract) == "" &&
			countTitleMatches(terms, p) >= min(2, len(terms)) {
			threshold = 0
		}
		if relevance >= threshold {
			scoredPapers = append(scoredPapers, scored{paper: p, relevance: relevance})
			if p.Citations > maxCitations {
				maxCitations = p.Citations
			}
		}
	}

	// Second pass: compute quality scores
	for i := range scoredPapers {
		scoredPapers[i].quality = QualityScore(
			scoredPapers[i].relevance,
			scoredPapers[i].paper,
			maxCitations,
		)
	}

	// Sort by quality score descending
	sort.Slice(scoredPapers, func(i, j int) bool {
		return scoredPapers[i].quality > scoredPapers[j].quality
	})

	// Extract papers
	result := make([]backends.Paper, len(scoredPapers))
	for i, sp := range scoredPapers {
		result[i] = sp.paper
	}

	return result
}

// wholeStringIsDOIPattern matches a normalised DOI and nothing else.
var wholeStringIsDOIPattern = regexp.MustCompile(`^10\.\d{4,9}/\S+$`)

// doiWhenTheWholeQueryIsOne returns the normalised DOI when the query is
// a DOI (bare, doi: or doi.org URL), and "" when it is anything else,
// including a DOI among other words.
func doiWhenTheWholeQueryIsOne(query string) string {
	doi := backends.NormalisedDOI(query)
	if wholeStringIsDOIPattern.MatchString(doi) {
		return doi
	}
	return ""
}

// papersCarryingDOI keeps the papers whose own DOI is the normalised doi.
func papersCarryingDOI(papers []backends.Paper, doi string) []backends.Paper {
	kept := make([]backends.Paper, 0, 1)
	for _, p := range papers {
		if p.DOI != "" && backends.NormalisedDOI(p.DOI) == doi {
			kept = append(kept, p)
		}
	}
	return kept
}

// countTitleMatches counts how many distinct query terms appear
// in the paper's title (checking UK/US spelling variants).
func countTitleMatches(terms []string, paper backends.Paper) int {
	titleLower := strings.ToLower(paper.Title)
	count := 0
	for _, term := range terms {
		if containsTermOrVariant(titleLower, term) {
			count++
		}
	}
	return count
}

// countDistinctMatches counts how many distinct query terms appear
// in the paper's title or abstract.
func countDistinctMatches(terms []string, paper backends.Paper) int {
	titleLower := strings.ToLower(paper.Title)
	abstractLower := strings.ToLower(paper.Abstract)
	count := 0
	for _, term := range terms {
		if containsTermOrVariant(titleLower, term) || containsTermOrVariant(abstractLower, term) {
			count++
		}
	}
	return count
}

// containsTermOrVariant checks whether text contains the term or its
// UK/US spelling variant as a whole word (word-boundary matching).
// This prevents "media" matching "mediator", "model" matching
// "modelling", etc.
func containsTermOrVariant(text, term string) bool {
	if containsWord(text, term) {
		return true
	}
	if v := backends.SpellingVariant(term); v != "" {
		return containsWord(text, v)
	}
	return false
}

// containsWord returns true if word appears in text at word boundaries.
// A word boundary is the start/end of the string, or any character that
// is not a letter or digit (spaces, hyphens, punctuation, etc.).
// Also matches plurals: "initiation" matches "initiations" (word + 's'
// at a word boundary) but "media" still doesn't match "mediator".
func containsWord(text, word string) bool {
	for idx := 0; idx <= len(text)-len(word); {
		pos := strings.Index(text[idx:], word)
		if pos < 0 {
			return false
		}
		pos += idx
		atStart := pos == 0 || !isWordChar(text[pos-1])
		if atStart {
			end := pos + len(word)
			atEnd := end >= len(text) || !isWordChar(text[end])
			// Accept plural: word + 's' at word boundary
			atPluralEnd := !atEnd && end < len(text) && text[end] == 's' &&
				(end+1 >= len(text) || !isWordChar(text[end+1]))
			if atEnd || atPluralEnd {
				return true
			}
		}
		idx = pos + 1
	}
	return false
}

// isWordChar returns true for letters and digits (ASCII).
func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
