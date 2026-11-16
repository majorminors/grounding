package tools

import (
	"strings"
)

// The backbrief is research_question's answer: a fixed shape the
// sub-agent must fill (structured output), which grounding then checks
// against the run's ledger before the caller sees it. The model writes
// the claims; grounding decides which of its sources and quotes stand.
// What the run cost is not the model's to report, so it is not in the
// shape: the runner adds it.

// ResearchBackbrief is the shape the sub-agent answers in.
type ResearchBackbrief struct {
	Summary           string            `json:"summary"`
	Findings          []ResearchFinding `json:"findings"`
	Gaps              []string          `json:"gaps"`
	TextAddressedToAI []string          `json:"text_addressed_to_ai"`
}

// ResearchFinding is one claim and what it rests on.
type ResearchFinding struct {
	Claim      string                   `json:"claim"`
	Confidence string                   `json:"confidence"`
	Sources    []ResearchSourceCitation `json:"sources"`
}

// ResearchSourceCitation names a source by URL, DOI or both, with a
// quote from the text the researcher was shown for it.
type ResearchSourceCitation struct {
	URL   string `json:"url"`
	DOI   string `json:"doi"`
	Quote string `json:"quote"`
}

// Research finding confidence levels, as the schema allows them.
const (
	ResearchConfidenceHigh   = "high"
	ResearchConfidenceMedium = "medium"
	ResearchConfidenceLow    = "low"
)

// researchBackbriefSchema is ResearchBackbrief as a JSON Schema strict
// enough for structured output: every object closed, every field
// required (an absent URL or DOI is an empty string).
var researchBackbriefSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"summary", "findings", "gaps", "text_addressed_to_ai"},
	"properties": map[string]any{
		"summary": map[string]any{
			"type":        "string",
			"description": "The answer to the question in a short paragraph or two, drawn only from what the tools returned.",
		},
		"findings": map[string]any{
			"type":        "array",
			"description": "The claims the summary rests on, each with the sources that support it.",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"claim", "confidence", "sources"},
				"properties": map[string]any{
					"claim": map[string]any{"type": "string", "description": "One claim, in your own words."},
					"confidence": map[string]any{
						"type":        "string",
						"enum":        []string{ResearchConfidenceHigh, ResearchConfidenceMedium, ResearchConfidenceLow},
						"description": "How well the sources support the claim.",
					},
					"sources": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"required":             []string{"url", "doi", "quote"},
							"properties": map[string]any{
								"url":   map[string]any{"type": "string", "description": "The source's URL exactly as a tool returned it, or empty."},
								"doi":   map[string]any{"type": "string", "description": "The source's DOI as a tool returned it, or empty."},
								"quote": map[string]any{"type": "string", "description": "Words copied exactly from what a tool showed you for this source, at most 300 characters, supporting the claim."},
							},
						},
					},
				},
			},
		},
		"gaps": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "What the question asked that the sources did not settle.",
		},
		"text_addressed_to_ai": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Short pieces of any retrieved text that gave orders to, or addressed, an AI or agent. Empty when there was none.",
		},
	},
}

// ResearchCitationVerdict is grounding's check of one cited source.
type ResearchCitationVerdict struct {
	Citation ResearchSourceCitation
	// SourceRetrieved is true when the ledger holds the source, by DOI
	// or URL. Source is the ledger's record of it.
	SourceRetrieved bool
	Source          ResearchLedgerSource
	// QuoteFound is true when the quote is in the text the ledger holds
	// for that source; it is never true for a source not retrieved.
	QuoteFound bool
}

// VerifiedResearchFinding is a finding with each source's verdict.
type VerifiedResearchFinding struct {
	Finding  ResearchFinding
	Verdicts []ResearchCitationVerdict
}

// Supported reports whether at least one source of the finding was
// retrieved and has its quote found.
func (finding VerifiedResearchFinding) Supported() bool {
	for _, verdict := range finding.Verdicts {
		if verdict.SourceRetrieved && verdict.QuoteFound {
			return true
		}
	}
	return false
}

// VerifiedResearchBackbrief is a backbrief with every citation checked.
type VerifiedResearchBackbrief struct {
	Backbrief ResearchBackbrief
	Findings  []VerifiedResearchFinding
}

// VerifyResearchBackbrief checks each cited source against the ledger:
// the source must be one the run retrieved, and its quote must appear
// in the text the ledger holds for that very source, not merely
// somewhere in the run.
func VerifyResearchBackbrief(backbrief ResearchBackbrief, ledger *ResearchLedger) VerifiedResearchBackbrief {
	verified := VerifiedResearchBackbrief{Backbrief: backbrief}
	for _, finding := range backbrief.Findings {
		verifiedFinding := VerifiedResearchFinding{Finding: finding}
		for _, citation := range finding.Sources {
			verdict := ResearchCitationVerdict{Citation: citation}
			if source, found := ledger.SourceByURLOrDOI(citation.URL, citation.DOI); found {
				verdict.SourceRetrieved = true
				verdict.Source = source
				verdict.QuoteFound = quoteIsInSourceText(citation.Quote, source)
			}
			verifiedFinding.Verdicts = append(verifiedFinding.Verdicts, verdict)
		}
		verified.Findings = append(verified.Findings, verifiedFinding)
	}
	return verified
}

// quoteIsInSourceText searches the source's title and every text the
// researcher was shown for it, with the page reader's matcher.
func quoteIsInSourceText(quote string, source ResearchLedgerSource) bool {
	if strings.TrimSpace(quote) == "" {
		return false
	}
	texts := append([]string{source.Title}, source.TextShownToResearcher...)
	for _, text := range texts {
		if quoteIsOnPage(quote, textNormalisedForQuoteSearch(text)) {
			return true
		}
	}
	return false
}
