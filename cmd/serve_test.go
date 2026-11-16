package cmd

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestLoggedToolHandlerTellsAClientCancellationFromAToolError: when an
// MCP client stops waiting, its call's context is cancelled and the
// tool usually returns an error result; the journal must say the client
// cancelled, not that the tool failed.
func TestLoggedToolHandlerTellsAClientCancellationFromAToolError(t *testing.T) {
	var logged bytes.Buffer
	log.SetOutput(&logged)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(os.Stderr); log.SetFlags(log.LstdFlags) })
	failing := loggedToolHandler("research_question", func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("stopped"), nil
	})

	failing(context.Background(), mcp.CallToolRequest{})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	failing(cancelled, mcp.CallToolRequest{})

	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "tool=research_question tier=full outcome=tool-error") ||
		!strings.Contains(lines[1], "tool=research_question tier=full outcome=client-cancelled") {
		t.Errorf("logged:\n%s", logged.String())
	}
}
