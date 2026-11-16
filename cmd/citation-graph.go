package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"

	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/tools"
)

var (
	graphDirection    string
	graphLimit        int
	graphSortBy       string
	graphMinCitations int
)

var graphCmd = &cobra.Command{
	Use:     "citation-graph <paper-id>",
	Aliases: []string{"graph"},
	Short:   "Explore a paper's citation graph",
	Long: `Fetch references and/or citations for a seed paper.

Uses Semantic Scholar for rich metadata (influence markers, intent labels).
Falls back to OpenAlex when SS is rate-limited, and to OpenCitations (with
metadata from OpenAlex or Crossref) when OpenAlex fails too.

The paper identifier can be a DOI, arXiv ID, Semantic Scholar paper ID,
or a title (which triggers a search fallback).

Direction:
  references  Papers cited by the seed paper
  citations   Papers that cite the seed paper
  both        Both directions (default)

Examples:
  grounding citation-graph "10.1038/nature12373"
  grounding citation-graph "attention is all you need" --direction citations
  grounding citation-graph "arxiv:1706.03762" --direction both --limit 10`,
	Args: cobra.ExactArgs(1),
	RunE: runGraph,
}

func init() {
	graphCmd.Flags().StringVar(&graphDirection, "direction", "both", "Direction (references|citations|both)")
	graphCmd.Flags().IntVar(&graphLimit, "limit", 20, "Maximum results per direction")
	graphCmd.Flags().StringVar(&graphSortBy, "sort-by", "recency", "Sort order (recency|citations)")
	graphCmd.Flags().IntVar(&graphMinCitations, "min-citations", 0, "Minimum citation count to include an edge")
	rootCmd.AddCommand(graphCmd)

	maxLimit := float64(1000)
	tools.Register(tools.ToolDef{
		Name:             "citation_graph",
		Title:            "Explore a paper’s citations",
		SummaryForPeople: "Start from one paper and see what it builds on and what has built on it since, ranked by influence. The quickest way to find the foundational work that keyword searches miss.",
		ReadOnly:         true,
		Description: `Start from a known paper and explore the research landscape around it.

Use this tool to discover what a paper cites, what cites it, and how
influential those connections are. This is the best way to find
canonical/foundational works that keyword search misses.

**When to use:**
- To find seminal works: pick any paper in the area, traverse its references (direction "references")
- To find recent follow-up work: traverse citations (direction "citations")
- When search_catalogue returns recent papers but you need the foundational work they build on
- To explore the research context around a specific paper

**Input:** A paper identifier (DOI, arXiv ID, Semantic Scholar ID, or title)
**Returns:** Seed paper metadata plus references and/or citations with
influence markers (is_influential) and intent labels (methodology, background,
result comparison). Includes a temporal trend (year histogram). The trend is
a sample when results hit the limit cap; for highly-cited papers (1000+),
treat the distribution as indicative, not exact. Pay attention to
is_influential edges — these mark the citations that shaped the seed paper's
core argument.

**Sorting:** By default, results are ordered by recency (most recent first).
For highly-cited seed papers, most results will be very recent and may not be
the most important citing works. Use sort_by "citations" to rank by citation
count instead.

**For literature characterisation,** use direction "citations" with
sort_by "citations" and a moderate limit (50--100). This surfaces the canonical
follow-up works, not just the latest.

**Resilience:** Uses Semantic Scholar for rich edge metadata (is_influential,
intents). Falls back to OpenAlex when SS is rate-limited (HTTP 429), and to
OpenCitations when OpenAlex fails too. When sort_by is "citations", OpenAlex
is used directly (it supports server-side citation sorting; SS does not).
Fallback results omit is_influential and intents but still provide paper
metadata, citation counts, and year data.

**Finding canonical works (recipe):**
1. Use search_catalogue to find any well-cited paper in the area (min_citations: 50).
2. Use citation_graph on that paper with direction "citations", sort_by "citations",
   min_citations 200, limit 50. This returns the most-cited papers that cite the seed.
3. Works best with seed papers that have 500+ citations. For niche areas, lower
   min_citations accordingly.

This two-step workflow is more reliable than keyword search, which misses papers
whose titles don't share your query terms (e.g. "Spandrels of San Marco" won't
appear for "evolutionary psychology critique").

**Verify→graph workflow (recommended for reliable seed resolution):**
Title-based seed resolution is unreliable when Semantic Scholar is rate-limited —
it may resolve to the wrong paper via OpenAlex fuzzy matching. For reliable
graph traversal, first verify the paper with check_citation to obtain its DOI,
then pass the DOI to citation_graph. DOIs always resolve correctly.
Example: check_citation → DOI "10.1177/0891243287001002002" → citation_graph with that DOI.`,
		InputSchema: tools.Schema{
			Type: "object",
			Properties: map[string]tools.Property{
				"paper_id": {
					Type:        "string",
					Description: "Paper identifier: DOI, arXiv ID, Semantic Scholar ID, or title for search fallback",
				},
				"direction": {
					Type:    "string",
					Enum:    []string{"references", "citations", "both"},
					Default: "both",
					Description: "Which direction to traverse. " +
						"\"references\": papers cited by seed — use to find intellectual foundations. " +
						"\"citations\": papers citing seed — use to find follow-up work. " +
						"\"both\" (default) doubles API calls — use a single direction when you know what you need.",
				},
				"limit": {
					Type:    "number",
					Default: 20,
					Description: "Maximum results per direction (default 20, max 1000). " +
						"20 is good for exploration. 50–100 for literature characterisation. " +
						"Higher values are slower and results become less relevant.",
					Maximum: &maxLimit,
				},
				"sort_by": {
					Type:    "string",
					Enum:    []string{"recency", "citations"},
					Default: "recency",
					Description: "Sort order for results. Default \"recency\" returns mostly very recent low-impact papers for highly-cited seeds. " +
						"Switch to \"citations\" for canonical follow-ups and the most influential citing works.",
				},
				"min_citations": {
					Type:        "integer",
					Default:     0,
					Description: "Minimum citation count to include an edge (0 = no filter). Post-retrieval filter; increase limit to compensate for filtered results",
				},
			},
			Required: []string{"paper_id"},
		},
		CLI: tools.CLIMapping{
			Command:    []string{"citation-graph"},
			Positional: []string{"paper_id"},
			Flags: map[string]string{
				"direction":     "direction",
				"limit":         "limit",
				"sort_by":       "sort-by",
				"min_citations": "min-citations",
			},
		},
		Handler: handleCitationGraph,
	})
}

type citationGraphInput struct {
	PaperID      string `json:"paper_id"`
	Direction    string `json:"direction"`
	Limit        int    `json:"limit"`
	SortBy       string `json:"sort_by"`
	MinCitations int    `json:"min_citations"`
}

func handleCitationGraph(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var input citationGraphInput
	if request.Params.Arguments != nil {
		argsJSON, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal arguments: %w", err)
		}
		if err := json.Unmarshal(argsJSON, &input); err != nil {
			return nil, fmt.Errorf("failed to parse arguments: %w", err)
		}
	}

	if input.PaperID == "" {
		return mcp.NewToolResultError("'paper_id' is required"), nil
	}

	if input.Direction == "" {
		input.Direction = "both"
	}
	validDirections := map[string]bool{"references": true, "citations": true, "both": true}
	if !validDirections[input.Direction] {
		return mcp.NewToolResultError(fmt.Sprintf("invalid direction %q (must be references, citations, or both)", input.Direction)), nil
	}

	if input.Limit <= 0 {
		input.Limit = 20
	}

	if input.SortBy == "" {
		input.SortBy = "recency"
	}
	validSortBy := map[string]bool{"recency": true, "citations": true}
	if !validSortBy[input.SortBy] {
		return mcp.NewToolResultError(fmt.Sprintf("invalid sort_by %q (must be recency or citations)", input.SortBy)), nil
	}

	result, err := tools.FetchCitationGraph(ctx, input.PaperID, input.Direction, input.Limit, input.SortBy, input.MinCitations)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func runGraph(cmd *cobra.Command, args []string) error {
	paperID := args[0]

	validDirections := map[string]bool{"references": true, "citations": true, "both": true}
	if !validDirections[graphDirection] {
		return fmt.Errorf("invalid --direction: %s (must be references, citations, or both)", graphDirection)
	}
	validSortBy := map[string]bool{"recency": true, "citations": true}
	if !validSortBy[graphSortBy] {
		return fmt.Errorf("invalid --sort-by: %s (must be recency or citations)", graphSortBy)
	}

	ctx := context.Background()
	result, err := tools.FetchCitationGraph(ctx, paperID, graphDirection, graphLimit, graphSortBy, graphMinCitations)
	if err != nil {
		return err
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Human-readable output
	seed := result.Seed
	fmt.Printf("Seed: %s", seed.Title)
	if seed.Year > 0 {
		fmt.Printf(" (%d)", seed.Year)
	}
	fmt.Println()
	if len(seed.Authors) > 0 {
		authors := strings.Join(seed.Authors, ", ")
		if len(authors) > 80 {
			authors = authors[:77] + "..."
		}
		fmt.Printf("      %s\n", authors)
	}
	if seed.DOI != "" {
		fmt.Printf("      doi:%s\n", seed.DOI)
	}
	fmt.Println()

	if len(result.References) > 0 {
		fmt.Printf("=== References (%d) ===\n\n", result.TotalReferences)
		for i := range result.References {
			printEdge(i+1, &result.References[i])
		}
	}

	if len(result.Citations) > 0 {
		fmt.Printf("=== Citations (%d) ===\n\n", result.TotalCitations)
		for i := range result.Citations {
			printEdge(i+1, &result.Citations[i])
		}
	}

	if len(result.References) == 0 && len(result.Citations) == 0 {
		fmt.Println("No citation data found.")
	}

	if tr := result.Trend; tr != nil && len(tr.Counts) > 0 {
		fmt.Printf("=== Trend (%d–%d) ===\n\n", tr.YearRange[0], tr.YearRange[1])
		for y := tr.YearRange[0]; y <= tr.YearRange[1]; y++ {
			if c, ok := tr.Counts[y]; ok {
				fmt.Printf("  %d  %s (%d)\n", y, strings.Repeat("#", c), c)
			}
		}
		if tr.MissingYear > 0 {
			fmt.Printf("\n  %d edges missing year data\n", tr.MissingYear)
		}
		if tr.Sampled {
			fmt.Printf("\n  (sampled — results hit the limit cap)\n")
		}
		fmt.Println()
	}

	return nil
}

func printEdge(n int, e *backends.CitationEdge) {
	marker := " "
	if e.IsInfluential {
		marker = "★"
	}
	fmt.Printf("%s %d. %s", marker, n, e.Paper.Title)
	if e.Paper.Year > 0 {
		fmt.Printf(" (%d)", e.Paper.Year)
	}
	fmt.Println()

	if len(e.Paper.Authors) > 0 {
		authors := strings.Join(e.Paper.Authors, ", ")
		if len(authors) > 80 {
			authors = authors[:77] + "..."
		}
		fmt.Printf("     %s\n", authors)
	}
	if e.Paper.DOI != "" {
		fmt.Printf("     doi:%s\n", e.Paper.DOI)
	}
	if len(e.Intents) > 0 {
		fmt.Printf("     intents: %s\n", strings.Join(e.Intents, ", "))
	}
	fmt.Println()
}
