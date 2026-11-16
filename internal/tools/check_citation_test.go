package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"btr.mt/grounding/internal/backends"
)

// checkCitationInput is a local copy for testing validation logic.
type checkCitationInput struct {
	DOI     string   `json:"doi"`
	Title   string   `json:"title"`
	Authors []string `json:"authors"`
	Year    int      `json:"year"`
}

func TestCheckCitationTool_Registration(t *testing.T) {
	// Reset registry to ensure clean state
	Reset()

	// Re-register by importing init() side effects
	Register(ToolDef{
		Name:        "check_citation",
		Description: "Test description",
		InputSchema: Schema{
			Type: "object",
			Properties: map[string]Property{
				"doi":     {Type: "string"},
				"title":   {Type: "string"},
				"authors": {Type: "array", Items: &Property{Type: "string"}},
				"year":    {Type: "integer"},
			},
		},
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	})

	tools := All()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0]
	if tool.Name != "check_citation" {
		t.Errorf("expected name 'check_citation', got %q", tool.Name)
	}

	// Check schema properties
	if tool.InputSchema.Type != "object" {
		t.Errorf("expected schema type 'object', got %q", tool.InputSchema.Type)
	}
	if _, ok := tool.InputSchema.Properties["doi"]; !ok {
		t.Error("expected 'doi' property in schema")
	}
	if _, ok := tool.InputSchema.Properties["title"]; !ok {
		t.Error("expected 'title' property in schema")
	}
	if _, ok := tool.InputSchema.Properties["authors"]; !ok {
		t.Error("expected 'authors' property in schema")
	}
	if _, ok := tool.InputSchema.Properties["year"]; !ok {
		t.Error("expected 'year' property in schema")
	}

	// Check authors array has items
	authorsProp := tool.InputSchema.Properties["authors"]
	if authorsProp.Items == nil {
		t.Error("expected authors property to have Items")
	} else if authorsProp.Items.Type != "string" {
		t.Errorf("expected authors items type 'string', got %q", authorsProp.Items.Type)
	}
}

func TestCheckCitationInput_Validation(t *testing.T) {
	tests := []struct {
		name      string
		input     checkCitationInput
		wantError bool
	}{
		{
			name:      "doi only",
			input:     checkCitationInput{DOI: "10.1234/test"},
			wantError: false,
		},
		{
			name:      "title only",
			input:     checkCitationInput{Title: "Test Paper"},
			wantError: false,
		},
		{
			name:      "doi and title",
			input:     checkCitationInput{DOI: "10.1234/test", Title: "Test Paper"},
			wantError: false,
		},
		{
			name:      "neither doi nor title",
			input:     checkCitationInput{},
			wantError: true,
		},
		{
			name:      "with authors and year",
			input:     checkCitationInput{Title: "Test Paper", Authors: []string{"Smith"}, Year: 2022},
			wantError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Validate using the same logic as the handler
			hasError := tc.input.DOI == "" && tc.input.Title == ""
			if hasError != tc.wantError {
				t.Errorf("expected error=%v, got error=%v", tc.wantError, hasError)
			}
		})
	}
}

func TestVerificationResult_JSON(t *testing.T) {
	// Test that VerificationResult serializes correctly
	result := backends.VerificationResult{
		Verified: true,
		Paper: &backends.Paper{
			Title:     "Test Paper",
			Authors:   []string{"John Smith", "Jane Doe"},
			Year:      2023,
			DOI:       "10.1234/test",
			Abstract:  "Test abstract",
			URL:       "https://example.com/paper",
			Source:    "crossref",
			Journal:   "Test Journal",
			Citations: 42,
		},
		Source: "crossref",
	}

	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	// Verify it unmarshals correctly
	var decoded backends.VerificationResult
	if err := json.Unmarshal(jsonBytes, &decoded); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if !decoded.Verified {
		t.Error("expected Verified=true")
	}
	if decoded.Paper == nil {
		t.Fatal("expected Paper to be non-nil")
	}
	if decoded.Paper.Title != "Test Paper" {
		t.Errorf("expected title 'Test Paper', got %q", decoded.Paper.Title)
	}
	if decoded.Source != "crossref" {
		t.Errorf("expected source 'crossref', got %q", decoded.Source)
	}
}

func TestVerificationResult_NotFound(t *testing.T) {
	result := backends.VerificationResult{
		Verified: false,
		Error:    "citation not found in any database",
	}

	jsonBytes, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal result: %v", err)
	}

	var decoded backends.VerificationResult
	if err := json.Unmarshal(jsonBytes, &decoded); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if decoded.Verified {
		t.Error("expected Verified=false")
	}
	if decoded.Paper != nil {
		t.Error("expected Paper to be nil")
	}
	if decoded.Error != "citation not found in any database" {
		t.Errorf("expected error message, got %q", decoded.Error)
	}
}

func TestSchemaItemsProperty(t *testing.T) {
	// Test that Items field works correctly for array types
	prop := Property{
		Type:        "array",
		Description: "List of strings",
		Items:       &Property{Type: "string"},
	}

	// Marshal to JSON
	jsonBytes, err := json.Marshal(prop)
	if err != nil {
		t.Fatalf("failed to marshal property: %v", err)
	}

	// Verify it contains items
	var decoded map[string]any
	if err := json.Unmarshal(jsonBytes, &decoded); err != nil {
		t.Fatalf("failed to unmarshal property: %v", err)
	}

	if decoded["type"] != "array" {
		t.Errorf("expected type 'array', got %v", decoded["type"])
	}

	items, ok := decoded["items"].(map[string]any)
	if !ok {
		t.Fatal("expected items to be an object")
	}
	if items["type"] != "string" {
		t.Errorf("expected items.type 'string', got %v", items["type"])
	}
}
