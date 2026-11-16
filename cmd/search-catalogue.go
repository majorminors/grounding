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
	searchSource       string
	searchLimit        int
	searchBackends     []string
	searchYearMin      int
	searchYearMax      int
	searchMinCitations int
	searchDomain       string
	searchDebug        bool
)

// searchCatalogueDescription is what every caller sees. It names only the
// free backends; the paid and slow ones are described in
// searchCatalogueCosts, which full-tier sessions get appended.
const searchCatalogueDescription = `Search catalogues of citable sources (research papers, books and encyclopaedia articles) for real sources to reason from, before making claims.

Use this tool to ground your reasoning in actual sources BEFORE making assertions. Choose the source type for the kind of grounding the question needs:
- "academic": the scholarly literature and books: research papers with abstracts, DOIs and citation counts from OpenAlex, Crossref, Europe PMC and Semantic Scholar, and books from OpenLibrary
- "general": encyclopaedic background from Wikipedia: settled facts, definitions, people and events
- "auto": academic and general together (default)

For anything on the open web (news, organisations, products, policy, opinion), use a web search tool if you have one: this tool searches catalogues, not web pages.

**Returns:** "papers" and "books", ranked and limited apart (a book has no abstract or citation count to compete with papers on), and Wikipedia "articles" with extracts. Every record has a "kind": "paper", "book" or "book_chapter". A record's "availability", when present, says how it can be had: "open_access_url" is a legal open-access copy, with "open_access_url_kind" "pdf" or "landing_page" (the publisher's page, no direct file). "summary.top_cited" highlights the highest-impact papers.

**Warnings:** "warnings" has one line for each source that failed, was resting after a rate limit, or was named but could not run: the results may then be incomplete; say so if it matters. When nothing comes back, "empty_result_explanation" says why, when the reason is not simply that the catalogues had nothing (records filtered out, a source failed); do not read it as evidence the work does not exist. Per-backend counts and errors ("diagnostics") are left out unless you pass debug: true.

**Query tips — these matter for result quality:**
- Use 3–5 distinctive keywords, not full sentences. UK/US spelling is handled transparently.
- Including an author surname dramatically improves precision (see query parameter).
- To find a book, search its title and author's surname; it comes back under "books".
- Multiple short queries beat one long query.
- For canonical works, use min_citations: 50+. This is the most effective noise filter.

**Rate limits:** the server rate-limits, retries and caches per backend, so parallel calls are fine. Semantic Scholar draws on a pool shared with every other unauthenticated client and is often in cooldown; results do not depend on it — OpenAlex, Crossref and Europe PMC supply abstracts and citation counts. If a backend is in cooldown a warning says so; there is nothing to do about it.

**Query terms:** avoid common English words as sole query terms (second, shift, class, model, system). Use specific compound terms ("household-labor" not "domestic") and combine with min_citations when looking for established work.

**Domain field:** OpenAlex results include a "domain" field (e.g. "Social Sciences") for discipline filtering. Only affects OpenAlex — combine with backends: ["openalex"] for cleanest results.

**Finding canonical literature:** search_catalogue with min_citations: 50 → citation_graph on the best-cited result (direction "references") to find foundational works keyword search misses.

**Literature characterisation:** 2–3 search_catalogue calls with different phrasings → citation_graph on anchor papers (direction "citations") to find follow-up work. Synthesise across abstracts; don't rely on any single paper.`

// searchCatalogueCosts tells a full-tier caller what the opt-in backends
// cost, so a model reaches for them deliberately. Figures verified
// against the providers' pricing on 2026-09-17.
const searchCatalogueCosts = `**Costs and etiquette (you are on the full tier).** Backends you may name in "backends":
- openalex, crossref, europepmc, openlibrary, wikipedia — free. OpenAlex draws on a daily credit budget shared by every caller of this server (a search spends ~20–30 of ~10,000 credits a day).
- semantic_scholar — free; a global pool shared with every unauthenticated client, often in cooldown.
- consensus — opt-in only; about 1¢ per query. Consensus answers what the literature agrees on, with each study's takeaway: an alternative to working the literature yourself with these tools. Use it to check your own reading against, or when the user wants a quick answer rather than a full search.
Web pages are not in this catalogue: for them use web_search (its semantic mode is Exa).
A record named by "consensus" carries "summary_written_by_consensus", Consensus's own one-line takeaway; its "abstract" is the authors'. Quote the abstract as the paper's, the summary as Consensus's.
When you have find_download_link, a record's "availability" also carries "find_download_link_arguments": pass them to find_download_link as they are to get the file (a DOI for an open-access PDF; a book's title and author, found when you ask, with a caution to check the edition). Download only what the user asked for.
Prefer the free backends. Name a paid one only when they cannot answer the question, and never repeat a paid query you have already run (responses are cached for ten minutes regardless).`

var searchCmd = &cobra.Command{
	Use:     "search-catalogue <query>",
	Aliases: []string{"search"},
	Short:   "Search for papers and articles",
	Long: `Search for papers and articles across multiple backends.

Source types:
  academic  Research papers from OpenAlex, Crossref, Europe PMC and Semantic Scholar
  general   Wikipedia articles for general knowledge
  auto      Academic and general sources (default)

Costs: openalex, crossref, europepmc, openlibrary, wikipedia and
semantic_scholar are free (OpenAlex spends a daily credit budget shared
by every caller). Opt-in via --backends: consensus (~1c per query), and
any source this build adds (check-backends lists them). Web pages are
web-search's.

Examples:
  grounding search-catalogue "cognitive load theory"
  grounding search-catalogue "climate change effects" --source academic --limit 10
  grounding search-catalogue "machine learning" --json`,
	Args: cobra.ExactArgs(1),
	RunE: runSearch,
}

func init() {
	searchCmd.Flags().StringVar(&searchSource, "source", "auto", "Source type (academic|general|auto)")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 5, "Maximum results per source")
	searchCmd.Flags().StringSliceVar(&searchBackends, "backends", nil, "Specific backends: semantic_scholar,openalex,crossref,europepmc,openlibrary,wikipedia (free); consensus (~1c/query); any a build adds")
	searchCmd.Flags().IntVar(&searchYearMin, "year-min", 0, "Minimum publication year")
	searchCmd.Flags().IntVar(&searchYearMax, "year-max", 0, "Maximum publication year")
	searchCmd.Flags().IntVar(&searchMinCitations, "min-citations", 0, "Minimum citation count")
	searchCmd.Flags().StringVar(&searchDomain, "domain", "", "OpenAlex domain filter (e.g. \"Social Sciences\")")
	searchCmd.Flags().BoolVar(&searchDebug, "debug", false, "Also show per-backend diagnostics (always logged)")
	rootCmd.AddCommand(searchCmd)

	tools.Register(tools.ToolDef{
		Name:                "search_catalogue",
		Title:               "Find sources on a question",
		SummaryForPeople:    "Searches the academic literature, books and Wikipedia at once and hands back real, citable sources with abstracts, and a legal open-access copy where one exists, so the model reasons from evidence rather than memory.",
		ReadOnly:            true,
		Description:         searchCatalogueDescription,
		FullTierDescription: searchCatalogueDescription + "\n\n" + searchCatalogueCosts,
		InputSchema: tools.Schema{
			Type: "object",
			Properties: map[string]tools.Property{
				"query": {
					Type: "string",
					Description: "Keyword query. **Including an author surname is the most effective way to improve precision.** " +
						"\"Stoet Geary gender equality paradox\" returns the right paper; " +
						"\"gender equality paradox STEM\" returns stem cell papers. " +
						"\"Hochschild second shift\" finds the book; " +
						"\"second shift women employment\" returns genomics noise. " +
						"Good: \"Buss sexual strategies theory\", " +
						"\"household labor division gender\". " +
						"Bad: \"how is household labour divided between genders in modern dual-income families\" (too long), " +
						"\"second shift emotional labour gender Hochschild\" (too many terms). " +
						"For Wikipedia (source_type \"general\"), 2–4 precise terms work best.",
				},
				"source_type": {
					Type:    "string",
					Enum:    []string{"academic", "general", "auto"},
					Default: "auto",
					Description: "Type of sources to search. Default: \"auto\". " +
						"\"academic\" when you only need papers (faster, skips Wikipedia). " +
						"\"general\" for background context and definitions (Wikipedia only). " +
						"\"auto\" searches academic and general backends.",
				},
				"max_results": {
					Type:    "number",
					Default: 5,
					Description: "Maximum results to return. Default: 5. " +
						"Use 10–15 when exploring a topic or looking for anchor papers to feed into citation_graph. " +
						"Higher values increase noise but improve coverage for broad queries.",
				},
				"backends": {
					Type:  "array",
					Items: &tools.Property{Type: "string"},
					Description: "Specific backends to use (overrides source_type entirely). " +
						"Options: semantic_scholar, openalex, crossref, europepmc, openlibrary (books), wikipedia. " +
						"Useful for targeting a single backend, e.g. [\"europepmc\"] for biomedical literature. " +
						"Use check_backends to see every backend available to you, including opt-in ones.",
				},
				"year_min": {
					Type: "number",
					Description: "Minimum publication year (inclusive). " +
						"Useful for \"recent work only\" queries. " +
						"Caution: excludes foundational older works — omit when looking for canonical literature.",
				},
				"year_max": {
					Type: "number",
					Description: "Maximum publication year (inclusive). Rarely needed. " +
						"Use to cap results to a specific era (e.g. pre-replication-crisis work before 2011).",
				},
				"min_citations": {
					Type: "number",
					Description: "Minimum citation count. Filters out papers below this threshold. " +
						"Effective for finding established/canonical work: 50 for established, 100+ for foundational. " +
						"Caution: filters out recent work that hasn't accumulated citations yet. " +
						"Don't use when looking for the latest research.",
				},
				"debug": {
					Type:        "boolean",
					Description: "Also return per-backend diagnostics (raw and filtered counts, errors, skips). Default false: warnings and the empty-result explanation are returned either way.",
				},
				"domain": {
					Type: "string",
					Description: "OpenAlex top-level domain filter. Restricts OA results to a discipline. " +
						"Values: \"Social Sciences\", \"Health Sciences\", \"Life Sciences\", \"Physical Sciences\". " +
						"**Important:** domain ONLY filters OpenAlex results. Crossref and Semantic Scholar results " +
						"pass through unfiltered — using domain alone can make results worse by removing OA's good " +
						"matches while Crossref STEM noise remains. For cleanest results, combine domain with " +
						"backends: [\"openalex\"] — e.g. backends [\"openalex\"] + domain \"Social Sciences\" " +
						"eliminates cross-discipline noise entirely.",
				},
			},
			Required: []string{"query"},
		},
		CLI: tools.CLIMapping{
			Command:    []string{"search-catalogue"},
			Positional: []string{"query"},
			Flags: map[string]string{
				"source_type":   "source",
				"max_results":   "limit",
				"backends":      "backends",
				"year_min":      "year-min",
				"year_max":      "year-max",
				"min_citations": "min-citations",
				"domain":        "domain",
				"debug":         "debug",
			},
		},
		Handler: handleSearchCatalogue,
	})
}

// searchCatalogueInput represents the input parameters for search_catalogue.
type searchCatalogueInput struct {
	Query        string   `json:"query"`
	SourceType   string   `json:"source_type"`
	MaxResults   int      `json:"max_results"`
	Backends     []string `json:"backends"`
	YearMin      int      `json:"year_min"`
	YearMax      int      `json:"year_max"`
	MinCitations int      `json:"min_citations"`
	Domain       string   `json:"domain"`
	Debug        bool     `json:"debug"`
}

func handleSearchCatalogue(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse input
	var input searchCatalogueInput
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
	if input.Query == "" {
		return mcp.NewToolResultError("'query' is required"), nil
	}

	// Apply defaults
	if input.SourceType == "" {
		input.SourceType = "auto"
	}
	if input.MaxResults <= 0 {
		input.MaxResults = 5
	}

	// Fetch context
	filters := backends.SearchFilters{
		YearMin:      input.YearMin,
		YearMax:      input.YearMax,
		MinCitations: input.MinCitations,
		Domain:       input.Domain,
	}
	result, err := tools.SearchCatalogue(ctx, input.Query, input.SourceType, input.MaxResults, input.Backends, filters)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !input.Debug {
		result.Diagnostics = nil // logged whatever the caller asked (tools.logCatalogueSearch)
	}

	// Return JSON result
	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return mcp.NewToolResultText(string(resultJSON)), nil
}

// lineForASearchThatReturnedNothing claims "No results found." only when
// nothing was filtered out, failed or skipped.
func lineForASearchThatReturnedNothing(result *backends.ContextResult) string {
	if result.EmptyResultExplanation != "" {
		return "No results to show: " + result.EmptyResultExplanation
	}
	return "No results found."
}

func runSearch(cmd *cobra.Command, args []string) error {
	query := args[0]

	// Validate source
	validSources := map[string]bool{"academic": true, "general": true, "auto": true}
	if !validSources[searchSource] {
		return fmt.Errorf("invalid --source: %s (must be academic, general, or auto)", searchSource)
	}

	tools.CommandLineRunHasFindDownloadLink = true

	if err := tools.GetNamedSourcesReadyOnTheCommandLine(searchBackends); err != nil {
		return err
	}

	ctx := context.Background()
	filters := backends.SearchFilters{
		YearMin:      searchYearMin,
		YearMax:      searchYearMax,
		MinCitations: searchMinCitations,
		Domain:       searchDomain,
	}
	result, err := tools.SearchCatalogue(ctx, query, searchSource, searchLimit, searchBackends, filters)
	if err != nil {
		return err
	}

	if !searchDebug {
		result.Diagnostics = nil // logged either way (tools.logCatalogueSearch)
	}
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Human-readable output
	fmt.Printf("Query: %s\n\n", result.Query)

	printCatalogueRecords("Papers", result.Papers)
	printCatalogueRecords("Books", result.Books)

	if len(result.Articles) > 0 {
		fmt.Printf("=== Wikipedia Articles (%d) ===\n\n", len(result.Articles))
		for i, a := range result.Articles {
			fmt.Printf("%d. %s\n", i+1, a.Title)
			fmt.Printf("   URL: %s\n", a.URL)
			if a.Extract != "" {
				extract := a.Extract
				if len(extract) > 300 {
					extract = extract[:297] + "..."
				}
				fmt.Printf("   Extract: %s\n", extract)
			}
			fmt.Println()
		}
	}

	if len(result.Papers) == 0 && len(result.Books) == 0 && len(result.Articles) == 0 {
		fmt.Println(lineForASearchThatReturnedNothing(result))
	}

	for _, warning := range result.Warnings {
		fmt.Printf("Warning: %s\n", warning)
	}
	if len(result.Warnings) > 0 {
		fmt.Println()
	}

	// Diagnostics only with --debug (stripped above otherwise).
	if len(result.Diagnostics) > 0 {
		fmt.Println("=== Diagnostics ===")
		fmt.Println()
		for _, d := range result.Diagnostics {
			line := fmt.Sprintf("  %s: %d raw", d.Backend, d.RawCount)
			if d.FilteredCount > 0 || d.RawCount > 0 {
				line += fmt.Sprintf(" → %d after filter", d.FilteredCount)
			}
			if d.Error != "" {
				line += fmt.Sprintf(" [error: %s]", d.Error)
			}
			fmt.Println(line)
		}
		fmt.Println()
	}

	return nil
}

// printCatalogueRecords prints one section of the human-readable search
// output: papers (with book chapters marked) or books.
func printCatalogueRecords(heading string, records []backends.Paper) {
	if len(records) == 0 {
		return
	}
	fmt.Printf("=== %s (%d) ===\n\n", heading, len(records))
	for i, p := range records {
		fmt.Printf("%d. %s", i+1, p.Title)
		if p.Year > 0 {
			fmt.Printf(" (%d)", p.Year)
		}
		if p.Kind == backends.RecordKindBookChapter {
			fmt.Print(", book chapter")
		}
		fmt.Println()

		if len(p.Authors) > 0 {
			authors := strings.Join(p.Authors, ", ")
			if len(authors) > 80 {
				authors = authors[:77] + "..."
			}
			fmt.Printf("   Authors: %s\n", authors)
		}
		if p.Journal != "" {
			fmt.Printf("   Journal: %s\n", p.Journal)
		}
		if p.Publisher != "" {
			fmt.Printf("   Publisher: %s\n", p.Publisher)
		}
		if p.DOI != "" {
			fmt.Printf("   DOI: %s\n", p.DOI)
		}
		if p.ISBN != "" {
			fmt.Printf("   ISBN: %s\n", p.ISBN)
		}
		if p.MD5 != "" {
			fmt.Printf("   MD5: %s\n", p.MD5)
		}
		if p.FileType != "" {
			fmt.Printf("   Format: %s\n", p.FileType)
		}
		if p.ContentType != "" {
			fmt.Printf("   Type: %s\n", p.ContentType)
		}
		if p.SummaryWrittenByConsensus != "" {
			fmt.Printf("   Consensus's summary (not the authors'): %s\n", p.SummaryWrittenByConsensus)
		}
		if p.Abstract != "" {
			abstract := p.Abstract
			if len(abstract) > 200 {
				abstract = abstract[:197] + "..."
			}
			fmt.Printf("   Abstract: %s\n", abstract)
		}
		if a := p.Availability; a != nil {
			if a.OpenAccessURL != "" {
				kind := "PDF"
				if a.OpenAccessURLKind == "landing_page" {
					kind = "the publisher's page, no direct file"
				}
				line := fmt.Sprintf("   Open access: %s (%s", a.OpenAccessURL, kind)
				if a.OpenAccessLicense != "" {
					line += ", " + a.OpenAccessLicense
				}
				fmt.Println(line + ")")
			}
			switch {
			case a.FindDownloadLinkArguments["doi"] != "":
				fmt.Printf("   Download: grounding find-download-link --doi %s\n", a.FindDownloadLinkArguments["doi"])
			case a.FindDownloadLinkArguments["md5"] != "":
				fmt.Printf("   Download: grounding find-download-link --md5 %s\n", a.FindDownloadLinkArguments["md5"])
			case a.FindDownloadLinkArguments["title"] != "":
				command := "grounding find-download-link --title " + quotedForTheShell(a.FindDownloadLinkArguments["title"])
				if author := a.FindDownloadLinkArguments["author"]; author != "" {
					command += " --author " + quotedForTheShell(author)
				}
				fmt.Printf("   Download: %s\n", command)
			}
		}
		if p.URL != "" && p.DOI == "" {
			fmt.Printf("   URL: %s\n", p.URL)
		}
		fmt.Printf("   Source: %s\n\n", p.Source)
	}
}

// quotedForTheShell single-quotes s so a printed command can be pasted
// as it is (a title with an apostrophe or a dollar sign included).
func quotedForTheShell(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
