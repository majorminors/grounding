package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"btr.mt/grounding/internal/config"
	"btr.mt/grounding/internal/tools"
)

func TestToolsRegistered(t *testing.T) {
	// Tools are registered via init() when cmd package is imported
	allTools := tools.All()

	// Should have 4 tools registered
	if len(allTools) < 4 {
		t.Fatalf("expected at least 4 tools, got %d", len(allTools))
	}

	// Check that expected tools are present
	toolNames := make(map[string]bool)
	for _, tool := range allTools {
		toolNames[tool.Name] = true
	}

	expectedTools := []string{"check_backends", "check_citation", "search_catalogue", "citation_graph"}
	for _, name := range expectedTools {
		if !toolNames[name] {
			t.Errorf("expected tool %q to be registered", name)
		}
	}
}

func TestToolsHaveHandlers(t *testing.T) {
	allTools := tools.All()

	for _, tool := range allTools {
		if tool.Handler == nil {
			t.Errorf("tool %q has nil handler", tool.Name)
		}
	}
}

func TestToolsHaveCLIMappings(t *testing.T) {
	allTools := tools.All()

	for _, tool := range allTools {
		if len(tool.CLI.Command) == 0 {
			t.Errorf("tool %q has no CLI command mapping", tool.Name)
		}
	}
}

func TestCheckBackendsHandler(t *testing.T) {
	// This is an integration test that actually calls backends
	// Skip in short mode
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	request := mcp.CallToolRequest{}

	result, err := handleCheckBackends(ctx, request)
	if err != nil {
		t.Fatalf("handleCheckBackends failed: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Result should be JSON with backends and healthy fields
	content := result.Content
	if len(content) == 0 {
		t.Fatal("expected non-empty content")
	}

	// Parse the text content
	textContent, ok := content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", content[0])
	}

	var healthResult tools.HealthResult
	if err := json.Unmarshal([]byte(textContent.Text), &healthResult); err != nil {
		t.Fatalf("failed to parse health result: %v", err)
	}

	if healthResult.Backends == nil {
		t.Error("expected backends map to be non-nil")
	}
}

func TestCheckCitationHandler_MissingInput(t *testing.T) {
	ctx := context.Background()
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "check_citation",
			Arguments: map[string]any{},
		},
	}

	result, err := handleCheckCitation(ctx, request)
	if err != nil {
		t.Fatalf("handleCheckCitation failed: %v", err)
	}

	// Should return error result, not nil
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Check it's an error result
	if !result.IsError {
		t.Error("expected error result when neither doi nor title provided")
	}
}

func TestSearchCatalogueHandler_MissingQuery(t *testing.T) {
	ctx := context.Background()
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "search_catalogue",
			Arguments: map[string]any{},
		},
	}

	result, err := handleSearchCatalogue(ctx, request)
	if err != nil {
		t.Fatalf("handleSearchCatalogue failed: %v", err)
	}

	// Should return error result
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if !result.IsError {
		t.Error("expected error result when query not provided")
	}
}

func TestCitationGraphHandler_MissingInput(t *testing.T) {
	ctx := context.Background()
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "citation_graph",
			Arguments: map[string]any{},
		},
	}

	result, err := handleCitationGraph(ctx, request)
	if err != nil {
		t.Fatalf("handleCitationGraph failed: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if !result.IsError {
		t.Error("expected error result when paper_id not provided")
	}
}

func TestSchemaCommand_OutputsJSON(t *testing.T) {
	// Capture stdout
	var buf bytes.Buffer

	allTools := tools.All()
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(allTools); err != nil {
		t.Fatalf("failed to encode tools: %v", err)
	}

	// Verify output is valid JSON array
	var decoded []tools.ToolDef
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("schema output is not valid JSON: %v", err)
	}

	if len(decoded) < 4 {
		t.Errorf("expected at least 4 tools in schema, got %d", len(decoded))
	}

	// Verify each tool has required fields
	for _, tool := range decoded {
		if tool.Name == "" {
			t.Error("tool has empty name")
		}
		if tool.Description == "" {
			t.Errorf("tool %q has empty description", tool.Name)
		}
		if tool.InputSchema.Type == "" {
			t.Errorf("tool %q has empty inputSchema type", tool.Name)
		}
		if len(tool.CLI.Command) == 0 {
			t.Errorf("tool %q has empty CLI command", tool.Name)
		}
	}
}

func TestServeCommand_ToolsRegisteredCorrectly(t *testing.T) {
	// Verify that all tools have the required fields for MCP server registration
	allTools := tools.All()

	for _, tool := range allTools {
		// Handler is required for serve
		if tool.Handler == nil {
			t.Errorf("tool %q has nil handler - serve will skip it", tool.Name)
		}

		// InputSchema must have a type
		if tool.InputSchema.Type == "" {
			t.Errorf("tool %q has empty inputSchema.type", tool.Name)
		}

		// Verify schema can be marshaled (required for NewToolWithRawSchema)
		if len(tool.InputSchema.Properties) > 0 {
			_, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Errorf("tool %q inputSchema failed to marshal: %v", tool.Name, err)
			}
		}
	}
}

// Public sessions receive every public tool's full input schema, and
// the landing page prints parameter descriptions for everyone. Only
// the top-level description is chosen per tier, so a parameter
// description that names a full-tier backend leaks it to the public.
func TestPublicToolParametersNameNoHiddenBackend(t *testing.T) {
	// Whole words only: "Example" contains "Exa" and is not a leak.
	hidden := regexp.MustCompile(`(?i)\b(consensus|exa|ann[a]|brave|firecrawl|haiku)\b`)
	for _, tool := range tools.All() {
		if tool.RequiresAuth {
			continue
		}
		for name, property := range tool.InputSchema.Properties {
			if match := hidden.FindString(property.Description); match != "" {
				t.Errorf("%s.%s names a full-tier backend (%q) in a parameter description every caller sees", tool.Name, name, match)
			}
		}
		if match := hidden.FindString(tool.Description); match != "" {
			t.Errorf("%s public description names a full-tier backend (%q); put it in FullTierDescription", tool.Name, match)
		}
		if match := hidden.FindString(tool.Title + " " + tool.SummaryForPeople); match != "" {
			t.Errorf("%s title or landing-page summary names a full-tier backend (%q)", tool.Name, match)
		}
	}
}

func TestResearchQuestionCostNoteFollowsTheConfiguredRunner(t *testing.T) {
	if !strings.Contains(researchQuestionFullTierCostsFor(config.Config{}), "Claude API") {
		t.Error("without a subscription token the note should name the API")
	}
	onSubscription := researchQuestionFullTierCostsFor(config.Config{ResearchClaudeCodeOAuthToken: "token"})
	if !strings.Contains(onSubscription, "subscription") || strings.Contains(onSubscription, "Claude API") {
		t.Errorf("with a token the note should name the subscription: %s", onSubscription)
	}
}
