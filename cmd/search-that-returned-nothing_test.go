package cmd

import (
	"strings"
	"testing"

	"btr.mt/grounding/internal/backends"
)

func TestSearchClaimsNoResultsFoundOnlyForACleanNegative(t *testing.T) {
	if got := lineForASearchThatReturnedNothing(&backends.ContextResult{}); got != "No results found." {
		t.Errorf("clean negative: got %q", got)
	}

	got := lineForASearchThatReturnedNothing(&backends.ContextResult{EmptyResultExplanation: "5 results were retrieved but all were filtered out."})
	if strings.Contains(got, "No results found") {
		t.Errorf("a filter decision must not read as absence: got %q", got)
	}
	if !strings.Contains(got, "filtered out") {
		t.Errorf("the explanation must reach the terminal: got %q", got)
	}
}
