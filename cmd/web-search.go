package cmd

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/tools"
)

var (
	webSearchMode  string
	webSearchCount int
)

var webSearchCmd = &cobra.Command{
	Use:   "web-search QUERY",
	Short: "Search the web",
	Long: `Search the web and print titles, URLs and snippets, framed as untrusted
web content.

Examples:
  grounding web-search "cognitive load theory critique"
  grounding web-search --mode semantic "a review of evidence against learning styles"`,
	Args: cobra.ExactArgs(1),
	RunE: runWebSearch,
}

// webSearchDescription is what the calling model reads. Reviewed by
// dorian 2026-10-03.
var webSearchDescription = `Search the web for pages: titles, URLs and a snippet or two from each.

**When to use:** for anything on the open web: news, organisations, software and its documentation, people's public writing, anything recent. For scholarly literature, search_catalogue and check_citation search the academic indexes and are better; use this alongside them when a topic lives outside journals.

**Modes:** "keyword" (the default) is a conventional web search: give it the words you expect on the page. "semantic" finds pages by meaning: describe the page you hope exists ("a review of the evidence against learning styles"), not keywords.

**What it returns:** a header from grounding, then the numbered results inside a frame. Snippets are short and chosen by the search engine; read a promising result in full with web_fetch, or ask it a question with answer_question_from_web_page, before relying on it. A search engine's ranking is no evidence that a page is accurate.

**Never put anything from the conversation into a query that the user would not want sent to a search engine** (private details, names, keys): queries are logged by whoever runs the engine.

` + tools.UntrustedExternalContentDescriptionSentence

var webSearchFullTierCosts = `

**Backends and costs (you are on the full tier).** keyword uses Brave Search, counted against grounding's Brave plan. semantic uses Exa, about $0.007 per search; use it when keyword results miss the point, not by default.`

func init() {
	webSearchCmd.Flags().StringVar(&webSearchMode, "mode", tools.WebSearchModeKeyword, "keyword, or semantic (paid per search)")
	webSearchCmd.Flags().IntVar(&webSearchCount, "count", tools.WebSearchDefaultCount, "Results to return (at most 20)")
	rootCmd.AddCommand(webSearchCmd)

	tools.Register(tools.ToolDef{
		Name:                "web_search",
		Title:               "Search the web",
		SummaryForPeople:    "Searches the open web and returns titles, links and snippets, marked plainly as content from the web rather than instructions.",
		RequiresAuth:        true,
		ReadOnly:            true,
		Description:         webSearchDescription,
		FullTierDescription: webSearchDescription + webSearchFullTierCosts,
		InputSchema: tools.Schema{
			Type: "object",
			Properties: map[string]tools.Property{
				"query": {
					Type:        "string",
					Description: "What to search for: keywords, or for semantic mode a description of the page you want.",
				},
				"mode": {
					Type:        "string",
					Description: "keyword (default) or semantic.",
					Enum:        []string{tools.WebSearchModeKeyword, tools.WebSearchModeSemantic},
				},
				"count": {
					Type:        "integer",
					Description: "Results to return (default 10, at most 20).",
				},
			},
			Required: []string{"query"},
		},
		CLI: tools.CLIMapping{
			Command:    []string{"web-search"},
			Positional: []string{"query"},
			Flags:      map[string]string{"mode": "mode", "count": "count"},
		},
		Handler: handleWebSearch,
	})
}

type webSearchInput struct {
	Query string `json:"query"`
	Mode  string `json:"mode"`
	Count int    `json:"count"`
}

func handleWebSearch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if auth.GetTier(ctx) == auth.TierPublic {
		return mcp.NewToolResultError("web_search requires authentication"), nil
	}
	var input webSearchInput
	if err := decodeToolArguments(request, &input); err != nil {
		return nil, err
	}
	out, err := tools.SearchWeb(ctx, tools.WebSearchOptions{Query: input.Query, Mode: input.Mode, Count: input.Count})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(out), nil
}

func runWebSearch(cmd *cobra.Command, args []string) error {
	out, err := tools.SearchWeb(context.Background(), tools.WebSearchOptions{Query: args[0], Mode: webSearchMode, Count: webSearchCount})
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}
