package tools

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"

	"btr.mt/grounding/internal/auth"
)

// HandlerFunc is the signature for tool handlers.
type HandlerFunc func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)

// CLIMapping documents how MCP params map to CLI flags.
type CLIMapping struct {
	Command    []string          `json:"command"`              // CLI command path, e.g., ["health"] or ["verify"]
	Positional []string          `json:"positional,omitempty"` // Params that become positional args
	Flags      map[string]string `json:"flags,omitempty"`      // Param name -> flag name mapping
}

// ToolDef defines a tool with its metadata and handler.
type ToolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema Schema      `json:"inputSchema"`
	CLI         CLIMapping  `json:"cli"`
	Handler     HandlerFunc `json:"-"`

	// RequiresAuth marks a tool as full-tier only. The HTTP server hides
	// it from tools/list and the landing page for public callers, and
	// the handler rejects public calls as defence in depth.
	RequiresAuth bool `json:"requires_auth,omitempty"`

	// FullTierDescription, when set, is what full-tier sessions and the
	// CLI see in place of Description. It is where a tool spells out
	// the backends that cost money or quota; the public description
	// never names them.
	FullTierDescription string `json:"full_tier_description,omitempty"`

	// Title is the short human-readable name MCP clients show in their
	// tool pickers (the readOnlyHint's sibling annotation, "title").
	Title string `json:"title,omitempty"`

	// SummaryForPeople is one or two plain sentences for the landing
	// page. Description is written for the model and full of usage
	// instructions; this is what a person reads to decide whether the
	// tool is for them. Like Description, it must never name a hidden
	// backend.
	SummaryForPeople string `json:"summary_for_people,omitempty"`

	// ReadOnly marks a tool that changes nothing outside the server's
	// own process. It becomes the MCP readOnlyHint, which lets clients
	// run calls in parallel and skip confirmation prompts. Leave it
	// false for anything that writes a file.
	ReadOnly bool `json:"read_only,omitempty"`
}

// DescriptionForTier returns the description a session at the given
// tier should be shown.
func (t ToolDef) DescriptionForTier(tier auth.Tier) string {
	if tier == auth.TierFull && t.FullTierDescription != "" {
		return t.FullTierDescription
	}
	return t.Description
}

// Schema represents a JSON Schema for tool input.
type Schema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

// Property represents a JSON Schema property.
type Property struct {
	Type        string    `json:"type"`
	Description string    `json:"description,omitempty"`
	Enum        []string  `json:"enum,omitempty"`
	Default     any       `json:"default,omitempty"`
	Minimum     *float64  `json:"minimum,omitempty"`
	Maximum     *float64  `json:"maximum,omitempty"`
	Items       *Property `json:"items,omitempty"` // For array types
}

var registry []ToolDef

// Register adds a tool to the registry.
func Register(tool ToolDef) {
	registry = append(registry, tool)
}

// All returns all registered tools.
func All() []ToolDef {
	return registry
}

// Lookup returns the registered tool with the given name.
func Lookup(name string) (ToolDef, bool) {
	for _, tool := range registry {
		if tool.Name == name {
			return tool, true
		}
	}
	return ToolDef{}, false
}

// RequiresAuth reports whether the named tool is full-tier only.
func RequiresAuth(name string) bool {
	tool, ok := Lookup(name)
	return ok && tool.RequiresAuth
}

// Reset clears the registry (for testing).
func Reset() {
	registry = nil
}
