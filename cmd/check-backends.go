package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"

	"btr.mt/grounding/internal/tools"
)

var (
	healthForce bool
)

// checkBackendsDescription is what every caller sees; checkBackendsCosts
// is appended for full-tier sessions, which are the only ones whose
// health checks can cost anything.
const checkBackendsDescription = `Check health status of all grounding backends at once.

Returns status for each backend:
- **crossref** — DOI verification (free, no auth)
- **openalex** — Broad paper discovery with abstracts (free)
- **europepmc** — Biomedical and life-science papers with abstracts and open-access full text (free)
- **semantic-scholar** — Paper search with influence markers (free, shared pool; often in cooldown)
- **opencitations** — Citation graph fallback (free)
- **wikipedia** — General knowledge (free)
- **openlibrary** — Book verification (free, no auth)
Further backends appear here when they are available to you.

**When to use:**
- Before a grounding-heavy session to see what's available
- When tools return errors, to diagnose which backend is down`

const checkBackendsCosts = `**Costs (you are on the full tier).** Consensus (~1¢ per probe) and Exa (~$0.007 per probe) are only probed when force is true; without it they report as configured-but-unprobed, which is all you usually need. The Unpaywall probe is free.`

var healthCmd = &cobra.Command{
	Use:     "check-backends",
	Aliases: []string{"health"},
	Short:   "Check health of all grounding backends",
	Long: `Check the health status of all grounding backends.

Tests connectivity to: Crossref, Semantic Scholar, OpenAlex, Europe PMC, OpenCitations,
Wikipedia, OpenLibrary, and (with keys) Unpaywall, Consensus, Exa.

By default the probes that cost something are skipped: Semantic Scholar
without an API key (its shared rate budget), Consensus (~1c) and Exa
(~$0.007). Use --force to run them.

Examples:
  grounding check-backends
  grounding check-backends --force
  grounding check-backends --json`,
	RunE: runHealth,
}

func init() {
	rootCmd.AddCommand(healthCmd)
	healthCmd.Flags().BoolVar(&healthForce, "force", false, "Also run the probes that cost something: Semantic Scholar without a key, Consensus (~1c), Exa (~$0.007)")

	tools.Register(tools.ToolDef{
		Name:                "check_backends",
		Title:               "Check the sources are up",
		SummaryForPeople:    "Reports which of the databases behind this server are answering right now. Useful when results look thin.",
		ReadOnly:            true,
		Description:         checkBackendsDescription,
		FullTierDescription: checkBackendsDescription + "\n\n" + checkBackendsCosts,
		InputSchema: tools.Schema{
			Type: "object",
			Properties: map[string]tools.Property{
				"force": {
					Type:        "boolean",
					Description: "Also run the probes skipped by default because they spend something (a shared rate budget, or a metered call). Default false: those backends report as configured-but-unprobed. Parameter text is the same for every caller; the tool description says which backends and what each costs.",
				},
			},
		},
		CLI: tools.CLIMapping{
			Command: []string{"check-backends"},
		},
		Handler: handleCheckBackends,
	})
}

// checkBackendsInput represents the input parameters for check_backends.
type checkBackendsInput struct {
	Force bool `json:"force"`
}

func handleCheckBackends(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse input
	var input checkBackendsInput
	if request.Params.Arguments != nil {
		argsJSON, err := json.Marshal(request.Params.Arguments)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal arguments: %w", err)
		}
		if err := json.Unmarshal(argsJSON, &input); err != nil {
			return nil, fmt.Errorf("failed to parse arguments: %w", err)
		}
	}

	result := tools.CheckBackends(ctx, &tools.CheckBackendsOptions{
		Force: input.Force,
	})

	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %s", err)), nil
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

func runHealth(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	result := tools.CheckBackends(ctx, &tools.CheckBackendsOptions{
		Force: healthForce,
	})

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Human-readable output
	fmt.Println("Backend Health Status")
	fmt.Println("=====================")
	fmt.Println()

	// Sort backend names for consistent output
	names := make([]string, 0, len(result.Backends))
	for name := range result.Backends {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		status := result.Backends[name]
		icon := "✓"
		if !status.Healthy {
			icon = "✗"
		}
		fmt.Printf("  %s %s: %s\n", icon, name, status.Message)
	}

	fmt.Println()
	if result.Healthy {
		fmt.Println("All backends healthy.")
	} else {
		fmt.Println("Some backends are unhealthy.")
	}

	// Exit with non-zero status if any backend is unhealthy
	if !result.Healthy {
		os.Exit(1)
	}

	return nil
}
