package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"btr.mt/grounding/internal/backends"
)

func ledgerWithOneArticleAndOnePaper() *ResearchLedger {
	ledger := newTestResearchLedger()
	ledger.RecordFetchedPage(&WebPageText{
		RequestedURL: "https://example.org/emotional-labour",
		FinalURL:     "https://example.org/emotional-labour",
		Title:        "Emotional labour, explained",
		Text: "Hochschild coined the term **emotional labour** in 1983.\n\n" +
			"Flight attendants were her main case. She argued that “managing feelings” was part of the job.",
	})
	ledger.RecordPapers("search_catalogue", []backends.Paper{{
		Title:    "The Managed Heart",
		DOI:      "10.1525/9780520951853",
		Abstract: "A study of the commercialisation of human feeling.",
	}})
	return ledger
}

func verdictsOf(t *testing.T, citations ...ResearchSourceCitation) []ResearchCitationVerdict {
	t.Helper()
	verified := VerifyResearchBackbrief(ResearchBackbrief{
		Findings: []ResearchFinding{{Claim: "a claim", Confidence: ResearchConfidenceHigh, Sources: citations}},
	}, ledgerWithOneArticleAndOnePaper())
	return verified.Findings[0].Verdicts
}

func TestBackbriefQuoteFromTheCitedSourceIsFound(t *testing.T) {
	verdicts := verdictsOf(t,
		// Markdown, typographic quotes and case differ from the page.
		ResearchSourceCitation{URL: "https://example.org/emotional-labour", Quote: `Hochschild coined the term emotional labour in 1983.`},
		ResearchSourceCitation{URL: "http://example.org/emotional-labour/", Quote: `She argued that "managing feelings" was part of the job.`},
		// By DOI, in another case and with a resolver prefix.
		ResearchSourceCitation{DOI: "https://doi.org/10.1525/9780520951853", Quote: "the commercialisation of human feeling"},
	)
	for _, verdict := range verdicts {
		if !verdict.SourceRetrieved || !verdict.QuoteFound {
			t.Errorf("%+v: want retrieved and found", verdict.Citation)
		}
	}
}

func TestBackbriefInventedSourceIsNotRetrieved(t *testing.T) {
	verdict := verdictsOf(t, ResearchSourceCitation{
		URL: "https://example.org/a-page-never-fetched", Quote: "Hochschild coined the term emotional labour in 1983.",
	})[0]
	if verdict.SourceRetrieved || verdict.QuoteFound {
		t.Errorf("%+v: an unretrieved source must fail both checks", verdict)
	}
}

func TestBackbriefQuoteMustComeFromTheSourceItIsAttributedTo(t *testing.T) {
	// The words are in the run, but in the article, not the paper.
	verdict := verdictsOf(t, ResearchSourceCitation{
		DOI: "10.1525/9780520951853", Quote: "Flight attendants were her main case.",
	})[0]
	if !verdict.SourceRetrieved || verdict.QuoteFound {
		t.Errorf("%+v: want retrieved but quote not found", verdict)
	}
}

func TestBackbriefParaphrasedOrStitchedQuoteIsNotFound(t *testing.T) {
	for _, quote := range []string{
		"Hochschild invented the phrase emotional labour in 1983.",
		// Two passages joined, skipping the sentence between them.
		"Hochschild coined the term emotional labour in 1983. She argued that managing feelings was part of the job.",
		"",
	} {
		verdict := verdictsOf(t, ResearchSourceCitation{URL: "https://example.org/emotional-labour", Quote: quote})[0]
		if verdict.QuoteFound {
			t.Errorf("%q should not be found", quote)
		}
	}
}

func TestBackbriefFindingIsSupportedOnlyByARetrievedSourceWithItsQuote(t *testing.T) {
	verified := VerifyResearchBackbrief(ResearchBackbrief{Findings: []ResearchFinding{
		{Claim: "supported", Sources: []ResearchSourceCitation{
			{URL: "https://example.org/nowhere", Quote: "x"},
			{URL: "https://example.org/emotional-labour", Quote: "Flight attendants were her main case."},
		}},
		{Claim: "unsupported", Sources: []ResearchSourceCitation{
			{URL: "https://example.org/emotional-labour", Quote: "words not on the page"},
		}},
		{Claim: "no sources"},
	}}, ledgerWithOneArticleAndOnePaper())
	want := []bool{true, false, false}
	for index, finding := range verified.Findings {
		if finding.Supported() != want[index] {
			t.Errorf("%q: supported %v, want %v", finding.Finding.Claim, finding.Supported(), want[index])
		}
	}
}

func TestBackbriefSchemaMatchesTheGoShape(t *testing.T) {
	// Every field the schema requires must decode into the struct, so a
	// schema change cannot silently drop a field.
	example := `{"summary":"s","findings":[{"claim":"c","confidence":"high","sources":[{"url":"u","doi":"d","quote":"q"}]}],"gaps":["g"],"text_addressed_to_ai":["t"]}`
	var backbrief ResearchBackbrief
	decoder := json.NewDecoder(strings.NewReader(example))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&backbrief); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(backbrief)
	if string(encoded) != example {
		t.Errorf("round trip:\n got %s\nwant %s", encoded, example)
	}
	required := researchBackbriefSchema["required"].([]string)
	if len(required) != 4 {
		t.Errorf("top-level required fields %v", required)
	}
}
