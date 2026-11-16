package cmd

import (
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"time"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/tools"
)

// The landing page is for people deciding whether to connect, not for
// the model: it shows each tool's Title and SummaryForPeople, and keeps
// the model-facing descriptions and parameters behind a <details>. The
// styling is lifted from txt.btr.mt (btrmt-site's
// resources/views/text/layouts/text.blade.php) so the page reads as
// part of btr.mt: same palette, serif, dark mode, zero JS, no fonts.
var landingTmpl = template.Must(template.New("landing").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Grounding — btrmt.</title>
<meta name="description" content="Give your AI agents real sources to reason from. A free MCP server that finds, checks and traces academic sources for Claude, ChatGPT and other assistants.">
<meta property="og:title" content="Grounding | btrmt.">
<meta property="og:description" content="Give your AI agents real sources to reason from.">
<meta property="og:url" content="https://grounding.btr.mt/">
<meta property="og:site_name" content="btrmt.">
<link rel="canonical" href="https://grounding.btr.mt/">
<link rel="alternate" type="text/plain" title="llms.txt" href="/llms.txt">
<meta name="author" content="Dorian Minors">
<script type="application/ld+json">{{.StructuredData}}</script>
<style>
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: Georgia, 'Times New Roman', Times, serif;
    font-size: 1.05rem; line-height: 1.7;
    color: #3E2524; background: #FFEEE1;
    max-width: 42em; margin: 0 auto; padding: 2em 1.5em;
  }
  h1 { font-size: 1.6rem; margin: 0.8em 0 0.4em; line-height: 1.3; }
  h2 { font-size: 1.3rem; margin: 1.6em 0 0.4em; line-height: 1.3; }
  h3 { font-size: 1.05rem; margin: 1em 0 0.2em; }
  p { margin: 0.8em 0; }
  ul, ol { margin: 0.8em 0; padding-left: 1.5em; }
  li { margin: 0.3em 0; }
  a { color: #A33E3B; text-decoration: underline; text-decoration-color: #6BA07D; text-decoration-thickness: 1px; text-underline-offset: 2px; }
  a:visited { color: #7A2E2C; }
  a:hover { text-decoration-thickness: 2px; }
  code { font-family: 'Courier New', Courier, monospace; font-size: 0.9em; }
  pre { overflow-x: auto; padding: 0.8em 1em; margin: 0.6em 0; background: #F3E1D5; font-size: 0.85em; line-height: 1.5; white-space: pre-wrap; word-break: break-word; }
  blockquote { border-left: 3px solid #C5B1A8; padding-left: 1em; margin: 1em 0; color: #77615C; }
  hr { border: none; border-top: 1px solid #C5B1A8; margin: 1.5em 0; }

  .nav { border-bottom: 1px solid #C5B1A8; padding-bottom: 1em; margin-bottom: 1.5em; }
  .logo-lockup { display: flex; align-items: center; gap: 0.6em; text-decoration: none; color: inherit; }
  .logo-lockup:visited { color: inherit; }
  .logo-svg { width: 1.6em; height: 1.6em; flex-shrink: 0; }
  .wordmark-text { font-family: 'Courier New', Courier, monospace; font-size: 1.8rem; font-weight: 700; letter-spacing: -0.02em; }
  .tagline { font-style: italic; margin: 0.2em 0 0; }

  .muted { color: #77615C; }
  .small { font-size: 0.88rem; }


  details { margin: 1.5em 0; border-top: 1px solid #C5B1A8; padding-top: 0.8em; }
  summary { cursor: pointer; color: #77615C; }
  summary:hover { color: #3E2524; }
  details[open] summary { margin-bottom: 0.8em; }

  /* The body is the README rendered (landing-page-and-readme-are-one-document.go). */
  h4 { font-size: 1rem; margin: 1.2em 0 0.2em; }
  main > h1 + p { font-size: 1.15rem; }
  main > pre { user-select: all; -webkit-user-select: all; }
  details pre { font-size: 0.85rem; max-height: 18em; overflow-y: auto; }
  details ul { font-size: 0.92rem; }
  table { border-collapse: collapse; font-size: 0.9rem; }
  th, td { border-bottom: 1px solid #D8C5BB; padding: 0.2em 0.6em; text-align: left; }
  #what-it-does + ul, #sources + ul { list-style: none; padding: 0; }
  #what-it-does + ul li { padding: 0.6em 0; border-bottom: 1px solid #D8C5BB; margin: 0; }
  #what-it-does + ul li:last-child { border-bottom: none; }

  .site-footer { border-top: 1px solid #C5B1A8; margin-top: 3em; padding-top: 1em; font-size: 0.85rem; color: #77615C; }
  .site-footer a { color: inherit; }

  @media (prefers-color-scheme: dark) {
    body { color: #FFEEE1; background: #3E2524; }
    a { color: #6BA07D; text-decoration-color: #A33E3B; }
    a:visited { color: #8AB092; }
    pre { background: #4D3533; }
    hr, .nav, details, .site-footer { border-color: #513936; }
    blockquote { border-left-color: #644D49; color: #A8938B; }
    .muted, summary, .site-footer { color: #A8938B; }
    summary:hover { color: #FFEEE1; }
    #what-it-does + ul li, th, td { border-bottom-color: #4D3533; }
  }
</style>
</head>
<body>

<header class="nav">
  <a href="https://btr.mt" class="logo-lockup">
    <svg class="logo-svg" aria-hidden="true" viewBox="0 0 321.192 325.077" fill="currentColor">
        <path d="M171.89 321.83c-3.473-3.075-4.67-9.372-5.964-31.408-.315-5.36-1.252-15.656-2.082-22.88-.83-7.222-1.69-15.933-1.911-19.357-.702-10.856-.755-30.563-.088-32.614 1.595-4.906 9.485-4.19 15.39 1.398 3.29 3.113 4.65 7.596 5.473 18.046 1.527 19.354 1.869 22.713 3.953 38.824 1.193 9.22 2.196 20.215 2.228 24.431.063 8.1-2.117 23.103-3.671 25.273-1.914 2.671-9.473 1.7-13.328-1.713zM44.937 229.97c-1.98-1.787.584-4.864 14.737-17.685 4.612-4.178 10.053-9.292 12.09-11.364 5.056-5.143 8.22-7.236 11.321-7.49 3.671-.299 11.983 2.519 14.528 4.926 3.875 3.664 2.895 5.186-8.378 13.018-11.966 8.313-23.383 15.455-29.918 18.715-5.31 2.648-5.654 2.707-9.574 1.647-2.237-.605-4.4-1.4-4.806-1.767zm73.438-39.98c-4.548-3.688-7.321-9.483-8.401-17.553-.672-5.015-.62-7.961.197-11.388l1.104-4.623 3.45-1.571c6.071-2.764 9.899-2.898 16.981-.596 5.656 1.839 6.604 2.382 10.11 5.796 7.15 6.96 10.974 20.236 8.328 28.911-1.232 4.038-1.455 4.317-4.583 5.745-6.882 3.14-20.382.796-27.186-4.721zm136.594 29.648c-9.871-7.222-21.667-17.694-23.59-20.942-1.312-2.217-.323-3.958 2.68-4.715 2.32-.585 3.587-.36 8.855 1.58 5.187 1.91 6.77 2.846 10.14 6 6.617 6.193 18.5 20.042 19.106 22.268.531 1.952.465 2.118-1.047 2.614-2.81.922-9.352-1.836-16.144-6.805zm-68.908-35.912c-5.37-4.478-7.594-13.07-5.217-20.155 3.556-10.597 13.435-14.71 25.883-10.775 12.513 3.954 19.429 12.98 16.627 21.7-1.078 3.355-5.767 8.176-9.078 9.335-1.518.53-4.43 1.675-6.472 2.542-2.129.905-5.47 1.531-7.83 1.47-4.503-.12-11.658-2.236-13.913-4.117zM83.103 113.171c-8.72-5.52-28.767-17.374-44.549-26.34C7.036 68.921 2.83 66.24.932 62.84c-1.52-2.724-1.215-4.12 1.165-5.331 4.81-2.45 16.002 1.254 35.304 11.685 49.456 26.728 84.493 52.035 77.854 56.236-2.198 1.39-6.182 1.206-11.28-.522-3.758-1.274-8.996-4.22-20.872-11.738zm68.615 6.658c-6.04-4.684-8.399-11.658-6.556-19.383 1-4.188 1.21-4.494 5.236-7.617 5.246-4.071 7.757-5.002 13.044-4.836 5.154.162 11.613 2.776 14.837 6.005 4.17 4.176 6.82 12.502 6.767 21.268-.03 4.79-.245 5.818-1.498 7.126-2.11 2.202-8.253 4.136-12.77 4.02-5.586-.143-14.908-3.363-19.06-6.583zm59.766-1.994c-1.078-.752-1.985-1.862-2.017-2.466-.08-1.55 6.551-8.303 13.143-13.381 3.072-2.367 12.017-8.75 19.878-14.186 7.862-5.435 19.444-13.688 25.738-18.34 16.37-12.096 25.308-17.831 33.702-21.625 8.411-3.801 10.386-4.165 14.898-2.746 5.702 1.793 5.81 5.105.377 11.552-3.844 4.56-11.186 10.453-26.112 20.96-14.442 10.164-18.051 12.444-33.976 21.455-12.296 6.958-20.529 11.93-28.781 17.378-2.33 1.54-5.34 3.038-6.688 3.331-3.127.681-7.65-.179-10.162-1.932zM148.639 52.28c-1.094-.766-2.317-2.442-2.718-3.723-1.685-5.382-.96-23.324 1.212-30.006.569-1.75 1.404-6.53 1.856-10.622.882-7.986 1.245-8.586 4.556-7.536 3.815 1.21 8.403 20.345 9.14 38.112.6 14.484.419 15.67-2.524 16.472-2.64.718-8.657-.69-11.522-2.697z"/>
    </svg>
    <span class="wordmark-text">btrmt.</span>
  </a>
  <p class="tagline">ideologies worth choosing</p>
</header>

<main>

{{.Body}}

<details>
<summary>About the author (and a note for AI agents)</summary>
{{- range .AboutAuthorParagraphs}}
<p class="small">{{.}}</p>
{{- end}}
<p class="small">AI agents: a plain-text summary of this server, its tools and
its author is at <a href="/llms.txt">/llms.txt</a>.</p>
<ul class="small">
{{- range .AuthorshipLinks}}
  <li><a href="{{.URL}}">{{.Label}}</a></li>
{{- end}}
</ul>
</details>

</main>

<footer class="site-footer">
  <p>A <a href="https://btr.mt">btrmt.</a> project by Dorian Minors ·
  <a href="https://btr.mt/analects/enigma-of-ai-reason">the argument behind it</a> ·
  version {{.Version}}</p>
</footer>

</body>
</html>
`))

type landingData struct {
	// Body is the README's part for visitors, rendered for the tier.
	Body template.HTML

	Tools              []landingTool
	Version            string
	Authenticated      bool
	PublicCallsPerHour int

	// SourcesAddOnsList are shown to authenticated visitors only.
	SourcesAddOnsList []tools.SourceOnTheLandingPage

	// Authorship for AI agents; see authorship-for-agents.go.
	AboutAuthorParagraphs []string
	AuthorshipLinks       []authorshipLink
	StructuredData        template.JS
}

type landingTool struct {
	Name             string
	Title            string
	SummaryForPeople string
	Description      string
	Params           []landingParam
}

type landingParam struct {
	Name     string
	Type     string
	Desc     string
	Required bool
	Default  string
}

// landingPageToolOrder puts the tools people care about first; the
// health check is last because nobody connects for it. Tools not
// listed follow, alphabetically.
var landingPageToolOrder = []string{
	"search_catalogue",
	"check_citation",
	"citation_graph",
	"search_downloadable_books",
	"find_download_link",
	"check_backends",
}

func landingPageRank(name string) int {
	for rank, ordered := range landingPageToolOrder {
		if ordered == name {
			return rank
		}
	}
	return len(landingPageToolOrder)
}

func buildLandingData(tier auth.Tier) landingData {
	allTools := append([]tools.ToolDef(nil), tools.All()...)
	sort.SliceStable(allTools, func(i, j int) bool {
		rankI, rankJ := landingPageRank(allTools[i].Name), landingPageRank(allTools[j].Name)
		if rankI != rankJ {
			return rankI < rankJ
		}
		return allTools[i].Name < allTools[j].Name
	})

	data := landingData{
		Version:            version,
		Authenticated:      tier == auth.TierFull,
		PublicCallsPerHour: int(time.Hour / publicTierCallRefillIntervalPerClient),
		SourcesAddOnsList:  tools.SourcesOnTheFullTierLandingPageAddedByAddOns(),

		AboutAuthorParagraphs: aboutAuthorParagraphs,
		AuthorshipLinks:       authorshipLinks,
		StructuredData:        structuredDataForLandingPage(),
	}
	for _, t := range allTools {
		// Full-tier tools are not advertised to public callers; the
		// same flag drives tools/list filtering in serve.go.
		if t.RequiresAuth && tier == auth.TierPublic {
			continue
		}
		lt := landingTool{
			Name:             t.Name,
			Title:            t.Title,
			SummaryForPeople: t.SummaryForPeople,
			Description:      t.DescriptionForTier(tier),
		}

		reqSet := make(map[string]bool, len(t.InputSchema.Required))
		for _, r := range t.InputSchema.Required {
			reqSet[r] = true
		}

		// Sort property names for stable output
		names := make([]string, 0, len(t.InputSchema.Properties))
		for name := range t.InputSchema.Properties {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			prop := t.InputSchema.Properties[name]
			p := landingParam{
				Name:     name,
				Type:     prop.Type,
				Desc:     prop.Description,
				Required: reqSet[name],
			}
			if prop.Default != nil {
				p.Default = formatDefault(prop.Default)
			}
			lt.Params = append(lt.Params, p)
		}

		data.Tools = append(data.Tools, lt)
	}
	return data
}

func formatDefault(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int(val)) {
			return fmt.Sprintf("%d", int(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		return fmt.Sprintf("%t", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func makeLandingHandler(authToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tier := auth.ResolveTier(authToken, r)
		data := buildLandingData(tier)
		body, err := landingPageBodyFor(tier)
		if err != nil {
			http.Error(w, "the landing page could not be built", http.StatusInternalServerError)
			return
		}
		data.Body = body
		landingTmpl.Execute(w, data)
	}
}
