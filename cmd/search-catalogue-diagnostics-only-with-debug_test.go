package cmd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/backends"
)

// Naming only Consensus with no key skips it without a request:
// one diagnostic, one warning, nothing found.
func searchCatalogueThroughTheHandler(t *testing.T, debug bool) backends.ContextResult {
	t.Helper()
	t.Setenv("CONSENSUS_APP_KEY", "")
	arguments := map[string]any{"query": "tribe", "backends": []any{"consensus"}}
	if debug {
		arguments["debug"] = true
	}
	request := mcp.CallToolRequest{}
	request.Params.Arguments = arguments
	response, err := handleSearchCatalogue(auth.WithTier(context.Background(), auth.TierFull), request)
	if err != nil || response.IsError {
		t.Fatalf("response %+v, err %v", response, err)
	}
	var result backends.ContextResult
	if err := json.Unmarshal([]byte(response.Content[0].(mcp.TextContent).Text), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSearchCatalogueShowsDiagnosticsOnlyWhenAskedButWarningsAlways(t *testing.T) {
	plain := searchCatalogueThroughTheHandler(t, false)
	if len(plain.Diagnostics) != 0 || len(plain.Warnings) != 1 {
		t.Errorf("without debug: diagnostics %+v, warnings %q; want none and one", plain.Diagnostics, plain.Warnings)
	}
	debugged := searchCatalogueThroughTheHandler(t, true)
	if len(debugged.Diagnostics) != 1 || len(debugged.Warnings) != 1 {
		t.Errorf("with debug: diagnostics %+v, warnings %q; want one and one", debugged.Diagnostics, debugged.Warnings)
	}
}
