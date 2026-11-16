package tools

import (
	"context"
	"fmt"
	"strings"

	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

// web_search runs a general web search: Brave for keyword search (the
// default), Exa for semantic search (opt-in by name, metered). Results
// come back as one framed list, since titles and snippets are written
// by the pages, not by grounding.

const (
	WebSearchModeKeyword  = "keyword"
	WebSearchModeSemantic = "semantic"

	WebSearchDefaultCount = 10
	WebSearchMaxCount     = 20

	// Exa returns page text; a snippet for the list is cut from it
	// when Exa picked no highlights.
	webSearchSnippetCharacters = 300
)

// WebSearchOptions are web_search's parameters.
type WebSearchOptions struct {
	Query string
	Mode  string // keyword (default) or semantic
	Count int
}

type webSearchBackends struct {
	brave *backends.BraveBackend
	exa   *backends.ExaBackend
}

func webSearchBackendsFromConfig() webSearchBackends {
	cfg := config.LoadConfig()
	var opts []backends.ClientOption
	if cfg.Email != "" {
		opts = append(opts, backends.WithEmail(cfg.Email))
	}
	client := backends.NewClient(opts...)
	return webSearchBackends{
		brave: backends.NewBrave(client, cfg.BraveAPIKey),
		exa:   backends.NewExa(client, cfg.ExaAPIKey),
	}
}

// SearchWeb runs web_search and returns what the caller sees.
func SearchWeb(ctx context.Context, opts WebSearchOptions) (string, error) {
	return webSearchBackendsFromConfig().searchWeb(ctx, opts)
}

func (searchBackends webSearchBackends) searchWeb(ctx context.Context, opts WebSearchOptions) (string, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	count := opts.Count
	if count <= 0 {
		count = WebSearchDefaultCount
	}
	if count > WebSearchMaxCount {
		count = WebSearchMaxCount
	}

	var results []backends.WebResult
	var err error
	mode := opts.Mode
	switch mode {
	case "", WebSearchModeKeyword:
		mode = WebSearchModeKeyword
		results, err = searchBackends.brave.Search(ctx, query, count)
	case WebSearchModeSemantic:
		results, err = searchBackends.exa.Search(ctx, query, count, backends.ExaSearchOptions{})
	default:
		return "", fmt.Errorf("unknown mode %q (want keyword or semantic)", opts.Mode)
	}
	if err != nil {
		return "", err
	}
	if ledger := ResearchLedgerFromContext(ctx); ledger != nil {
		ledger.RecordWebResults("web_search", results)
	}
	return renderWebSearchResults(query, mode, results), nil
}

func renderWebSearchResults(query, mode string, results []backends.WebResult) string {
	header := fmt.Sprintf("Web search (%s) for %q: %d results.\n", mode, query, len(results))
	if len(results) == 0 {
		return header + "Nothing found. Try other words, or the other mode."
	}
	header += "Read a result in full with web_fetch, giving its URL.\n\n"

	var list strings.Builder
	for position, result := range results {
		fmt.Fprintf(&list, "%d. %s\n   %s\n", position+1, oneLine(result.Title), result.URL)
		// Both engines date the page, not the work on it: Brave's
		// page_age gave Sweller's 1988 paper its Wiley page's 2010.
		if result.PublishedDate != "" {
			fmt.Fprintf(&list, "   Page dated: %s\n", oneLine(result.PublishedDate))
		}
		snippets := result.Highlights
		if len(snippets) == 0 && result.Text != "" {
			snippets = []string{firstRunes(result.Text, webSearchSnippetCharacters)}
		}
		for _, snippet := range snippets {
			fmt.Fprintf(&list, "   %s\n", oneLine(snippet))
		}
		list.WriteString("\n")
	}
	return header + FramedAsExternalContent("web_search results", strings.TrimRight(list.String(), "\n"))
}

// oneLine flattens whitespace so a result's fields cannot start lines
// of their own (and so cannot pose as another result).
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func firstRunes(text string, count int) string {
	runes := []rune(text)
	if len(runes) <= count {
		return text
	}
	return string(runes[:count]) + "…"
}
