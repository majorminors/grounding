package cmd

import (
	"context"
	"log"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"btr.mt/grounding/internal/tools"
)

// A research session is `grounding serve` started by the Claude Code
// research runner, one process per research_question run. It serves
// only the researcher's tools, puts one ledger in every call's context
// (so the tools record into it and web_fetch checks it), and rewrites
// the ledger file after each call, since the runner reads the file
// when Claude Code finishes and the process is then simply ended.

var (
	researchSessionLedgerFile    string
	researchSessionID            string
	researchSessionFetchableURLs []string
)

func init() {
	serveCmd.Flags().StringVar(&researchSessionLedgerFile, "research-session-ledger-file", "",
		"serve only the researcher's tools for one research_question run, keeping its ledger in this file (set by the Claude Code research runner)")
	serveCmd.Flags().StringVar(&researchSessionID, "research-session-id", "",
		"the research run's session ID (with --research-session-ledger-file)")
	serveCmd.Flags().StringArrayVar(&researchSessionFetchableURLs, "research-session-fetchable-url", nil,
		"a URL written in the research question, which web_fetch may take (repeatable; with --research-session-ledger-file)")
}

// servingResearchSession reports whether this serve is a research
// session.
func servingResearchSession() bool {
	return researchSessionLedgerFile != ""
}

// toolIsServedInThisSession leaves out every tool but the researcher's
// in a research session.
func toolIsServedInThisSession(name string) bool {
	if !servingResearchSession() {
		return true
	}
	for _, researchTool := range tools.ResearchQuestionTools {
		if name == researchTool {
			return true
		}
	}
	return false
}

// newResearchSessionLedger starts the session's ledger and writes it
// once, so the runner finds a file even if no tool is ever called.
func newResearchSessionLedger() (*tools.ResearchLedger, error) {
	ledger := tools.NewResearchLedger(time.Now())
	if researchSessionID != "" {
		ledger.SessionID = researchSessionID
	}
	ledger.AllowFetching(researchSessionFetchableURLs)
	return ledger, ledger.WriteSnapshotFile(researchSessionLedgerFile)
}

// researchSessionToolHandler counts each call into the ledger and
// rewrites the ledger file after it.
func researchSessionToolHandler(ledger *tools.ResearchLedger, ledgerFile string, handler tools.HandlerFunc) tools.HandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		result, err := handler(tools.WithResearchLedger(ctx, ledger), request)
		failure := ""
		switch {
		case err != nil:
			failure = err.Error()
		case result != nil && result.IsError:
			for _, content := range result.Content {
				failure += mcp.GetTextFromContent(content)
			}
		}
		ledger.CountToolCall(request.Params.Name, failure)
		if writeErr := ledger.WriteSnapshotFile(ledgerFile); writeErr != nil {
			log.Printf("research session: writing the ledger: %v", writeErr)
		}
		return result, err
	}
}
