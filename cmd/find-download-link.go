package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/spf13/cobra"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/tools"
)

var (
	findLinkDOI    string
	findLinkMD5    string
	findLinkTitle  string
	findLinkAuthor string
	findLinkISBN   string
	findLinkSaveTo string
)

var findDownloadLinkCmd = &cobra.Command{
	Use:     "find-download-link",
	Aliases: []string{"download-document", "download"},
	Short:   "Find a link to a copy of a document (a legal open-access copy first)",
	Long: `Find a link to a copy of a document by DOI, by MD5 hash, or a book by
its title and author; with --save-to, save the file too.

A DOI is first looked up on Unpaywall for a legal open-access copy
(needs GROUNDING_EMAIL). Only when there is none, or for an MD5 or a
book, does it go to a download source this build adds, if any.

Without --save-to (or GROUNDING_DOWNLOAD_DIRECTORY) it prints the link
and writes nothing.

Examples:
  grounding find-download-link --doi 10.1086/226906
  grounding find-download-link --doi 10.1086/226906 --save-to ~/Downloads
  grounding find-download-link --title "The New Monogamy" --author "Tammy Nelson"`,
	RunE: runFindDownloadLink,
}

// findDownloadLinkDescription is core's; an add-on's download source
// appends its own paragraph (tools.AppendToFullTierDescription).
const findDownloadLinkDescription = `Find a link to a copy of a document (paper or book). It returns a link for you (or the user) to fetch; it never downloads the file itself.

Provide one of: a DOI, an MD5 hash, or a book's title (with its author;
or an ISBN alone).

**DOI:** a legal open-access copy is looked up first (Unpaywall); the
response then has "source": "unpaywall" and "open_access": true, and the
DOI→document mapping is exact. "download_url_kind" is "pdf", or
"landing_page" when Unpaywall has only the publisher's page.

Call it only for a document the user has asked for. When the response
carries a "caution", check the returned "title"/"authors" against the
document you intended before trusting, citing, or attaching the file.

Returns: download_url, title/authors, source, and any caution or quota
the source reports.`

func init() {
	// The command is always there: a DOI needs no key when Unpaywall
	// has a copy, and a download source fetches its key only when it
	// is needed.
	findDownloadLinkCmd.Flags().StringVar(&findLinkDOI, "doi", "", "DOI of the document")
	findDownloadLinkCmd.Flags().StringVar(&findLinkMD5, "md5", "", "MD5 hash of a file a download source holds")
	findDownloadLinkCmd.Flags().StringVar(&findLinkTitle, "title", "", "title of a book to find")
	findDownloadLinkCmd.Flags().StringVar(&findLinkAuthor, "author", "", "author of the book given by --title")
	findDownloadLinkCmd.Flags().StringVar(&findLinkISBN, "isbn", "", "ISBN of a book to find, when there is no title")
	findDownloadLinkCmd.Flags().StringVar(&findLinkSaveTo, "save-to", "", "save the file in this directory too (default GROUNDING_DOWNLOAD_DIRECTORY; unset prints the link only)")
	rootCmd.AddCommand(findDownloadLinkCmd)

	tools.Register(tools.ToolDef{
		Name:             "find_download_link",
		Title:            "Find a link to a document",
		SummaryForPeople: "Finds a link to a copy of a paper or book, trying legal open-access copies first.",
		RequiresAuth:     true,
		// It writes nothing: the server only ever returns a link. A
		// source may still count the call against a daily quota, which
		// its own full-tier text says.
		ReadOnly:    true,
		Description: findDownloadLinkDescription,
		InputSchema: tools.Schema{
			Type: "object",
			Properties: map[string]tools.Property{
				"doi": {
					Type:        "string",
					Description: "DOI of the document. A legal open-access copy is tried first.",
				},
				"md5": {
					Type:        "string",
					Description: "MD5 hash of a file, from a previous search's results or availability.",
				},
				"title": {
					Type:        "string",
					Description: "Title of a book to find, as search_catalogue gave it.",
				},
				"author": {
					Type:        "string",
					Description: "Author of the book given by title (one name is enough).",
				},
				"isbn": {
					Type:        "string",
					Description: "ISBN of a book to find, when there is no title. Less reliable than title and author.",
				},
			},
		},
		CLI: tools.CLIMapping{
			Command: []string{"find-download-link"},
			Flags: map[string]string{
				"doi":    "doi",
				"md5":    "md5",
				"title":  "title",
				"author": "author",
				"isbn":   "isbn",
			},
		},
		Handler: handleFindDownloadLink,
	})
}

// md5Pattern is what a caller-supplied MD5 must look like before it is
// allowed anywhere near a URL or a filename.
var md5Pattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// documentNamedBy reads the document a call names from its arguments.
func documentNamedBy(arguments map[string]any) tools.DocumentToDownload {
	text := func(name string) string {
		value, _ := arguments[name].(string)
		return value
	}
	return tools.DocumentToDownload{DOI: text("doi"), MD5: text("md5"), Title: text("title"), Author: text("author"), ISBN: text("isbn")}
}

// handleFindDownloadLink returns a link and never saves a file: the
// server has no directory a caller could read.
func handleFindDownloadLink(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if auth.GetTier(ctx) == auth.TierPublic {
		return mcp.NewToolResultError("find_download_link requires authentication"), nil
	}
	arguments, _ := request.Params.Arguments.(map[string]any)
	result, err := findDownloadLink(ctx, documentNamedBy(arguments), arguments, "")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	return mcp.NewToolResultText(string(resultJSON)), nil
}

// findDownloadLink checks the MD5's shape before anything is asked of a
// source, then finds the link (tools.FindDownloadLink).
func findDownloadLink(ctx context.Context, document tools.DocumentToDownload, parameters map[string]any, saveToDirectory string) (*tools.FoundDocumentCopy, error) {
	if err := document.Validate(); err != nil {
		return nil, err
	}
	md5 := strings.ToLower(strings.TrimSpace(document.MD5))
	if md5 != "" && !md5Pattern.MatchString(md5) {
		return nil, fmt.Errorf("invalid MD5 %q: expected 32 hex characters", md5)
	}
	return tools.FindDownloadLink(ctx, document, parameters, saveToDirectory)
}

// parametersAddOnsAddedTo returns, by parameter name, the value of
// each flag an add-on put on the command for a tool parameter, when
// it was given: core's own flags are read through their variables.
func parametersAddOnsAddedTo(command *cobra.Command, toolName string, coreParameters ...string) map[string]any {
	parameters := map[string]any{}
	tool, ok := tools.Lookup(toolName)
	if !ok {
		return parameters
	}
	for parameter, flagName := range tool.CLI.Flags {
		if containsString(coreParameters, parameter) {
			continue
		}
		flag := command.Flags().Lookup(flagName)
		if flag == nil || !flag.Changed {
			continue
		}
		switch tool.InputSchema.Properties[parameter].Type {
		case "integer", "number":
			var number float64
			if _, err := fmt.Sscan(flag.Value.String(), &number); err == nil {
				parameters[parameter] = number
			}
		case "boolean":
			parameters[parameter] = flag.Value.String() == "true"
		default:
			parameters[parameter] = flag.Value.String()
		}
	}
	return parameters
}

func containsString(list []string, wanted string) bool {
	for _, item := range list {
		if item == wanted {
			return true
		}
	}
	return false
}

func runFindDownloadLink(cmd *cobra.Command, args []string) error {
	document := tools.DocumentToDownload{DOI: findLinkDOI, MD5: findLinkMD5, Title: findLinkTitle, Author: findLinkAuthor, ISBN: findLinkISBN}
	saveTo := findLinkSaveTo
	if saveTo == "" {
		saveTo = tools.DefaultSaveToDirectory()
	}
	ctx := tools.WithCommandLineRun(context.Background())
	result, err := findDownloadLink(ctx, document, parametersAddOnsAddedTo(cmd, "find_download_link", "doi", "md5", "title", "author", "isbn"), saveTo)
	if err != nil {
		return err
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	if result.FilePath != "" {
		fmt.Printf("Saved: %s\n", result.FilePath)
	}
	fmt.Printf("Link: %s\n", result.DownloadURL)
	fmt.Printf("Source: %s\n", result.Source)
	if result.Title != "" {
		fmt.Printf("Title: %s\n", result.Title)
	}
	if len(result.Authors) > 0 {
		fmt.Printf("Authors: %s\n", strings.Join(result.Authors, "; "))
	}
	if result.Caution != "" {
		fmt.Printf("\nCAUTION: %s\n\n", result.Caution)
	}
	if result.QuotaInfo != nil {
		fmt.Printf("Quota: %d/%d remaining today\n",
			result.QuotaInfo.DownloadsLeft, result.QuotaInfo.DownloadsPerDay)
	}

	return nil
}
