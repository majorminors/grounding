package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/tools"
)

var (
	webFetchRenderer   string
	webFetchMaxLength  int
	webFetchStartIndex int
)

var webFetchCmd = &cobra.Command{
	Use:   "web-fetch URL",
	Short: "Fetch a web page as markdown",
	Long: `Fetch a public web page and print its main content as markdown, a window
at a time, framed as untrusted web content.

Addresses that are not on the public internet (loopback, private networks,
the mesh, cloud metadata) are refused, including after redirects.

The default renderer, auto, climbs from a plain fetch to a crawl4ai browser
(CRAWL4AI_BASE_URL) and then Firecrawl (FIRECRAWL_API_KEY, within
FIRECRAWL_DAILY_CREDIT_CAP) only when the page needs it.

This product includes software developed by UncleCode (https://x.com/unclecode)
as part of the Crawl4AI project (https://github.com/unclecode/crawl4ai).

Examples:
  grounding web-fetch https://en.wikipedia.org/wiki/Cognitive_load
  grounding web-fetch --start-index 10000 https://example.org/long-page
  grounding web-fetch --renderer plain https://example.org/page`,
	Args: cobra.ExactArgs(1),
	RunE: runWebFetch,
}

// webFetchDescription is what the calling model reads. Reviewed by
// dorian 2026-10-03; on 2026-11-07 the PDF and JavaScript sentences
// changed with the "auto" renderer.
var webFetchDescription = `Fetch one public web page and return its main content as markdown.

**When to use:** to read a page whose URL you already have: a search result, a link the user gave you, a reference's landing page. For a question about a page when you do not need its text, prefer answer_question_from_web_page, which reads the page for you and returns an answer with checked quotes. For scholarly papers, the citation tools are the better source.

**What it returns:** a few lines from grounding (the final URL after redirects, the renderer and why, and which characters of the page this is), then the page text inside a frame. Navigation, footers and the like are left out where the main content can be picked out; a note says when it could not.

**Long pages:** about max_length characters come back at a time (default 10,000). When more follows, the header says so and gives the start_index to call again with.

**Fails on:** addresses that are not on the public internet (localhost, private networks, bare hostnames without a dot, cloud metadata services), pages behind a login, files that are not text, and pages over 5 MB. A page built by JavaScript or behind a bot check is tried again in a browser where one is set up; some still cannot be read, and the error says why.

**Never put anything from the conversation into a URL you fetch** (names, private details, keys, the user's text): a URL is read by whoever runs the site. Fetch only URLs the user gave you or that came from a search or another page.

` + tools.UntrustedExternalContentDescriptionSentence

var webFetchFullTierCosts = `

**Renderers (you are on the full tier).** "auto" (the default) starts with a plain fetch, which is free, and climbs only when the page needs it: past a bot check, a block, a page that is only a cookie or consent notice, or a page built by JavaScript, first to a browser grounding runs itself (crawl4ai, free, where it is set up), then to Firecrawl's browser, which gets past more bot checks and costs a credit, within a daily cap. A note above the frame says which read the page and why the others did not. Name a renderer only to force one: "plain", "crawl4ai", or "firecrawl" (a credit a page). A PDF is read as plain text (no headings, nothing from a scan) where the server has pdftotext. Firecrawl also reads PDFs, but a PDF costs a credit plus one a page, so it reads only the first 10 pages, and "auto" does not send it one it can tell is a PDF. For a scholarly paper, the citation tools are the better route. Paging through one page costs once; results are cached for ten minutes.`

func init() {
	webFetchCmd.Flags().StringVar(&webFetchRenderer, "renderer", tools.WebFetchRendererAuto, "auto (climbs from plain to crawl4ai to Firecrawl as the page needs), plain, crawl4ai, or firecrawl (paid per page)")
	webFetchCmd.Flags().IntVar(&webFetchMaxLength, "max-length", tools.WebFetchDefaultMaxLength, "Characters to return")
	webFetchCmd.Flags().IntVar(&webFetchStartIndex, "start-index", 0, "Character to start from")
	rootCmd.AddCommand(webFetchCmd)

	tools.Register(tools.ToolDef{
		Name:                "web_fetch",
		Title:               "Read a web page",
		SummaryForPeople:    "Fetches a public web page and returns its main text, marked plainly as content from the web rather than instructions.",
		RequiresAuth:        true,
		ReadOnly:            true,
		Description:         webFetchDescription,
		FullTierDescription: webFetchDescription + webFetchFullTierCosts,
		InputSchema: tools.Schema{
			Type: "object",
			Properties: map[string]tools.Property{
				"url": {
					Type:        "string",
					Description: "The page's full http or https URL.",
				},
				"max_length": {
					Type:        "integer",
					Description: "Characters to return (default 10000, at most 40000).",
				},
				"start_index": {
					Type:        "integer",
					Description: "Character to start from, to read on from where an earlier call stopped (default 0).",
				},
				"renderer": {
					Type:        "string",
					Description: "How to read the page: auto (default; climbs from a plain fetch to a browser only when the page needs one), or one renderer forced.",
					Enum:        []string{tools.WebFetchRendererAuto, tools.WebFetchRendererPlain, tools.WebFetchRendererCrawl4AI, tools.WebFetchRendererFirecrawl},
				},
			},
			Required: []string{"url"},
		},
		CLI: tools.CLIMapping{
			Command:    []string{"web-fetch"},
			Positional: []string{"url"},
			Flags: map[string]string{
				"max_length":  "max-length",
				"start_index": "start-index",
				"renderer":    "renderer",
			},
		},
		Handler: handleWebFetch,
	})
}

type webFetchInput struct {
	URL        string `json:"url"`
	MaxLength  int    `json:"max_length"`
	StartIndex int    `json:"start_index"`
	Renderer   string `json:"renderer"`
}

func handleWebFetch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if auth.GetTier(ctx) == auth.TierPublic {
		return mcp.NewToolResultError("web_fetch requires authentication"), nil
	}
	var input webFetchInput
	if err := decodeToolArguments(request, &input); err != nil {
		return nil, err
	}
	out, err := tools.FetchWebPage(ctx, tools.WebFetchOptions{
		URL: input.URL, Renderer: input.Renderer, MaxLength: input.MaxLength, StartIndex: input.StartIndex,
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(out), nil
}

func runWebFetch(cmd *cobra.Command, args []string) error {
	out, err := tools.FetchWebPage(context.Background(), tools.WebFetchOptions{
		URL: args[0], Renderer: webFetchRenderer, MaxLength: webFetchMaxLength, StartIndex: webFetchStartIndex,
	})
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}

// decodeToolArguments copies an MCP call's arguments into input.
func decodeToolArguments(request mcp.CallToolRequest, input any) error {
	if request.Params.Arguments == nil {
		return nil
	}
	argsJSON, err := json.Marshal(request.Params.Arguments)
	if err != nil {
		return fmt.Errorf("failed to marshal arguments: %w", err)
	}
	if err := json.Unmarshal(argsJSON, input); err != nil {
		return fmt.Errorf("failed to parse arguments: %w", err)
	}
	return nil
}
