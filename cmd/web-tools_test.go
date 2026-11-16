package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/tools"
)

var webToolNames = []string{"web_fetch", "web_search", "answer_question_from_web_page"}

// The web tools spend paid quota and would make the server an open
// fetcher, so a public caller is refused even if one reaches them.
func TestWebToolsRefusePublicCallers(t *testing.T) {
	publicContext := auth.WithTier(context.Background(), auth.TierPublic)
	for _, name := range webToolNames {
		tool, ok := tools.Lookup(name)
		if !ok {
			t.Fatalf("%s not registered", name)
		}
		if !tool.RequiresAuth || !tool.ReadOnly {
			t.Errorf("%s must be full tier only and read-only", name)
		}
		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]any{"url": "https://example.org/", "query": "x", "question": "x"}
		result, err := tool.Handler(publicContext, request)
		if err != nil || result == nil || !result.IsError {
			t.Errorf("%s did not refuse a public caller", name)
		}
	}
}

// Each web tool tells the caller its content is untrusted, in the one
// shared sentence.
func TestWebToolsCarryTheUntrustedContentSentence(t *testing.T) {
	for _, name := range webToolNames {
		tool, _ := tools.Lookup(name)
		if !strings.Contains(tool.Description, tools.UntrustedExternalContentDescriptionSentence) {
			t.Errorf("%s description lacks the untrusted-content sentence", name)
		}
	}
}
