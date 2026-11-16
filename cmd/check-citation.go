package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"

	"btr.mt/grounding/internal/tools"
)

var (
	verifyDOI     string
	verifyTitle   string
	verifyAuthors []string
	verifyYear    int
)

var verifyCmd = &cobra.Command{
	Use:     "check-citation",
	Aliases: []string{"verify"},
	Short:   "Verify a citation exists",
	Long: `Verify that a citation/reference exists in academic databases.

Provide either a DOI (most reliable) or a title with optional authors and year
for fuzzy matching. A DOI cascades through Crossref, Semantic Scholar,
OpenAlex, Europe PMC and OpenLibrary; a title queries them all in parallel
and returns the best-scoring match.`,
	RunE: runVerify,
}

func init() {
	verifyCmd.Flags().StringVar(&verifyDOI, "doi", "", "DOI to verify (most reliable)")
	verifyCmd.Flags().StringVar(&verifyTitle, "title", "", "Paper title for fuzzy matching")
	verifyCmd.Flags().StringSliceVar(&verifyAuthors, "authors", nil, "Author names (comma-separated or repeated)")
	verifyCmd.Flags().IntVar(&verifyYear, "year", 0, "Publication year")

	rootCmd.AddCommand(verifyCmd)

	tools.Register(tools.ToolDef{
		Name:             "check_citation",
		Title:            "Check a citation is real",
		SummaryForPeople: "Confirms that a paper or book actually exists, by title, author, year or DOI, and returns its real details. Catches invented references before they reach you.",
		ReadOnly:         true,
		Description: `Verify that a citation/reference exists before presenting it to the user.

Use this tool BEFORE citing a paper to confirm it's real. Helps prevent hallucinated citations.

**When to use:**
- Before including a citation in your response
- To verify a paper the user mentioned actually exists
- To get the correct DOI/URL for a paper

**Input priority:**
- If you have a DOI, provide it (most reliable)
- Otherwise, provide title + authors + year for fuzzy matching

**Backends consulted:** Crossref (authoritative DOI registry), Semantic Scholar, OpenAlex, Europe PMC, OpenLibrary (books)

**If the result says "not found":** This means the citation could not be verified. Do NOT assume the work exists anyway. Common causes:
- You have the wrong title (e.g. confusing a concept name with a book title)
- You have the wrong author or year
- The work genuinely does not exist (hallucination)
- The work exists but is not indexed (rare for published books/papers)
If you have a web search tool, use it to confirm the work exists before citing it; otherwise tell the user it could not be verified. Pay attention to the suggestion field in the response.

**Books:** Academic databases index journal articles, not books. When you verify a book, the tool may match a book *review* or catalogue entry instead of the book itself. OpenLibrary provides accurate book metadata but has no citation counts. If the result has unexpectedly low citations or wrong authors for a well-known book, check that the matched work is the book itself, not a review of it.

**Response fields:** Check "verified" (bool), "confidence" (0–1 match quality), "source" (which backend matched), and "suggestion" (hints when not found). A low confidence score means the match is uncertain — verify the title and authors before citing.

**Tip:** The DOI returned here can be passed directly to citation_graph for reliable graph traversal — more dependable than passing a title.`,
		InputSchema: tools.Schema{
			Type: "object",
			Properties: map[string]tools.Property{
				"doi": {
					Type:        "string",
					Description: "DOI of the paper (e.g., \"10.1037/apl0000353\"). Preferred if available.",
				},
				"title": {
					Type:        "string",
					Description: "Title of the paper. Required if no DOI provided.",
				},
				"authors": {
					Type:        "array",
					Description: "Author names (e.g., [\"Smith, J.\", \"Jones, A.\"]). Improves matching accuracy.",
					Items:       &tools.Property{Type: "string"},
				},
				"year": {
					Type:        "number",
					Description: "Publication year. Improves matching accuracy.",
				},
			},
		},
		CLI: tools.CLIMapping{
			Command: []string{"check-citation"},
			Flags: map[string]string{
				"doi":     "doi",
				"title":   "title",
				"authors": "authors",
				"year":    "year",
			},
		},
		Handler: handleCheckCitation,
	})
}

// checkCitationInput represents the input parameters for check_citation.
type checkCitationInput struct {
	DOI     string   `json:"doi"`
	Title   string   `json:"title"`
	Authors []string `json:"authors"`
	Year    int      `json:"year"`
}

func handleCheckCitation(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse input
	var input checkCitationInput
	if request.Params.Arguments != nil {
		argsJSON, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal arguments: %w", err)
		}
		if err := json.Unmarshal(argsJSON, &input); err != nil {
			return nil, fmt.Errorf("failed to parse arguments: %w", err)
		}
	}

	// Validate input
	if input.DOI == "" && input.Title == "" {
		return mcp.NewToolResultError("at least 'doi' or 'title' is required"), nil
	}

	// Verify citation
	result, err := tools.VerifyCitation(ctx, input.DOI, input.Title, input.Authors, input.Year)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// Return JSON result
	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func runVerify(cmd *cobra.Command, args []string) error {
	if verifyDOI == "" && verifyTitle == "" {
		return fmt.Errorf("at least --doi or --title is required")
	}

	ctx := context.Background()
	result, err := tools.VerifyCitation(ctx, verifyDOI, verifyTitle, verifyAuthors, verifyYear)
	if err != nil {
		return err
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Human-readable output
	if result.Verified {
		fmt.Printf("✓ Verified via %s: \"%s\"", result.Source, result.Paper.Title)
		if result.Paper.Year > 0 {
			fmt.Printf(" (%d)", result.Paper.Year)
		}
		if result.Paper.DOI != "" {
			fmt.Printf(" - doi:%s", result.Paper.DOI)
		}
		fmt.Println()
		return nil
	}

	fmt.Fprintln(os.Stderr, "✗ Not verified: citation not found in any database")
	os.Exit(1)
	return nil
}
