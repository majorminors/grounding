package tools

import (
	"btr.mt/grounding/internal/auth"
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestRegisterAndAll(t *testing.T) {
	Reset()

	tool := ToolDef{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]Property{
				"query": {Type: "string", Description: "Search query"},
			},
			Required: []string{"query"},
		},
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	}

	Register(tool)

	all := All()
	if len(all) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(all))
	}
	if all[0].Name != "test_tool" {
		t.Errorf("expected name 'test_tool', got %q", all[0].Name)
	}
	if all[0].Description != "A test tool" {
		t.Errorf("expected description 'A test tool', got %q", all[0].Description)
	}
}

func TestSchemaTypes(t *testing.T) {
	min := 1.0
	max := 100.0

	prop := Property{
		Type:        "integer",
		Description: "Number of results",
		Minimum:     &min,
		Maximum:     &max,
		Default:     5,
	}

	if prop.Type != "integer" {
		t.Errorf("expected type 'integer', got %q", prop.Type)
	}
	if *prop.Minimum != 1.0 {
		t.Errorf("expected minimum 1.0, got %f", *prop.Minimum)
	}
	if *prop.Maximum != 100.0 {
		t.Errorf("expected maximum 100.0, got %f", *prop.Maximum)
	}
	if prop.Default != 5 {
		t.Errorf("expected default 5, got %v", prop.Default)
	}
}

func TestReset(t *testing.T) {
	Reset()

	Register(ToolDef{Name: "tool1"})
	Register(ToolDef{Name: "tool2"})

	if len(All()) != 2 {
		t.Fatalf("expected 2 tools before reset, got %d", len(All()))
	}

	Reset()

	if len(All()) != 0 {
		t.Errorf("expected 0 tools after reset, got %d", len(All()))
	}
}

func TestCLIMapping(t *testing.T) {
	mapping := CLIMapping{
		Command:    []string{"search-catalogue"},
		Positional: []string{"query"},
		Flags: map[string]string{
			"source_type": "source",
			"max_results": "limit",
		},
	}

	if len(mapping.Command) != 1 || mapping.Command[0] != "search-catalogue" {
		t.Errorf("expected command ['search-catalogue'], got %v", mapping.Command)
	}
	if len(mapping.Positional) != 1 || mapping.Positional[0] != "query" {
		t.Errorf("expected positional ['query'], got %v", mapping.Positional)
	}
	if mapping.Flags["source_type"] != "source" {
		t.Errorf("expected flag mapping source_type->source, got %v", mapping.Flags["source_type"])
	}
	if mapping.Flags["max_results"] != "limit" {
		t.Errorf("expected flag mapping max_results->limit, got %v", mapping.Flags["max_results"])
	}
}

func TestToolDefWithCLI(t *testing.T) {
	Reset()

	tool := ToolDef{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]Property{
				"query": {Type: "string", Description: "Search query"},
			},
			Required: []string{"query"},
		},
		CLI: CLIMapping{
			Command:    []string{"test"},
			Positional: []string{"query"},
		},
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	}

	Register(tool)

	all := All()
	if len(all) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(all))
	}

	registered := all[0]
	if len(registered.CLI.Command) != 1 || registered.CLI.Command[0] != "test" {
		t.Errorf("expected CLI command ['test'], got %v", registered.CLI.Command)
	}
	if len(registered.CLI.Positional) != 1 || registered.CLI.Positional[0] != "query" {
		t.Errorf("expected CLI positional ['query'], got %v", registered.CLI.Positional)
	}
}

func TestToolDefJSONSerialization(t *testing.T) {
	tool := ToolDef{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]Property{
				"query": {Type: "string", Description: "Search query"},
			},
		},
		CLI: CLIMapping{
			Command: []string{"test"},
			Flags:   map[string]string{"query": "q"},
		},
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	}

	// Marshal to JSON
	jsonBytes, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("failed to marshal tool: %v", err)
	}

	// Verify expected fields are present
	var decoded map[string]any
	if err := json.Unmarshal(jsonBytes, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded["name"] != "test_tool" {
		t.Errorf("expected name 'test_tool', got %v", decoded["name"])
	}
	if decoded["description"] != "A test tool" {
		t.Errorf("expected description 'A test tool', got %v", decoded["description"])
	}

	// Handler should not be serialized (json:"-")
	if _, ok := decoded["Handler"]; ok {
		t.Error("Handler should not be serialized")
	}

	// CLI mapping should be present
	cli, ok := decoded["cli"].(map[string]any)
	if !ok {
		t.Fatal("expected cli to be an object")
	}
	command, ok := cli["command"].([]any)
	if !ok || len(command) != 1 || command[0] != "test" {
		t.Errorf("expected cli.command ['test'], got %v", cli["command"])
	}
}

func TestDescriptionForTier(t *testing.T) {
	plain := ToolDef{Name: "plain", Description: "public words"}
	if got := plain.DescriptionForTier(auth.TierFull); got != "public words" {
		t.Errorf("no full-tier text set: full tier should see the public description, got %q", got)
	}

	costed := ToolDef{
		Name:                "costed",
		Description:         "public words",
		FullTierDescription: "public words plus what it costs",
	}
	if got := costed.DescriptionForTier(auth.TierPublic); got != "public words" {
		t.Errorf("public tier must never see the costed text, got %q", got)
	}
	if got := costed.DescriptionForTier(auth.TierFull); got != "public words plus what it costs" {
		t.Errorf("full tier should see the costed text, got %q", got)
	}
}
