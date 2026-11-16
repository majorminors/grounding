# Grounding

_"Reason"_ is both a gift and a curse. It helps us create
sophisticated arguments, but those sophisticated arguments only
have to _sound_ good, they don't need to reflect the truth. In
humans we call the cursed version of reason "man-guessing" and
"echo-chambers". In machine intelligence, we call it
"hallucination" and "sycophancy". "Reason" needs [two things to be
a gift, not a curse](https://btr.mt/analects/enigma-of-ai-reason):
it needs **social friction** and **intuitions grounded in the real
world**. This tool tries to give your reasoning agents the latter.
The social friction is up to you (but I'll give you some
suggestions).

## Connect

This is a remote [MCP](https://modelcontextprotocol.io) server: you
give your assistant the following address, and it gains the tooling
below:

```
https://grounding.btr.mt/mcp
```

It's free, and doesn't need sign in, but it is linked to my website,
so I'll just take it down if it gets abused.

### Claude (web or desktop app)

Settings → Connectors → _Add custom connector_. Name it “Grounding”
and paste the address above.

### Claude Code

```
claude mcp add --transport http grounding https://grounding.btr.mt/mcp
```

### ChatGPT and other assistants

Anywhere that accepts a remote MCP server (in ChatGPT, that’s
developer mode): add the address above. The transport is Streamable
HTTP, with no authentication.

## What it does

<!-- generated: tools-for-people -->

- **Find sources on a question.** Searches the academic literature, books and Wikipedia at once and hands back real, citable sources with abstracts, and a legal open-access copy where one exists, so the model reasons from evidence rather than memory.
- **Check a citation is real.** Confirms that a paper or book actually exists, by title, author, year or DOI, and returns its real details. Catches invented references before they reach you.
- **Explore a paper’s citations.** Start from one paper and see what it builds on and what has built on it since, ranked by influence. The quickest way to find the foundational work that keyword searches miss.
- **Check the sources are up.** Reports which of the databases behind this server are answering right now. Useful when results look thin.

<!-- end generated: tools-for-people -->

You don’t call these yourself: the model picks them up when a
question needs them. You can nudge it, though. Try:

- “Before you answer, find me the research on how sleep affects
  memory consolidation.”
- “Is ‘Kahneman & Tversky, 1974, Judgment under uncertainty’ a real
  paper? Check it.”
- “What are the foundational papers behind this one? Use the
  citation graph.”

## Using it well

Grounding only helps if the model is made to use it, and to argue
with itself. Paste something like this into your assistant’s custom
instructions (or a project’s):

```
Before you cite anything, check it with the grounding tools. Tell me where each claim comes from: a source you retrieved, or your own recall. When I put forward an idea, argue against it before you agree. Empirically, reasoned inference is weakest in the absence of social friction and intuitions grounded in the world or in data (see e.g. Mercier & Sperber, 2017). The grounding tools give you grounded intuitions, but we need to generate the adversarial friction together, or we're just engaging in the worst possible version of reasoning.
```

Note: We add the stakes here because models, like people, respond
much better when they have a 'why'.

## What it won’t do

- It finds and checks sources; it doesn’t read them for you. The
  model sees abstracts and details, not full texts, so you still
  need to read what matters.
- It searches by keyword, so it can miss work that uses different
  words for the same idea. Starting from a paper you trust and
  following its citations often finds what a search didn’t.
- Short, specific questions get better results than long, sprawling
  ones.
- It’s shared and free. Each person gets about <!-- generated: public-calls-per-hour -->40<!-- end generated: public-calls-per-hour -->
  tool calls an hour, which is plenty for real work but not for a
  runaway loop. On a busy day one of the databases may pause
  for up to an hour; the others carry on.

**Privacy.** Your questions are passed to the databases listed
below in order to answer them. This server doesn’t keep them, beyond
a ten-minute cache of identical requests. It logs which tool was
called, when, and how long it took, and holds your network address
in memory for up to two hours to apply the hourly limit.

<details>
<summary>Under the hood: sources, tools and parameters</summary>

### Sources

<!-- generated: sources -->

- **OpenAlex**—open catalogue of scholarly works
- **Crossref**—the DOI registry, for checking citations
- **Semantic Scholar**—paper search with abstracts and citation data
- **Europe PMC**—biomedical and life-science papers
- **OpenCitations**—open citation graph, the fallback when the others are down
- **Open Library**—books
- **Wikipedia**—general background

<!-- end generated: sources -->

### Credits

The web tools read pages with Crawl4AI, run on grounding’s own
server. This product includes software developed by UncleCode
(https://x.com/unclecode) as part of the Crawl4AI project
(https://github.com/unclecode/crawl4ai). grounding calls a crawl4ai
server; it includes none of its code.

### Tools

What follows is what the model itself is told about each tool.

<!-- generated: tool-reference -->

#### Find sources on a question `search_catalogue`

```
Search catalogues of citable sources (research papers, books and encyclopaedia articles) for real sources to reason from, before making claims.

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

**Literature characterisation:** 2–3 search_catalogue calls with different phrasings → citation_graph on anchor papers (direction "citations") to find follow-up work. Synthesise across abstracts; don't rely on any single paper.
```

- `backends` (array): Specific backends to use (overrides source_type entirely). Options: semantic_scholar, openalex, crossref, europepmc, openlibrary (books), wikipedia. Useful for targeting a single backend, e.g. ["europepmc"] for biomedical literature. Use check_backends to see every backend available to you, including opt-in ones.
- `debug` (boolean): Also return per-backend diagnostics (raw and filtered counts, errors, skips). Default false: warnings and the empty-result explanation are returned either way.
- `domain` (string): OpenAlex top-level domain filter. Restricts OA results to a discipline. Values: "Social Sciences", "Health Sciences", "Life Sciences", "Physical Sciences". **Important:** domain ONLY filters OpenAlex results. Crossref and Semantic Scholar results pass through unfiltered — using domain alone can make results worse by removing OA's good matches while Crossref STEM noise remains. For cleanest results, combine domain with backends: ["openalex"] — e.g. backends ["openalex"] + domain "Social Sciences" eliminates cross-discipline noise entirely.
- `max_results` (number, default 5): Maximum results to return. Default: 5. Use 10–15 when exploring a topic or looking for anchor papers to feed into citation_graph. Higher values increase noise but improve coverage for broad queries.
- `min_citations` (number): Minimum citation count. Filters out papers below this threshold. Effective for finding established/canonical work: 50 for established, 100+ for foundational. Caution: filters out recent work that hasn't accumulated citations yet. Don't use when looking for the latest research.
- `query` (string, required): Keyword query. **Including an author surname is the most effective way to improve precision.** "Stoet Geary gender equality paradox" returns the right paper; "gender equality paradox STEM" returns stem cell papers. "Hochschild second shift" finds the book; "second shift women employment" returns genomics noise. Good: "Buss sexual strategies theory", "household labor division gender". Bad: "how is household labour divided between genders in modern dual-income families" (too long), "second shift emotional labour gender Hochschild" (too many terms). For Wikipedia (source_type "general"), 2–4 precise terms work best.
- `source_type` (string, default auto): Type of sources to search. Default: "auto". "academic" when you only need papers (faster, skips Wikipedia). "general" for background context and definitions (Wikipedia only). "auto" searches academic and general backends.
- `year_max` (number): Maximum publication year (inclusive). Rarely needed. Use to cap results to a specific era (e.g. pre-replication-crisis work before 2011).
- `year_min` (number): Minimum publication year (inclusive). Useful for "recent work only" queries. Caution: excludes foundational older works — omit when looking for canonical literature.

#### Check a citation is real `check_citation`

```
Verify that a citation/reference exists before presenting it to the user.

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

**Tip:** The DOI returned here can be passed directly to citation_graph for reliable graph traversal — more dependable than passing a title.
```

- `authors` (array): Author names (e.g., ["Smith, J.", "Jones, A."]). Improves matching accuracy.
- `doi` (string): DOI of the paper (e.g., "10.1037/apl0000353"). Preferred if available.
- `title` (string): Title of the paper. Required if no DOI provided.
- `year` (number): Publication year. Improves matching accuracy.

#### Explore a paper’s citations `citation_graph`

```
Start from a known paper and explore the research landscape around it.

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
Example: check_citation → DOI "10.1177/0891243287001002002" → citation_graph with that DOI.
```

- `direction` (string, default both): Which direction to traverse. "references": papers cited by seed — use to find intellectual foundations. "citations": papers citing seed — use to find follow-up work. "both" (default) doubles API calls — use a single direction when you know what you need.
- `limit` (number, default 20): Maximum results per direction (default 20, max 1000). 20 is good for exploration. 50–100 for literature characterisation. Higher values are slower and results become less relevant.
- `min_citations` (integer, default 0): Minimum citation count to include an edge (0 = no filter). Post-retrieval filter; increase limit to compensate for filtered results
- `paper_id` (string, required): Paper identifier: DOI, arXiv ID, Semantic Scholar ID, or title for search fallback
- `sort_by` (string, default recency): Sort order for results. Default "recency" returns mostly very recent low-impact papers for highly-cited seeds. Switch to "citations" for canonical follow-ups and the most influential citing works.

#### Check the sources are up `check_backends`

```
Check health status of all grounding backends at once.

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
- When tools return errors, to diagnose which backend is down
```

- `force` (boolean): Also run the probes skipped by default because they spend something (a shared rate budget, or a metered call). Default false: those backends report as configured-but-unprobed. Parameter text is the same for every caller; the tool description says which backends and what each costs.

<!-- end generated: tool-reference -->

</details>

<!-- the landing page ends here -->

## Run your own grounding

grounding is a Go CLI and MCP server. It runs locally over stdio,
or as a remote Streamable HTTP server with a public tier and a
bearer-token full tier. This repository is the core: free and
sanctioned sources only. An add-on can add others (Add-ons, below).

Everything above this section is also the server's landing page:
`cmd/landing-page-and-readme-are-one-document.go` renders it, and
fills the sections marked `generated` from the tool registry for
each visitor. After changing this file, or a tool's text, run
`go test ./cmd -run TestTheReadmeIsCurrentAndTheLandingPageHasItsCopy -update-readme`.

### Building and running

```bash
go build -o grounding .
# with a version stamp (shows in /health and the User-Agent):
go build -ldflags "-X btr.mt/grounding/cmd.version=$(git describe --tags --always)" -o grounding .

./grounding serve                                  # stdio (full tier)
GROUNDING_AUTH_TOKEN=… ./grounding serve --transport http --addr localhost:9471
```

Register a local build with Claude Code:

```bash
claude mcp add --scope user grounding -- /path/to/grounding serve
```

Use a remote server at full tier:

```bash
claude mcp add --transport http --header "Authorization: Bearer $GROUNDING_TOKEN" grounding https://your-server/mcp
```

claude.ai connectors cannot set headers; use
`https://your-server/mcp?token=…` there.

Every tool is also a CLI command, named as the tool in hyphens
(`grounding search-catalogue`, `grounding check-citation`, …);
`grounding --help` lists them and `grounding schema` prints the MCP
tool definitions.

### Access tiers

| Condition | Tier |
|---|---|
| stdio transport | full |
| http, correct `Authorization: Bearer <token>` or `?token=<token>` | full |
| http, no or wrong token | public |
| http, `GROUNDING_AUTH_TOKEN` unset | refuses to start |

Public-tier tool calls are limited per client address (a burst of
40, then one every 90 seconds), because every public caller shares
one OpenAlex budget and the contact email's standing upstream. A
caller over the limit gets a tool error that tells the model to
explain the wait. Full-tier calls are not limited. The client
address is the rightmost `X-Forwarded-For` entry, the one a reverse
proxy such as Caddy writes.

Every tool carries MCP annotations: a human `title` and
`readOnlyHint` (true for every tool: none writes on the server), so
clients can run calls in parallel without confirmation prompts.

### Environment variables

| Variable | Purpose |
|---|---|
| `GROUNDING_EMAIL` | Contact email: Crossref polite pool, Wikimedia UA policy, Unpaywall (required by it) |
| `GROUNDING_AUTH_TOKEN` | Bearer token for the full tier over http (required for http) |
| `OPENALEX_API_KEY` | Free key; without it OpenAlex allows ~100 list queries/day per IP |
| `SEMANTIC_SCHOLAR_API_KEY` | Optional; 1 req/s per key instead of the shared pool |
| `OPENCITATIONS_TOKEN` | Optional; leave unset rather than guess (a wrong token is a hard 403) |
| `EXA_API_KEY` | Enables the Exa web backend (metered) |
| `CONSENSUS_APP_KEY` | Enables Consensus (metered) |
| `BRAVE_API_KEY` | Enables `web_search` keyword mode |
| `FIRECRAWL_API_KEY` | Enables Firecrawl: renderer `auto`'s last rung and the `firecrawl` renderer (a credit a page) |
| `GROUNDING_STATE_DIRECTORY` | Where grounding keeps state across runs (Firecrawl's daily credit count). Default: systemd's `$STATE_DIRECTORY`, else `$XDG_STATE_HOME/grounding`, else `~/.local/state/grounding` |
| `FIRECRAWL_DAILY_CREDIT_CAP` | Most Firecrawl credits spent in a UTC day, across the host's grounding processes (default 25; `off` for none) |
| `CRAWL4AI_BASE_URL` | A self-hosted crawl4ai server (e.g. `http://127.0.0.1:11235`, or `unix:///path/to/its.sock`): renderer `auto`'s free browser |
| `CRAWL4AI_API_TOKEN` | crawl4ai's bearer token |
| `ANTHROPIC_API_KEY` | Without the token below, enables `answer_question_from_web_page` (its reader is Claude Haiku 5.5, paid per call) and `research_question` on the API (paid per run). Unused when the token is set |
| `GROUNDING_RESEARCH_CLAUDE_CODE_OAUTH_TOKEN` | A `claude setup-token` token: `research_question` and `answer_question_from_web_page` run in headless Claude Code on that subscription instead of the API. The subscription holder's own use only |
| `GROUNDING_RESEARCH_CLAUDE_BINARY` | Claude Code's executable for those runs (default `claude` on `PATH`) |
| `GROUNDING_RESEARCH_RUN_RECORDS_DIR` | Where `research_question` keeps its run records; `off` keeps none. Default: `research-runs` under systemd's `$STATE_DIRECTORY`, else `$XDG_STATE_HOME/grounding/research-runs`, else `~/.local/state/grounding/research-runs` |
| `GROUNDING_RESEARCH_RUN_RECORDS_KEEP_DAYS` | Days a run record is kept (default 90; 0 keeps them all) |
| `GROUNDING_DOWNLOAD_DIRECTORY` | CLI only: where `find-download-link` saves the file when `--save-to` is not given; unset = print the link only |

The binary does not read `.env`; export the variables (or use an
`EnvironmentFile=` in systemd).

### Backends

| Backend | Used for | Key | Tier |
|---|---|---|---|
| OpenAlex | search, verification, citation graph fallback, DOI batches | `OPENALEX_API_KEY` (free; 10× the daily budget) | public |
| Crossref | search, DOI verification | none (`GROUNDING_EMAIL` for the polite pool) | public |
| Europe PMC | search, verification; abstracts and OA full text | none | public |
| Semantic Scholar | search, citation graph (influence markers, intents) | `SEMANTIC_SCHOLAR_API_KEY` (optional) | public |
| OpenCitations | citation graph, third fallback | `OPENCITATIONS_TOKEN` (optional) | public |
| Wikipedia | general knowledge | none (`GROUNDING_EMAIL` in the User-Agent) | public |
| OpenLibrary | book search (in the default search, under `books`), book verification | none | public |
| Unpaywall | legal open-access copy for a DOI: every search's `availability`, and `find_download_link` | `GROUNDING_EMAIL` | public (availability); full (links) |
| Consensus | study-level search; opt-in `backends: ["consensus"]` (~1¢/query) | `CONSENSUS_APP_KEY` | full |
| Exa | web pages with text: `web_search` mode `semantic`, opt-in (metered) | `EXA_API_KEY` | full |
| Brave Search | `web_search` mode `keyword` (the default) | `BRAVE_API_KEY` | full |
| crawl4ai (self-hosted) | the web tools' free browser: renderer `auto`'s second rung, or `crawl4ai` by name | `CRAWL4AI_BASE_URL`, `CRAWL4AI_API_TOKEN` | full |
| Firecrawl | the web tools' paid browser: renderer `auto`'s last rung, within a daily credit cap, or `firecrawl` by name (a credit a page) | `FIRECRAWL_API_KEY`, `FIRECRAWL_DAILY_CREDIT_CAP` | full |
| Claude Sonnet 5.5 in Claude Code, else Claude Haiku 5.5 on the API | the separate reader behind `answer_question_from_web_page` (subscription usage, else paid per call) | `GROUNDING_RESEARCH_CLAUDE_CODE_OAUTH_TOKEN`, else `ANTHROPIC_API_KEY` | full |

Search results are deduplicated across backends, filtered by
relevance (query-term overlap in title and abstract) and re-ranked
by a composite of relevance, abstract presence and citation count.

The shared HTTP client keeps one rate limiter per backend for the
life of the process, retries a 429 once and then benches the backend
(honouring `Retry-After`, capped at an hour), retunes Crossref from
its `x-rate-limit-*` headers, and sends the contact email in the
User-Agent, which is how Crossref's polite pool and Wikimedia's
identified-client tier are granted. The server also caches GET
responses for ten minutes.

### Tools

<!-- generated: tools-and-tiers -->

| Tool | Tier |
|---|---|
| `search_catalogue` | public |
| `check_citation` | public |
| `citation_graph` | public |
| `find_download_link` | full |
| `check_backends` | public |
| `answer_question_from_web_page` | full |
| `research_question` | full |
| `web_fetch` | full |
| `web_search` | full |

<!-- end generated: tools-and-tiers -->

Full-tier tools are absent from `tools/list` and the landing page
for public callers. Full-tier sessions (and the CLI) also see longer
descriptions on `search_catalogue` and `check_backends` that name
the opt-in backends and what each costs; public descriptions never
mention them. `search_catalogue` returns per-backend diagnostics
only with `debug: true` (`--debug`), and always logs them (Catalogue
search log, below); `warnings` and `empty_result_explanation` come
back either way. A diagnostic carries `cost_note` for Consensus,
whose price the full-tier description also gives. `check_backends`
does not probe Consensus or Exa unless `force` is true, since their
health checks are billed queries.

`find_download_link` finds a link to a copy of a document:
Unpaywall's legal open-access copy first, then any download source
an add-on registers. The tool only ever returns a `download_url`,
which the caller fetches from its own network; the server writes
nothing. The CLI command (`find-download-link`, aliases
`download-document` and `download`) can also save the file with
`--save-to DIR`.

### Web tools

A general web toolkit for agents, so a project can hand a subagent
web access without writing its own. All of them are full tier only:
`web_search` spends Brave or Exa quota, and a public fetcher would
make the server an open proxy.

- `web_search`: titles, URLs and snippets from Brave (`keyword`) or
  Exa (`semantic`, opt-in).
- `web_fetch`: one page's main content as markdown (readability,
  then html-to-markdown), a window of `max_length` characters from
  `start_index`. Renderer `auto` (the default) climbs a ladder and
  stops at the first rung that reads the page: the plain fetch, then
  crawl4ai's browser, then Firecrawl's. It climbs on a bot check or
  block page, a page that is only a cookie or consent notice, HTTP
  401, 403, 429 or 5xx, a failed render, or a page with almost no
  text outside its links; it stops at once on an
  address that is not public, a missing page or a file that is not
  text. A page crawl4ai rendered thin is returned rather than paid
  for again, and a `.pdf` URL is never sent to Firecrawl unasked. A
  note above the frame says which rung read the page and why. A PDF
  the plain fetch gets is read as plain text by `pdftotext`
  (poppler-utils), where the host has it, with no shell, a 30-second
  limit and 4 MB of text at most. `plain`, `crawl4ai` and
  `firecrawl` force one rung. Every browser render goes through the
  same extraction; a PDF Firecrawl reads comes from its markdown, at
  most 10 pages (`internal/tools/web_page_reading_ladder.go`).
- `answer_question_from_web_page`: the quarantined read. A separate
  model with no tools reads the page and answers one question in a
  fixed JSON shape, with quotes; grounding checks each quote against
  the page and marks it found or not found. The caller never sees
  the page. With `GROUNDING_RESEARCH_CLAUDE_CODE_OAUTH_TOKEN` set the
  reader runs in headless Claude Code on that subscription, isolated
  as the researcher is below but with no MCP server at all;
  otherwise it calls the API.
- `research_question`: the quarantined researcher. A separate agent
  (Opus 5.5 at medium effort by default; `model` and `effort` choose
  otherwise) answers one question with `search_catalogue`,
  `check_citation`, `citation_graph`, `web_search` and `web_fetch`,
  and returns a backbrief in a fixed shape. Every tool call records
  what it showed into the run's ledger; `web_fetch` in a run takes
  only URLs the run's own results produced; grounding checks each
  cited source against the ledger and each quote against that
  source's text, and marks them. It stops itself at 24 turns, 6
  minutes or $1 (list price), asking for the backbrief from what it
  has. With `GROUNDING_RESEARCH_CLAUDE_CODE_OAUTH_TOKEN` set it runs
  in headless Claude Code on that subscription instead: a fresh,
  empty home, config and working directory per run, no setting
  sources, auto-memory off, no built-in tools, and one MCP server, a
  child `grounding serve --research-session-ledger-file …` that
  serves only the researcher's tools and hands the run's ledger back
  in a file. That runner is bounded by time alone (Claude Code has
  no turn limit to set) and cannot be told to wind up. Every run is
  kept as a record (Research run records, below).

Every result is wrapped in an external-content frame: an open line
saying the content is data from the web, not instructions, and a
close line, with any delimiter-shaped line inside the content
neutralised so a page cannot forge the frame's end.

Fetches are checked against an address guard at dial time:
loopback, private, link-local (cloud metadata), carrier-grade NAT
(mesh VPNs) and other non-public ranges are refused, including after
a redirect and when a public name resolves to a private address.
Environment proxies are ignored so the guard judges the real
destination.

A browser renderer dials from its own host, where that guard does
not reach: crawl4ai and Firecrawl are handed only a URL that passes
the pre-check (`WebURLOrRefusal`), with crawl4ai's host looked up
first and its final address checked after; what the page itself
loads in crawl4ai's browser must be kept off internal addresses by
that host's firewall.

Firecrawl's daily credit cap is counted in
`firecrawl-credits-spent-today` in the state directory
(`GROUNDING_STATE_DIRECTORY`), under a lock, so the server and every
research run's child process share one count.

#### The browser renderer: crawl4ai

crawl4ai runs as a service of its own, not inside grounding.
`crawl4ai-render-service/` holds one that needs no Docker:

- `render_pages_with_crawl4ai_on_a_unix_socket.py`: a thin aiohttp
  service around crawl4ai's library, serving the subset of
  crawl4ai's server API that grounding's client speaks (`GET
  /health`, `POST /crawl` with one URL) on a Unix socket made 0660.
  One browser stays open; two pages render at once; HTML handed
  back is capped at 10 MB.
- `build-the-render-service-into-its-deploy-directory.sh`: builds
  it. A venv with `requirements-pinned.txt` and Playwright's
  Chromium, named by the requirements' hash (reused while they are
  unchanged), and a launcher as the program.
- `requirements-pinned.txt`: crawl4ai 0.9.5 and every dependency at
  an exact version.

Run it where it cannot reach loopback, private or mesh addresses,
since the pages it renders load whatever they ask for, and let only
grounding's user open its socket; then set
`CRAWL4AI_BASE_URL=unix:///path/to/its.sock`.

The packages headless Chromium needs on Debian 13, from
Playwright's table (`nativeDeps.ts`, `debian13-x64`):
`python3-venv libasound2t64 libatk-bridge2.0-0t64 libatk1.0-0t64
libatspi2.0-0t64 libcairo2 libcups2t64 libdbus-1-3 libdrm2 libgbm1
libglib2.0-0t64 libnspr4 libnss3 libpango-1.0-0 libx11-6 libxcb1
libxcomposite1 libxdamage1 libxext6 libxfixes3 libxkbcommon0
libxrandr2 libfontconfig1 libfreetype6 fonts-liberation
fonts-noto-color-emoji`.

Tests: `python3 -m unittest
crawl4ai-render-service/test_render_pages_with_crawl4ai_on_a_unix_socket.py`
(needs aiohttp; a stand-in for crawl4ai, over a real socket).

### Logs and records

All three live under the research run records directory
(`GROUNDING_RESEARCH_RUN_RECORDS_DIR`), with its privacy, retention
and backup; `off` there turns them all off.

#### Page-read log

Every page `web_fetch` or `answer_question_from_web_page` reads, by
any caller, research runs' child processes included, adds one JSON
line to `page-reads/YYYY-MM-DD.jsonl`. A line holds the time, tool,
tier, research session, URL, the renderer asked for and the one that
read it, every rung tried (outcome `read`, `thin`, `blocked`,
`stopped` or `not-tried`, the reason, milliseconds, whether the
cache answered), Firecrawl credits spent, the title, length, notes,
any error, and the first 300 characters read: enough to catch a bot
check or a page of script passed off as the page. For triage:

```bash
cd ~/.local/state/grounding/research-runs/page-reads   # or under GROUNDING_RESEARCH_RUN_RECORDS_DIR
jq -r '.read_by // "failed"' *.jsonl | sort | uniq -c   # who reads
jq -c 'select(.firecrawl_credits > 0) | {url, credits: .firecrawl_credits, why: [.rungs[].reason]}' *.jsonl
jq -c 'select(.error) | {url, error}' *.jsonl            # unread
jq -c 'select(.characters < 1500 and .read_by) | {url, read_by, beginning}' *.jsonl
```

#### Catalogue search log

Every `search_catalogue` call, by any caller (the CLI too), adds one
JSON line to `catalogue-searches/YYYY-MM-DD.jsonl`: the time, tier,
research session, query, source type, the backends named, limit and
filters, how many papers, books and articles came back and how many
had an open-access copy, the full diagnostics, the warnings, the
empty-result explanation, any error, and milliseconds. Diagnostics a
caller sees only with `debug` are always here, so use shows which
sources work:

```bash
cd ~/.local/state/grounding/research-runs/catalogue-searches   # or under GROUNDING_RESEARCH_RUN_RECORDS_DIR
jq -r '.diagnostics[]? | select(.error) | .backend' *.jsonl | sort | uniq -c   # who fails
jq -c 'select(.papers + .books + .articles == 0) | {query, why: .empty_result_explanation}' *.jsonl
jq -r '.ms' *.jsonl | sort -n | tail -5                                          # slowest
```

#### Research run records

Every `research_question` run, answered or not, leaves a directory
named for its start and session ID
(`20261029T144302Z-research-…`), holding:

- `run.json`: the question; the caller (MCP or CLI, tier, client
  address, and the name and version the MCP client gave); the
  runner, model asked for and model used, effort, grounding's and
  Claude Code's versions, both session IDs; start, end and
  duration; turns, tool calls, failed calls, sources, cost; the
  outcome (`answered`, `failed`, `timed-out`,
  `cancelled-by-caller`) and any error; findings and how many were
  supported.
- `claude-code-stream.jsonl` and `claude-code-stderr.txt`: Claude
  Code's whole `stream-json` output, every turn and tool result; or
  `api-conversation.json` on the API runner.
- `ledger.json`: every source the researcher was shown, with the
  text it saw.
- `backbrief.json`: the backbrief as the researcher wrote it.
- `returned-to-caller.txt`: what the caller was handed.

A run cut short (a caller that stopped waiting, the time limit)
keeps what it had done up to then.

**Privacy.** Records hold the question, which can be personal, and
whole pages. Directories are 0700 and files 0600, the server user's
alone; records older than the retention are deleted when the next
run is kept. A records directory inside a synced or backed-up tree
carries them wherever that tree goes, retention or not.

`grounding research-runs list`, `show RUN` (`--file NAME` prints
one of the files) and `replay RUN` (the same question again, as a
new run, optionally on another `--model` or `--effort`) read them;
RUN is a session ID, a directory name or the start of either. On a
server, run them as the service's user with its environment.

### Add-ons

This module builds the core server: free and sanctioned sources
only. An add-on is a separate Go module whose path sits under
`btr.mt/grounding/` (so it may import `internal/`), with its own
`main` that imports `btr.mt/grounding/cmd` and its own packages.
Each package registers from its `init()`, through
`internal/tools/registry-of-search-sources-citation-verifiers-and-health-probes.go`
(search sources, enrichers, citation verifiers, health probes,
availability providers), `internal/tools/find-download-link.go`
(download providers) and
`internal/tools/hooks-an-add-on-extends-core-with.go` plus
`cmd/hooks-an-add-on-extends-the-command-line-with.go` (full-tier
text, tool parameters, commands, landing-page sources, research-run
variables). Core never names an add-on's backend;
`TestCoreNamesNoBackendThatBelongsInAnAddOn` holds it to that.

### Architecture

```
grounding
├── cmd/                    # Cobra commands (CLI + MCP tool registration)
│   ├── root.go             # Global flags (--json), version stamp
│   ├── serve.go            # MCP server: stdio or http; tier filter; access log
│   ├── landing.go          # GET / on the http transport: the page around the README
│   ├── landing-page-and-readme-are-one-document.go  # this README, rendered per tier
│   ├── public-tier-call-limit.go  # Per-caller tool-call allowance for the public tier
│   ├── authorship-for-agents.go   # /llms.txt, JSON-LD and the "About the author" blurb
│   ├── check-backends.go   # check_backends
│   ├── search-catalogue.go # search_catalogue
│   ├── check-citation.go   # check_citation
│   ├── citation-graph.go   # citation_graph
│   ├── find-download-link.go  # find_download_link (full tier)
│   ├── web-search.go, web-fetch.go, answer-question-from-web-page.go
│   ├── research-question.go  # research_question (full tier)
│   └── schema.go           # Dump tool definitions as JSON
│
├── internal/
│   ├── auth/tier.go        # TierFull / TierPublic from the bearer token
│   ├── config/config.go    # Environment variables
│   ├── backends/           # API clients
│   │   ├── types.go        # Interfaces: SearchBackend, CitationBackend, WikipediaBackend
│   │   ├── http.go         # Shared client: rate limiters, 429 retry/cooldown, User-Agent
│   │   ├── cache.go        # Server-only GET cache (10 min)
│   │   ├── download.go     # Capped, containment-checked file transfer
│   │   ├── text.go         # Text normalisation
│   │   ├── crossref.go, openalex.go, semantic_scholar.go, europepmc.go
│   │   ├── openlibrary.go, opencitations.go, unpaywall.go
│   │   ├── wikipedia.go, exa.go, consensus.go
│   │
│   └── tools/              # Business logic (shared by CLI + MCP)
│       ├── tools.go        # Tool registry (ToolDef, Register, All, RequiresAuth)
│       ├── check_backends.go
│       ├── check_citation.go
│       ├── citation_graph.go
│       ├── search_catalogue.go
│       ├── registry-of-search-sources-citation-verifiers-and-health-probes.go
│       │                   # the seam an add-on registers its backends into
│       ├── availability-of-catalogue-records.go  # open-access copies, download arguments
│       ├── catalogue-search-log.go  # one JSON line a search
│       └── relevance.go    # Post-retrieval scoring and filtering
│
├── crawl4ai-render-service/  # the browser renderer's own service
└── main.go
```

Each command file in `cmd/` defines the CLI command, registers the
MCP tool in `init()`, and implements handlers that call the shared
logic in `internal/tools`, so CLI and MCP behave identically.

### Testing

```bash
go test -short ./...   # offline, httptest only
go test ./...          # also runs live smoke tests against the real APIs
```

## Licence

Copyright 2026 Dorian Minors. Licensed under the Apache License,
Version 2.0; see `LICENSE`. Crawl4AI's notice is under Credits,
above.
