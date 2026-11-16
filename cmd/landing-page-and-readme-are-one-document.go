package cmd

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/tools"
)

// The README and the landing page are one document (dorian,
// 2026-11-16: two copies drift). README.md is the source: the page is
// its part above landingPageEndsHereMarker, rendered, with a link to
// the code in place of the part below ("Run your own grounding").
//
// What depends on the tool registry or the visitor's tier is
// generated: each section between <!-- generated: NAME --> and
// <!-- end generated: NAME --> is written into README.md as a public
// visitor sees it, and filled again for each visitor on the page.
//
// Go cannot embed a file above its package, and an add-on's build
// must have the page too, so cmd embeds a copy of the README;
// TestTheReadmeIsCurrentAndTheLandingPageHasItsCopy keeps both
// current. After editing README.md, or anything a generated section
// shows (a tool's title, summary, description or parameters):
//
//	go test ./cmd -run TestTheReadmeIsCurrentAndTheLandingPageHasItsCopy -update-readme

//go:embed landing-page-copy-of-the-readme.md
var landingPageCopyOfTheReadme string

const (
	landingPageEndsHereMarker   = "<!-- the landing page ends here -->"
	publicRepositoryOfTheCode   = "https://github.com/majorminors/grounding"
	landingPageInPlaceOfTheRest = "The code, and how to run your own grounding or extend it, is on [GitHub](" + publicRepositoryOfTheCode + ")."
)

var generatedSectionInTheReadme = regexp.MustCompile(`(?s)<!-- generated: ([a-z-]+) -->(.*?)<!-- end generated: ([a-z-]+) -->`)

// generatedSectionsInline sit inside a sentence; the rest are blocks.
var generatedSectionsInline = map[string]bool{"public-calls-per-hour": true}

// generatedSectionsFor is each generated section's text as a visitor
// of that tier sees it.
func generatedSectionsFor(tier auth.Tier) map[string]string {
	data := buildLandingData(tier)
	return map[string]string{
		"tools-for-people":      toolsForPeopleInMarkdown(data.Tools),
		"public-calls-per-hour": fmt.Sprint(data.PublicCallsPerHour),
		"sources":               sourcesInMarkdown(data),
		"tool-reference":        toolReferenceInMarkdown(data.Tools),
		"tools-and-tiers":       toolsAndTiersInMarkdown(),
	}
}

// readmeWithItsGeneratedSectionsFor fills every generated section of a
// README for a tier. A section it does not know, or a marker pair that
// does not match, is an error: a typo must not leave stale text.
func readmeWithItsGeneratedSectionsFor(readme string, tier auth.Tier) (string, error) {
	sections := generatedSectionsFor(tier)
	var problem error
	filled := generatedSectionInTheReadme.ReplaceAllStringFunc(readme, func(found string) string {
		parts := generatedSectionInTheReadme.FindStringSubmatch(found)
		name, endName := parts[1], parts[3]
		text, known := sections[name]
		if name != endName || !known {
			if problem == nil {
				problem = fmt.Errorf("README: generated section %q (ended as %q) is not one grounding writes", name, endName)
			}
			return found
		}
		if generatedSectionsInline[name] {
			return "<!-- generated: " + name + " -->" + text + "<!-- end generated: " + name + " -->"
		}
		return "<!-- generated: " + name + " -->\n\n" + strings.TrimSpace(text) + "\n\n<!-- end generated: " + name + " -->"
	})
	return filled, problem
}

var markdownToHTML = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	// The README is ours, built into the binary, and holds a <details>.
	goldmark.WithRendererOptions(goldmarkhtml.WithUnsafe()),
)

// landingPageBodyFor renders the README's part above the marker for a
// tier, with the link to the code in place of the rest.
func landingPageBodyFor(tier auth.Tier) (template.HTML, error) {
	visitorsPart, _, found := strings.Cut(landingPageCopyOfTheReadme, landingPageEndsHereMarker)
	if !found {
		return "", fmt.Errorf("README has no %q", landingPageEndsHereMarker)
	}
	filled, err := readmeWithItsGeneratedSectionsFor(visitorsPart, tier)
	if err != nil {
		return "", err
	}
	var rendered bytes.Buffer
	if err := markdownToHTML.Convert([]byte(filled+"\n\n"+landingPageInPlaceOfTheRest+"\n"), &rendered); err != nil {
		return "", err
	}
	return template.HTML(rendered.String()), nil
}

// textForMarkdown keeps registry text from opening raw HTML.
func textForMarkdown(text string) string {
	return strings.ReplaceAll(text, "<", "&lt;")
}

func toolsForPeopleInMarkdown(visibleTools []landingTool) string {
	var lines []string
	for _, tool := range visibleTools {
		lines = append(lines, fmt.Sprintf("- **%s.** %s", textForMarkdown(tool.Title), textForMarkdown(tool.SummaryForPeople)))
	}
	return strings.Join(lines, "\n")
}

// sourcesOnTheLandingPage are core's catalogues, as every visitor sees
// them; sourcesOnTheFullTierLandingPage are added for a full-tier
// visitor, before any an add-on lists.
var sourcesOnTheLandingPage = []tools.SourceOnTheLandingPage{
	{Name: "OpenAlex", What: "open catalogue of scholarly works"},
	{Name: "Crossref", What: "the DOI registry, for checking citations"},
	{Name: "Semantic Scholar", What: "paper search with abstracts and citation data"},
	{Name: "Europe PMC", What: "biomedical and life-science papers"},
	{Name: "OpenCitations", What: "open citation graph, the fallback when the others are down"},
	{Name: "Open Library", What: "books"},
	{Name: "Wikipedia", What: "general background"},
}

var sourcesOnTheFullTierLandingPage = []tools.SourceOnTheLandingPage{
	{Name: "Unpaywall", What: "legal open-access copies by DOI, tried before any other download source"},
	{Name: "Consensus", What: "AI-powered academic search with study takeaways (~1¢/query)"},
	{Name: "Exa", What: "neural web search with page text (metered per call)"},
}

func sourcesInMarkdown(data landingData) string {
	sources := append([]tools.SourceOnTheLandingPage(nil), sourcesOnTheLandingPage...)
	if data.Authenticated {
		sources = append(sources, sourcesOnTheFullTierLandingPage...)
		sources = append(sources, data.SourcesAddOnsList...)
	}
	var lines []string
	for _, source := range sources {
		lines = append(lines, fmt.Sprintf("- **%s**—%s", textForMarkdown(source.Name), textForMarkdown(source.What)))
	}
	return strings.Join(lines, "\n")
}

// fenceLongerThanAnyIn is a code fence no line of text can close.
func fenceLongerThanAnyIn(text string) string {
	longest := 0
	for _, run := range regexp.MustCompile("`+").FindAllString(text, -1) {
		if len(run) > longest {
			longest = len(run)
		}
	}
	if longest < 3 {
		return "```"
	}
	return strings.Repeat("`", longest+1)
}

func toolReferenceInMarkdown(visibleTools []landingTool) string {
	var out strings.Builder
	for i, tool := range visibleTools {
		if i > 0 {
			out.WriteString("\n\n")
		}
		fence := fenceLongerThanAnyIn(tool.Description)
		fmt.Fprintf(&out, "#### %s `%s`\n\n%s\n%s\n%s", textForMarkdown(tool.Title), tool.Name, fence, tool.Description, fence)
		if len(tool.Params) > 0 {
			out.WriteString("\n")
		}
		for _, param := range tool.Params {
			about := []string{param.Type}
			if param.Required {
				about = append(about, "required")
			}
			if param.Default != "" {
				about = append(about, "default "+textForMarkdown(param.Default))
			}
			fmt.Fprintf(&out, "\n- `%s` (%s)", param.Name, strings.Join(about, ", "))
			if param.Desc != "" {
				fmt.Fprintf(&out, ": %s", textForMarkdown(strings.Join(strings.Fields(param.Desc), " ")))
			}
		}
	}
	return out.String()
}

// toolsAndTiersInMarkdown is every tool and the tier that sees it, for
// someone running their own.
func toolsAndTiersInMarkdown() string {
	lines := []string{"| Tool | Tier |", "|---|---|"}
	for _, tool := range buildLandingData(auth.TierFull).Tools {
		registered, _ := tools.Lookup(tool.Name)
		tier := "public"
		if registered.RequiresAuth {
			tier = "full"
		}
		lines = append(lines, fmt.Sprintf("| `%s` | %s |", tool.Name, tier))
	}
	return strings.Join(lines, "\n")
}
