package backends

import (
	"regexp"
	"strings"
)

// xmlTagRe matches XML/HTML tags (including JATS: <jats:p>, <jats:italic>, etc.).
var xmlTagRe = regexp.MustCompile(`<[^>]+>`)

// stripXMLTags removes XML/HTML tags and collapses whitespace runs.
// Used to clean JATS markup from Crossref abstracts.
func stripXMLTags(s string) string {
	s = xmlTagRe.ReplaceAllString(s, " ")
	// Collapse runs of whitespace to a single space.
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

// Stopwords are common words filtered out when checking query relevance.
var Stopwords = map[string]bool{
	// articles & determiners
	"a": true, "an": true, "the": true, "this": true, "that": true,
	"these": true, "those": true,
	// words too common in titles to count as evidence of relevance:
	// "the new monogamy" matched AutoDock Vina's "a new scoring
	// function" on "new" alone (2026-11-15)
	"new": true,
	// be-verbs & auxiliaries
	"is": true, "are": true, "was": true, "were": true, "been": true,
	"be": true, "am": true,
	"do": true, "does": true, "did": true,
	"has": true, "have": true, "had": true,
	"can": true, "could": true, "will": true, "would": true,
	"should": true, "shall": true, "may": true, "might": true, "must": true,
	// question words
	"what": true, "how": true, "why": true, "when": true, "where": true,
	"which": true, "who": true, "whom": true, "whose": true,
	// conjunctions & prepositions
	"and": true, "or": true, "but": true, "nor": true, "not": true,
	"of": true, "to": true, "in": true, "on": true, "at": true,
	"by": true, "for": true, "with": true, "from": true, "up": true,
	"into": true, "through": true, "during": true, "before": true,
	"after": true, "above": true, "below": true, "between": true,
	"about": true, "against": true, "along": true, "among": true,
	// pronouns
	"it": true, "its": true, "he": true, "she": true, "his": true,
	"her": true, "they": true, "them": true, "their": true, "we": true,
	"our": true, "you": true, "your": true,
	// common filler
	"so": true, "if": true, "as": true, "no": true, "yes": true,
	"than": true, "then": true, "there": true, "here": true,
	"also": true, "just": true, "very": true, "much": true, "many": true,
	"more": true, "most": true, "some": true, "any": true, "all": true,
	"each": true, "every": true, "both": true, "few": true, "own": true,
	"such": true, "only": true, "same": true, "other": true,
}

// isAcronym returns true for short all-caps tokens (e.g. AI, ML, UK).
func isAcronym(s string) bool {
	if len(s) < 2 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// ExtractQueryTerms returns significant words from a query (lowercase, no stopwords).
// Short all-caps tokens (AI, ML, UK) are preserved as query terms.
func ExtractQueryTerms(query string) []string {
	words := strings.Fields(query)
	terms := make([]string, 0, len(words))
	for _, w := range words {
		raw := strings.Trim(w, ".,?!\"'")
		if isAcronym(raw) {
			terms = append(terms, strings.ToLower(raw))
			continue
		}
		low := strings.ToLower(raw)
		if len(low) >= 3 && !Stopwords[low] {
			terms = append(terms, low)
		}
	}
	return terms
}

// spellingVariants maps UK English terms to their US equivalents and
// vice versa. Used to match papers across spelling conventions (e.g.
// "labour" in the query matches "labor" in a paper title).
var spellingVariants map[string]string

func init() {
	// Define UK→US pairs; the init builds the bidirectional map.
	pairs := [][2]string{
		{"labour", "labor"},
		{"behaviour", "behavior"},
		{"behavioural", "behavioral"},
		{"organisation", "organization"},
		{"organisational", "organizational"},
		{"organise", "organize"},
		{"organised", "organized"},
		{"analyse", "analyze"},
		{"analysed", "analyzed"},
		{"colour", "color"},
		{"favour", "favor"},
		{"favourable", "favorable"},
		{"honour", "honor"},
		{"defence", "defense"},
		{"offence", "offense"},
		{"licence", "license"},
		{"practise", "practice"},
		{"specialise", "specialize"},
		{"specialised", "specialized"},
		{"recognise", "recognize"},
		{"recognised", "recognized"},
		{"emphasise", "emphasize"},
		{"characterise", "characterize"},
		{"characterised", "characterized"},
		{"modelling", "modeling"},
		{"counselling", "counseling"},
		{"travelling", "traveling"},
		{"ageing", "aging"},
		{"socialisation", "socialization"},
		{"globalisation", "globalization"},
		{"industrialisation", "industrialization"},
		{"urbanisation", "urbanization"},
		{"radicalisation", "radicalization"},
		{"utilisation", "utilization"},
		{"polarisation", "polarization"},
		{"centralisation", "centralization"},
		{"marginalisation", "marginalization"},
		{"normalisation", "normalization"},
		{"conceptualisation", "conceptualization"},
		{"operationalisation", "operationalization"},
	}
	spellingVariants = make(map[string]string, len(pairs)*2)
	for _, p := range pairs {
		spellingVariants[p[0]] = p[1]
		spellingVariants[p[1]] = p[0]
	}
}

// SpellingVariant returns the UK/US variant of a term, or empty
// string if no variant exists.
func SpellingVariant(term string) string {
	return spellingVariants[term]
}

// QuerySpellingVariant returns a copy of the query with UK/US
// spelling variants swapped. Returns empty string if no terms
// have variants.
func QuerySpellingVariant(query string) string {
	words := strings.Fields(query)
	changed := false
	out := make([]string, len(words))
	for i, w := range words {
		low := strings.ToLower(w)
		if v, ok := spellingVariants[low]; ok {
			out[i] = v
			changed = true
		} else {
			out[i] = w
		}
	}
	if !changed {
		return ""
	}
	return strings.Join(out, " ")
}
