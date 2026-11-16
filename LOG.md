# grounding log

Decisions, patterns, and operational knowledge worth persisting
across sessions. Append-only by convention.

This file is public, like the code. It records grounding's own
decisions in generic terms: what is particular to a deployment is
kept with that deployment's configuration, and the history of an
add-on's backend in the add-on. Commits cited from before
2026-11-16 are in the history before the public split, which is
kept in a private archive.

## Delivery and config

- Remote MCP at the public instance's `/mcp` (Streamable HTTP).
  Landing page at `/`. Health at `/health`.
- No config file---all config via env vars (`GROUNDING_EMAIL`,
  `SEMANTIC_SCHOLAR_API_KEY`, etc.).
- No Co-Authored-By or AI branding in commits.

## Rate limiting

> Superseded 2026-09-17: several facts below were wrong or are no
> longer true. See "Rethink (2026-09-17)" at the end of this file.

- Singleton `sync.Map` of `rate.Limiter` in `http.go`.
  Persists across tool calls in the long-lived MCP server
  process.
- Each backend sets its rate in its constructor via
  `SetRateLimit(name, rps, burst)`:
    - `semantic-scholar`: 1 RPS, burst 1
    - `crossref`: 10 RPS, burst 1 (polite pool)
    - `openalex`: 10 RPS, burst 1 (polite pool)
    - `wikipedia`: 5 RPS, burst 1
    - `consensus`: 1 RPS, burst 1
- `Client.Do()` calls `limiter.Wait(ctx)` before every
  request. Concurrent requests are serialised.
- **429 retry** (SS only): 1 retry with 1s backoff. Respects
  `Retry-After` header. Re-acquires rate limit token before
  retry.
- **Session-level cooldown:** when SS exhausts its 1-retry
  budget on 429, `SetCooldown("semantic-scholar", 60s)` is
  called. `Do()` checks `InCooldown()` _before_ acquiring
  rate limiter token---returns `CooldownError` immediately.
  `IsRateLimited()` matches both `*HTTPError{429}` and
  `*CooldownError`.
- **Cooldown skip:** `searchAcademicBackends` checks
  `InCooldown("semantic-scholar")` before adding SS to the
  query list. When active, SS is skipped entirely and a
  `Diagnostic` with `Skipped: true` and `SkippedReason` is
  emitted.
- Failure mode: fail noisily but gracefully.

## Semantic Scholar API

> Superseded 2026-09-17: several facts below were wrong or are no
> longer true. See "Rethink (2026-09-17)" at the end of this file.

- API key does NOT give higher rate limits. 1 RPS regardless.
- SS hits 429 after ~10--15 sequential queries, 2--3 in
  parallel.
- When SS is down, Crossref/OA carry the load. Cooldown/skip
  system handles this gracefully.

## Search blend strategy

- OpenAlex and Crossref each make up to three parallel
  requests: relevance-ordered, citation-sorted, and
  optionally a UK/US spelling variant. Results merge with a
  `2*limit` cap per backend.
- `searchAcademicBackends()` collects ALL results from all
  backends before deduping and filtering. Limit applied
  _after_ relevance filter.

## UK/US spelling variants

- Bidirectional map in `text.go` (~40 common academic terms).
  `QuerySpellingVariant()` / `SpellingVariant()`. OpenAlex
  and Crossref send variant queries. SS skips (rate limit
  budget). Academic relevance filter uses
  `containsTermOrVariant()`.

## Relevance scoring

- Absolute match counts:
  `titleMatches * 0.25 + abstractMatches * 0.10`.
- Base threshold 0.1; no-abstract papers need 0.3. Papers
  with 100+ citations and 2+ _title_ matches get threshold
  0.01. Word-boundary matching with plural 's' support.

## check_citation best-of

- Title-based verification queries all backends, returns
  highest-confidence match. DOI stays as cascade. Backends:
  Crossref, SS, OA, OpenLibrary, and an add-on's verifier when
  one is configured.

## Match confidence penalties

- 60/20/20 scheme (title similarity, year match, author
  match). `yearPenalty()` multiplicative decay for year
  gaps > 2. First-author gate caps at 0.4 if first query
  author absent. Shared helpers in `crossref.go`.

## Wikipedia bigram phrase queries

- For 3+ term queries, fires parallel CirrusSearch queries
  with quoted bigrams. Max 2 bigrams (4 extra queries).
  Results merge bigram-first. Skipped when user already
  quoted phrases.

## Diagnostics

- `Diagnostic` struct: per-backend `raw_count`,
  `filtered_count`, `error`, `skipped`, `skipped_reason`.
  Always populated.

## MCP error handling

- All MCP handlers return business logic errors via
  `mcp.NewToolResultError()`, never `return nil, err`.

## Search filters

- `SearchFilters` struct: `YearMin`, `YearMax`,
  `MinCitations`, `Domain`. Native API filters: OpenAlex
  (all), Crossref (year), SS (year). Post-retrieval fallback
  for `MinCitations`.

## Citation trend

- Year histogram from `Paper.Year` on `CitationEdge` data.
  Zero extra API calls. `Sampled` flag when capped by limit.

## Citation graph

- `sort_by` (recency|citations), `min_citations` filter. OA
  fallback per-direction on SS 429. OA preferred for
  citation-sorted queries.

## Domain filter

- OA-only native filter via `primary_topic.domain.id`. Four
  values: Social Sciences, Health Sciences, Life Sciences,
  Physical Sciences. Numeric IDs via `openalexDomainID()`.

## Consensus.app backend

- `internal/backends/consensus.go`: `SearchBackend` only (no
  `CitationBackend`---API is search-only).
- Auth: `x-api-key` header with `CONSENSUS_APP_KEY`.
- Endpoint: `GET /v1/quick_search?query=...`
- Returns: title, authors, year, DOI, abstract, journal,
  citation_count, study_type, takeaway (AI summary).
- Takeaway prepended to abstract as
  `[Consensus takeaway: ...]`.
- Year filters passed natively (`year_min`, `year_max`).
- Opt-in only: `backends: ["consensus"]`. Not in default
  sets.
- ~1 cent per API call. Key in `.env` as
  `CONSENSUS_APP_KEY`.
- `.env` not auto-loaded by binary; use `direnv` or manual
  export locally.

## Bearer token access tiers

> Superseded 2026-09-17: several facts below were wrong or are no
> longer true. See "Rethink (2026-09-17)" at the end of this file.

- `internal/auth/tier.go`: TierFull/TierPublic via context.
  `GROUNDING_AUTH_TOKEN` env. Unset = full (local dev).
- HTTP: `WithHTTPContextFunc` in `cmd/serve.go` injects
  tier. Stdio/CLI defaults full.
  `subtle.ConstantTimeCompare` for token compare. Token
  also via `?token=` query param.
- Public tier: consensus rejected, the add-on backend's source
  stripped, `check_backends` omits it and consensus, download
  gated.
- Landing page tier-aware; tool descriptions strip any
  allusion to hidden backends. LLMs discover via
  `check_backends`. Per-connection `tools/list` filtering
  is deferred (PLAN.md).

## Deployment (the first one)

- Hand-made at first: an environment file loaded by a systemd
  unit, a forge webhook that pulled, built and restarted it, and a
  reverse proxy in front. Superseded on 2026-10-03 ("Deployment
  is declared, not hand-made").

## Test queries for evaluation

```bash
# Household labour (Bianchi with --min-citations 100)
./grounding search "household labour division gender" \
  --source academic --limit 10 --json
```

## Known limitations

- Search APIs can't find papers by concept when title terms
  don't overlap. Citation graph is the correct tool.
- Dedup by DOI misses preprint/published pairs with
  different DOIs.
- Long queries with common terms dilute API relevance
  scoring. Use short 3--5 term keyword queries +
  `min_citations`.
- The add-on backend's own limitations moved to its LOG
  (2026-11-16).

## Session insights

- **Citation graph is the killer feature.** Keyword search
  fails for conceptual queries. `check_citation` →
  `citation_graph` two-step is the reliable workflow.
- **SS burns out in 1 call.** Use
  `backends: [openalex, crossref]` for search, preserve SS
  for citation_graph. _(2026-09-17: SS is no longer
  load-bearing---OpenAlex, Crossref and Europe PMC carry
  abstracts; see the rethink section.)_
- **Crossref is the main noise source** for social-science
  queries. Best suppressed by `--backends openalex`.
- **Wikipedia strong for contemporary culture** (tradwife,
  neopatriarchy---material academic search can't reach).

## AGENTS.md replaces AI.md as the canonical context file (2026-09-20)

`AI.md` is retired. `AGENTS.md` is now the real file and
`CLAUDE.md`/`GEMINI.md` symlink to it---the inverse of the previous
arrangement. The rename is deliberate: these are instructions for
agents, human and machine, not for "AI". Prose and code references
were rewritten with it, including older entries in this file, so
citations still resolve. The `## Pick-up`/`## Wrap-up` blocks now
state that they are fragments requiring the generic skill.

## Rethink (2026-09-17)

A start-to-finish review, prompted by "rate limiting is annoying" and
"how do I enable download for an AI". Everything below was measured
or verified against the providers' live docs on 2026-09-17.

Commits: 0035039 (request bodies on the shared client), 1b15b86
(rate-limiting causes), 60c048e (gate and download), 9450cfa (four
new backends), 6d7db40 (wiring), 9e87cd0 (SS-404 fallback,
toolchain, docs). Merged to master and deployed the same day.

### What was actually wrong

- **The polite-pool email never reached anyone.** `http.go` sent it
  as an HTTP header literally named `mailto`. Crossref reads
  `mailto=` as a query parameter or `mailto:` inside the
  User-Agent; probed live, the header form lands in the public pool
  (1 req/s, `x-api-pool: public-array`), the UA form in the polite
  pool (3 req/s). The old `crossrefRPS = 10` was a client-side
  ceiling ten times the real allowance, and the three concurrent
  legs of a search tripped 429s that were never retried (retry was
  Semantic Scholar only).
- **OpenAlex retired the polite pool (2026-02) and meters a daily
  credit budget instead.** Keyless: $0.10/day per IP = 1000
  credits; a list query costs 10, a single-work GET is free. A
  `fetch_context` fires two or three list queries, so a keyless
  server has roughly 35--50 searches a day for everyone behind it.
  A free key gives $1/day. Sent as `Authorization: Bearer`
  (`OPENALEX_API_KEY`). Headers `x-ratelimit-remaining` /
  `x-ratelimit-reset` tell you where you stand.
- **OpenAlex abstracts were discarded.** `abstract_inverted_index`
  was never in the struct. Measured on a five-term query: OpenAlex
  had abstracts for 6/10 results, Crossref 2/10. Parsing them made
  the add-on backend's abstract enrichment step redundant.
- **The add-on backend's enrichment cost ~54s per academic
  query**, on every call regardless of `backends`, gated only on
  its key being set; public-tier callers got it too (the add-on's
  LOG has the detail).
- **The rate limiter reset on every call.** `SetRateLimit` was a
  plain `Store`, and every backend constructor called it; the
  tools layer constructs backends per call, so each call got a
  fresh token bucket. The "persists across tool calls" comment was
  a fiction. Same root cause: a source with a login logged in
  again on every call.
- **Semantic Scholar keyless is a global shared pool**, not a
  per-client 1 req/s (product page, verified). It 429'd on the
  first call of the session. Last official word on key
  applications (Nov 2024, notes since discontinued): ~1 month
  backlog, no free-email domains, no third-party apps. Don't plan
  around a key.
- **Wikimedia introduced per-minute limits in 2026**: 10 req/min
  for unidentified clients, 200 for a User-Agent with contact
  details. Ours was `grounding-mcp/1.0 (https://github.com/btrmt/
  grounding-mcp)`: a dead URL, no email.
- The public landing page listed a full-tier book tool and named
  its backend (only `download_document` was in the hidden set).
  The production unit had no writable path, so a download to disk
  there would spend a quota slot and then fail at `MkdirAll`.

### What changed

- User-Agent is `grounding-mcp/<version> (<the public instance's
  URL>; mailto:<GROUNDING_EMAIL>)` for every request (Crossref
  polite, Wikimedia identified). No `mailto` header.
- `ensureRateLimit` (LoadOrStore) in constructors; `SetRateLimit`
  kept for tests. 429 retry-once + cooldown for every backend;
  cooldown honours `Retry-After` / `x-ratelimit-reset`, capped at
  one hour. Crossref's `x-rate-limit-limit`/`-interval` retune its
  limiter live. Response bodies capped at 16MB.
- OpenAlex: API key, `select=` of the eleven fields we read,
  abstracts reconstructed. Crossref: 1 req/s public, 3 polite.
- The add-on's backend and `consensus` are opt-in via `backends:`
  and full-tier only; nothing is stripped from public results any
  more because public results never contain the add-on's. Its
  enrichment runs only when it is requested, only over the papers
  being returned.
- **Measured after:** same query, default academic set, 5 results:
  3.7s with abstracts on 5/5 from OpenAlex and Europe PMC (was 64s
  and 4/5 via the add-on's backend). Through the deployed server
  the same call answered from cache in 0.1s.
- **Gate:** HTTP transport refuses to start without
  `GROUNDING_AUTH_TOKEN`. `ToolDef.RequiresAuth` drives both
  `tools/list` filtering (mcp-go `WithToolFilter`, sees the
  per-request tier) and the landing page. Verified through the
  real transport: public and wrong-token sessions list four tools,
  bearer sessions six.
- **Download:** DOI tries Unpaywall first (legal OA copy, exact DOI
  match, no quota, no caution), then the add-on's provider. With no
  download path set the tool returns `download_url`
  (`download_url_kind`: pdf | landing_page | file) and writes
  nothing---the shape a remote caller needs and what a read-only
  production unit can do. Destination containment-checked;
  transfers share `FetchToFile` with a 512MB cap.
- **New backends:** Europe PMC (default academic set + verification
  cascade; no key), Unpaywall (download path; email only),
  OpenCitations (third `citation_graph` fallback, metadata filled
  from OpenAlex in one DOI batch or Crossref one-by-one; 180/min,
  optional token), Exa (opt-in `source_type: "web"` or
  `backends: ["exa"]`, full-tier; $0.007 per search plus contents).
- **Server:** ten-minute in-process GET cache (server only; keyed
  by URL + headers; errors and POSTs never cached). One
  access-log line per HTTP tool call: tool, tier, outcome,
  duration. `backends.Version` stamped from `cmd.version`; build
  with `-ldflags "-X btr.mt/grounding/cmd.version=$(git describe
  --tags --always)"`. Toolchain 1.26.6 (govulncheck: 13 stdlib
  CVEs reachable at 1.24; none after).
- `check_citation` verifies titles across backends in parallel.
- Tool descriptions no longer say "call ONE AT A TIME" or treat
  Semantic Scholar as precious; that advice was compensating for
  the limiter bug.

### Decisions worth keeping

- Bearer token stays the gate. OAuth would be the literal "log in
  some special way" for claude.ai, but mcp-go has nothing
  server-side and it is ~300 lines of authorisation server for a
  one-user deployment---more infrastructure, not less. The
  `?token=` concession for claude.ai connectors stands.
- "Public tier = free backends" is no longer true: public callers
  spend the OpenAlex credit budget. With a key that is ~1000 list
  queries/day, fine for the programme's scale; the access log is
  what shows who is spending it.
- Files are not served from the public instance. A remote caller
  gets a provider's minted URL (per-file, expiring) or an Unpaywall
  link, never a standing directory of files on a student-facing
  domain.
- Wikipedia stays on the Action API for now; the UA fix is the
  load-bearing change. The REST `search/page` + `page/summary`
  migration and the too-strict `isRelevant` intro filter are in
  PLAN Deferred.
- Not built: a long-lived backend registry (the structural fix for
  per-call construction). `ensureRateLimit` and the cache remove
  the practical harm; the registry is in PLAN Deferred.

### Provider facts (verified 2026-09-17)

- Crossref: public 1/s, polite 3/s (live headers; the docs table
  says 5 and 10 with no interval). `mailto=` param or UA both work.
- OpenAlex: see above. `help.openalex.org/api/authentication`
  (updated 2026-08-19); polite-pool retirement announced on the
  openalex-users list 2026-02.
- Semantic Scholar: `x-api-key`; keyed 1 RPS; `/paper/batch` takes
  500 IDs / 10MB. Unauthenticated is a shared global pool.
- Wikimedia: `mediawiki.org/wiki/Wikimedia_APIs/Rate_limits`
  (2026-06-03); ≤3 concurrent; 429 + Retry-After; `api.wikimedia.
  org` portal shutting down through 2026--27.
- Europe PMC: no key, no published limit; `resultType=core` for
  abstracts; `PUB_YEAR:[a TO b]`, `CITED:[n TO *]`, `DOI:"…"`,
  `TITLE:"…"` all verified live.
- Unpaywall: `email=` required and must be a real address (a
  placeholder is rejected with 422); 100k/day.
- OpenCitations: 180/min per IP; `authorization: <bare token>`; a
  wrong token is a hard 403, so leave `OPENCITATIONS_TOKEN` unset
  rather than guess. `citing`/`cited` are space-separated
  identifier lists; the `count` field is a string.
- Exa: 10 QPS standard / 5 QPS deep; `highlights.numSentences` and
  `highlightsPerUrl` deprecated (use `maxCharacters`); `score` no
  longer in the reference.

## Cost signalling (2026-09-17)

Commits: 72a087b (per-tier descriptions, unforced health, cost in
diagnostics), f7fe716 (parameter-text leak fix and guard test).

dorian asked whether a model or CLI user could tell that some
backends cost. Audit: Exa was called "metered" with no figure and
only on one of its two entry points; Consensus's ~1¢/query appeared
nowhere a model reads (only the landing page); `check_backends` at
full tier ran a real Consensus query and a real Exa search on every
call and reported the cost afterwards. The structural cause was one
description for both tiers with the standing rule that public text
never names hidden backends.

- Descriptions are now per tier: `ToolDef.FullTierDescription`,
  chosen by `DescriptionForTier` in the `WithToolFilter` hook (the
  same hook that hides full-tier tools) and on the landing page.
  `fetch_context` and `check_backends` carry a costs paragraph for
  full-tier sessions and the CLI; public text is unchanged.
- `check_backends` reports Consensus and Exa as configured-but-
  unprobed unless `force` is true, mirroring the Semantic Scholar
  rule. A missing optional key is no longer "unhealthy".
- `Diagnostic` gained `cost_usd` (Exa's per-call figure) and
  `cost_note` (Consensus's standing rate), so every response shows
  the spend beside the results.

Figures used: Consensus ~1¢/query; Exa ~$0.007/search plus ~$0.002
per result with page text; OpenAlex ~20--30 credits per search of
~10,000/day with a key, shared by every caller. Verified
2026-09-17.

## Public instance for people dorian points at it (2026-09-25)

Commits: `d055ca8` (landing page, per-caller limit, annotations),
`f9e1a8d` (authorship for agents, dorian's copy). Deployed as
`v1.0.0-30-gf9e1a8d`.

The audience changed from a student programme's participants to a
handful of people dorian recommends it to (colleagues, students,
family). The CLI stays private; only the public instance's `/mcp`
is the product.

- **The landing page was dense because it showed the model's text to
  people.** Tool descriptions are 750--3,100 characters of usage
  instructions for the model, rendered raw (literal `**`) in a table.
  Fix: `ToolDef.Title` and `ToolDef.SummaryForPeople`, shown on the
  page; the model-facing descriptions and parameters moved behind a
  `<details>` ("Under the hood"). Tools are ordered by usefulness,
  not alphabetically, so the health check comes last.
- **Styling comes from txt.btr.mt** (btrmt-site
  `resources/views/text/layouts/text.blade.php`): palette, Georgia,
  dark mode, the logo SVG copied byte-for-byte from the navigation
  partial. Zero JS, no web fonts, so it stays a single Go template.
- **The page frames the server with the man-guessing essay**
  (btr.mt/analects/enigma-of-ai-reason): grounding plus friction;
  the server is the grounding, and a pasteable instruction snippet
  supplies the friction.
- **Per-caller limit on public tool calls** (burst 40, one per 90s,
  in `cmd/public-tier-call-limit.go`). Every public caller shares one
  OpenAlex budget (~300--500 searches a day in total) and the contact
  email's standing, so one looping agent could spend the day for
  everyone. Keyed on the rightmost `X-Forwarded-For` entry: Caddy has
  no `trusted_proxies`, so it discards client-supplied values and
  appends the real peer. Refusals are tool errors, not protocol
  errors, so the model can explain the wait. Full tier is exempt.
- **Annotations are explicit on every tool.** `mcp.NewTool` defaults
  to `readOnlyHint: false, destructiveHint: true` and the raw-schema
  constructor sends none, so every tool looked potentially
  destructive. Now `title`, `readOnlyHint` (all but
  `download_document`), `destructiveHint: false`, `openWorldHint:
  true`.
- **The contact email stays in the User-Agent.** dorian asked why
  it isn't anonymous: without it Crossref drops from 3 to 1 req/s,
  Wikimedia from 200 to 10 req/min, and Unpaywall refuses outright
  (and rejects placeholders). The answer is a dedicated alias, not
  anonymity (PLAN).
- **Parameter defaults were double-escaped** on the old page
  (`HTMLEscapeString` inside `html/template`); removed.
- **Authorship for AI agents** (`cmd/authorship-for-agents.go`):
  one blurb, rendered three ways---`/llms.txt` (tools from the
  registry, tier-aware like the page), schema.org JSON-LD in the
  `<head>`, and a collapsed "About the author" section on the page.
  Wording is dorian's, lifted from btrmt-site's `llms-content`
  partial and the essay's `academic:` front matter; change it there
  first, then here.

## Agent web toolkit (2026-10-03)

dorian's 2026-10-02 idea, taken up: grounding as the one set of web
tools to hand any subagent, ported in spirit from the researcher
profile of dorian's own agent harness (its web tools crate).

- **Three tools, all full tier only.** `web_search` (Brave keyword,
  Exa semantic opt-in), `web_fetch` (main content as markdown, paged)
  and `answer_question_from_web_page` (the quarantined read). Gated
  because search spends quota per call and a public fetcher would
  make the server an open proxy: scraping and abuse traced to its
  address, and a blocklisting that would also cost the polite-pool
  standing
  the academic backends rely on. Opening plain `web_fetch` to the
  public later is one flag; reconsider after watching full-tier use.
- **The frame is a port, with one word changed.** The harness's
  `external_content_framing.rs` (open line, close line, delimiter
  neutralisation, a notice inside the frame) in
  `internal/tools/external_content_frame.go`. "From outside the
  harness" became "from the web": grounding is not a harness, and
  byte-identical lines would buy nothing, since a harness framing
  grounding's result neutralises any frame inside it anyway.
- **The harness's own verdict: prose is the weakest layer.** Its PLAN
  ("The trifecta"): no single context should hold private data,
  untrusted content and a channel out, and telling a model not to
  trust content is the weakest defence. That compartment lives in
  the caller's harness; a tool server can only mark the boundary
  (the frame) and offer a read that keeps the page away from the
  caller (the quarantined reader).
- **SSRF guard, which the harness does not have.** Its fetch runs
  where nothing private is reachable; grounding runs on a server
  beside loopback services, a private mesh (inside carrier-grade
  NAT, which `netip` does not count as private) and possibly a
  metadata address. The check is a `net.Dialer.Control`
  on the resolved address, so redirects and DNS rebinding are judged
  by what is actually dialled; environment proxies are disabled so
  the guard sees the real destination. A URL pre-check refuses
  obvious names early and is the only check Firecrawl's renderer
  gets (Firecrawl dials from its own servers). Verified live
  2026-10-03: a `nip.io` name (public DNS to a mesh address) passed
  the pre-check and was refused at dial time.
- **Libraries.** `codeberg.org/readeck/go-readability/v2` for main
  content (go-shiori/go-readability is archived and its module
  marked deprecated in favour of readeck's fork) and
  `JohannesKaufmann/html-to-markdown/v2` for conversion. When
  readability finds under 200 characters the whole page is
  converted, with a note saying so.
- **Links are resolved before conversion, and lose their titles.**
  html-to-markdown's `WithDomain` resolves against scheme and host
  only, so a Wikipedia footnote `#cite_note-1` came out as
  `https://en.wikipedia.org#cite_note-1` and `../b` landed at the
  root. Now a walk over the node tree
  (`resolveLinksAgainstPageURLAndDropTitles`, both the readability
  and the whole-page path) resolves every `a[href]` and `img[src]`
  against the final URL and drops `title` attributes, which on
  Wikipedia repeat the link text on nearly every link and cost the
  caller characters. `javascript:` links lose their href (bare
  text); mailto: stays; an unparseable address is left as written.
- **Firecrawl renders; grounding extracts.** Live on 2026-10-03,
  Firecrawl's own markdown of a PMC article
  (`pmc.ncbi.nlm.nih.gov/articles/PMC11270031/`) was mostly the .gov
  banner, "Skip to main content", icons and menus, despite
  `onlyMainContent`. The scrape now asks for `rawHtml` (the rendered
  page unmodified; `html` is Firecrawl's cleaned copy without
  `<head>`) and runs it through the same readability and
  html-to-markdown as the plain renderer, so both renderers give the
  same kind of output, the whole-page note included.
  `onlyMainContent` is off. Firecrawl's markdown is still requested,
  and read only for a PDF (Firecrawl parses those to markdown) or an
  answer with no HTML. Not yet re-checked live on the PMC page.
- **The reader's key must be scoped to a workspace.** The first
  `ANTHROPIC_API_KEY` in production was an organisation-level key,
  and the
  API answered 400: "This API key is not scoped to a workspace, so
  this request must include the anthropic-workspace-id header".
  dorian swapped in a workspace key (2026-10-03) rather than grounding
  sending that header. The reader now passes the API's own message
  through on any 4xx/5xx it does not name, which is how this was
  found.
- **Search dates are the page's.** Brave's `page_age` and Exa's
  `publishedDate` date the page, not the work on it (Sweller's 1988
  paper came back dated 2010, its Wiley page), so results say
  "Page dated".
- **Paging follows the harness and the reference MCP fetch server:**
  `max_length` (default 10,000, clamped 500--40,000) from
  `start_index`, the next index said outside the frame. Fetched
  pages and Firecrawl renders go through the server's response
  cache, so paging fetches once.
- **The quarantined reader is Claude Haiku 4.5** through
  `anthropic-sdk-go` (the official SDK, unlike grounding's other
  backends, which are raw HTTP: the Claude API guidance is to use
  the SDK where one exists). No tools; structured output
  (`output_config.format`, a JSON schema: status, answer, quotes,
  text addressed to an AI); the page between delimiters carrying a
  random code, so it cannot close its own block. Each quote is
  checked against the page after markdown, typographic quotes,
  dashes, case and whitespace are normalised, with ellipses
  splitting a quote into parts that must appear in order. The
  reader's answer is rendered with `> ` on every line so it cannot
  forge grounding's `[found on page]` marks. This is the dual-LLM
  pattern (Willison 2023) and how Claude Code's WebFetch already
  works; its limit, stated in the tool's description, is that the
  reader can still be steered, so its answer is framed as untrusted
  too. The quote check is the only non-probabilistic part.
- **Prompts drafted from research** (2026-10-03 subagent): Claude
  Code's WebFetch and its web-reading-specialist prompt (as
  extracted in Piebald-AI/claude-code-system-prompts), the reference
  MCP fetch server, Exa's and Firecrawl's MCP descriptions,
  Microsoft's spotlighting post, Willison on dual-LLM and CaMeL. The
  three tool descriptions and the reader's system prompt were
  reviewed and approved by dorian on 2026-10-03, with the reader's
  limits as drafted: quotes of at most 300 characters, five at most,
  and the first 200,000 characters of a page.
- **Never probed.** `check_backends` reports Brave, Firecrawl and the
  reader as configured or not, never probing, `force` or not: every
  probe would be billed.
- **Keys** (`BRAVE_API_KEY`, `FIRECRAWL_API_KEY`, `ANTHROPIC_API_KEY`)
  are sealed by the deployment, not placed by hand: see "Deployment
  is declared, not hand-made" below. The operator's older Brave key
  is on a grandfathered free plan; a new key may be metered.

## Deployment is declared, not hand-made (2026-10-03)

Supersedes the hand-made deploy described under "Deployment (the
first one)".

- In production, grounding runs as a declared service and reads
  only a sealed environment file; the hand-made environment file
  was deleted on 2026-10-03.
- The sealed file holds every key grounding reads, from the
  operator's password store, the Anthropic key scoped to a
  workspace. The deployment's configuration records where each key
  came from.
- To change a key: reseal it in the deployment's configuration and
  propose the change. Never edit the file on the server.
- Tested by the session that sealed it: all five paid backends
  through the full tier, on 2026-10-03.

## Archived from PLAN (2026-10-03)

PLAN.md was normalised on 2026-10-03: done items left it, and what
they decided that no earlier entry here records is kept below.
Everything else they said is in the sections above (or superseded
by "Rethink (2026-09-17)"); the full texts are in git history
before that commit.

### Result quality: normalisation and dedup

- **Crossref JATS** (`72f03c0`): abstracts carried `<jats:p>`,
  `<jats:italic>` and the like into CLI and MCP output. A tag regex
  in `text.go` strips them and collapses whitespace.
- **DOIs are case-insensitive** (`72f03c0`): `normalizeDOI`
  lowercases, and so do `dedupeKey` and each backend's `seen` map.
  Torr & Short 2004 had appeared twice (`10.1111/J.1728-…` beside
  `10.1111/j.1728-…`).
- **APA double-slash DOIs** (`3a57a7c`): `check_citation` returned
  `10.1037//0022-3514.52.3.511` verbatim and Semantic Scholar 404'd
  it as a graph seed. `normalizeDOI` (and the seed path in
  `citation_graph.go`) collapse `//` after the prefix is stripped.
- **Title dedup** (`3a57a7c`): `normTitle` (punctuation, case,
  whitespace) keys papers without a DOI, and `dedupeTitleVariants`
  groups by it after the DOI pass, keeping the richest metadata.
  Caught encoding variants ("Rites de passage, rites d'initiation"
  with `â€™`), OECD series with one title and many DOIs
  ("Employment rates: women" five times). `dedupeBookChapters`
  (`20e33c2`) collapses papers sharing a Journal and a common title
  prefix of 20+ characters ("Masculinity Besieged?" had appeared as
  four chapters). Preprint/published pairs differ in title too and
  still get through (PLAN Observations).
- **The 2026-03-04 SS-dependency changes** (`20e33c2`: no-abstract
  threshold, title-only citation floor, word-boundary matching,
  chapter dedup) cut STEM papers in no-SS results from 26 to 4
  over five test queries. The remainder were real whole-word
  matches in another sense ("second", "class", "initiation").
- **Domain on `Paper`** (`f66b6ad`) came first, exposing the
  OpenAlex primary topic's domain so the model could discard
  strays; the native filter (`c36034d`, "Domain filter" above)
  followed. Session 5 found the filter alone made results worse
  (it removed OpenAlex's good matches while Crossref's noise passed
  through), hence the prompt to pair it with
  `backends: ["openalex"]`.
- `ResultSummary` on `ContextResult` gives the year range and top
  cited papers first in the JSON, for characterising a literature
  without a separate synthesis tool. A stub synthesis mode was
  removed in `b820766` (revisit only with browser automation).
- Wikipedia relevance (`de70bef`): ~80 stopwords, acronyms kept
  (AI, ML, UK), proportional matching (2 of n terms, a title match
  lowers the bar), and the `(software)` exclusion dropped.

### Semantic Scholar, fallbacks and errors

- **One retry, not three** (`72f03c0`): three retries with 1+2+4s
  backoff spent 7s on a 429 before cooldown began, plus three
  requests sure to 429. Semantic Scholar's 429s were never
  transient, so `maxRetries = 1`.
- **OpenAlex first for citation-sorted graphs** (`72f03c0`): SS
  returns citers in recency order only, so `sort_by: "citations"`
  re-sorted a batch of the newest papers ("Doing Gender" gave ten
  2026 papers; `min_citations 500` gave none), while OpenAlex sorts
  server-side. The fallback had been better than the primary path.
  `sortEdgesByCitations` stays as a safety net; references have no
  server-side sort on either backend.
- **`errors.As`, not a type assertion** (`b672c5d`):
  `IsRateLimited` and `IsNotFound` missed errors wrapped by
  `fmt.Errorf`, which stopped the OpenAlex fallback from firing.
- **Empty results keep their diagnostics** (2026-03):
  `FetchContext` returned `nil, err` when every backend came back
  empty and one had errored, and the handler turned that into a
  framework error with no body. It now always returns the result,
  with backend errors in the per-backend diagnostics.

### check_citation and books

- **OpenLibrary for books** (`6b87861`): academic databases return
  reviews and catalogue entries for books (Buller's _Adapting
  Minds_ matched a Choice review at 0.51; via OpenLibrary, 1.0).
  The first-author gate had been blamed; it was working, and the
  real cause was book metadata. The same commit fixed Crossref's
  `authorSimilarity`, which could count one query author several
  times (its `break` left only the inner loop).
- **"Not found" means investigate** (`6927f12`): PLAN's own test
  case, Henderson's _Luxury Beliefs_ (2024), does not exist (his
  memoir is _Troubled_; "luxury beliefs" is from his essays). The
  proposed fix, telling the model to trust its own knowledge, would
  have reinforced exactly that hallucination. So the description
  calls "not found" ambiguous (wrong title, wrong author,
  hallucination, rarely the tool) and says to check by web search,
  and `VerificationResult.Suggestion` says so on every miss. Never
  tell the model to trust itself over the tool. OpenLibrary
  verified all four test trade books at 1.0, so Google Books was
  not needed:

  ```bash
  ./grounding verify --title "Troubled" \
    --authors "Henderson, Rob" --year 2024 --json
  ./grounding verify --title "Men Are from Mars, Women Are from Venus" \
    --authors "Gray, John" --year 1992 --json
  ./grounding verify --title "12 Rules for Life" \
    --authors "Peterson, Jordan" --year 2018 --json
  ./grounding verify --title "The Righteous Mind" \
    --authors "Haidt, Jonathan" --year 2012 --json
  ```

### Prompting lessons (2026-03)

Chunks 4, 9 and 12 (`f2180b9`, `70ae473`, `00ffdad`); still in the
descriptions, apart from the Semantic Scholar budget advice the
rethink removed.

- An author surname is the strongest precision lever, so it comes
  first in the `query` description: "Stoet Geary gender equality
  paradox" finds the paper, "gender equality paradox STEM" finds
  stem cells; "Hochschild second shift" finds the book, "second
  shift women employment" finds PLINK.
- Verify, then graph: get the DOI from `check_citation` and seed
  `citation_graph` with it, because title-based seed resolution is
  unreliable when Semantic Scholar is down.
- Canonical works: find any paper in the area, then
  `citation_graph` with `direction: "citations"`,
  `sort_by: "citations"`, `min_citations: 200`; best from seeds
  with 500+ citations.
- Each parameter says when to change its default and what it
  costs (the trade-off guidance for weaker models).

### v1.0 and the student framing

- `v1.0.0` was tagged after chunk 15 (`1c24cdf`): OpenLibrary on the
  landing page, a "How this works" section for the student
  programme's participants,
  the `/health` endpoint, `GROUNDING_EMAIL` set server-side.
  Consensus (chunk 14) was built as "Consensus.app backend" above
  once its API (`quick_search`) turned out to exist.
- The tools were mapped onto the programme's method: "Is this real?" is
  `check_citation`; "What does the evidence say?" is
  `fetch_context`; "What's the intellectual lineage?" is
  `citation_graph`; "Give me context" is `fetch_context` with
  Wikipedia. The audience widened on 2026-09-25 (see "Public
  instance").
- The 2026-07-12 security sweep (`db7d44e`): five items, all closed
  by the rethink (MD5 validated and path containment, body caps,
  HTTP fails closed, the `?token=` trade-off recorded, govulncheck).
  Its premise, "no caller-controlled URL fetch, so SSRF is
  negligible", stopped holding with `web_fetch` (2026-10-03), which
  has its own guard ("Agent web toolkit").

### Evaluation sessions 1--5 (2026-03)

Five sessions against dorian's essay territory (gender roles,
masculinity, household labour, evolutionary psychology, attachment,
luxury beliefs, tradwives), 15--25 queries each; the open findings
are in PLAN Observations. What held across them:

- `check_citation` was excellent throughout; `citation_graph` by
  citations was the strongest discovery path once OpenAlex served
  it ("Doing Gender" with `min_citations 500`: Acker, Addis &
  Mahalik, Ridgeway, England, Risman, Connell).
- Single-backend `--backends openalex` avoided Crossref's polysemy
  ("emotional labour": five relevant results, no noise).
- Wikipedia is strong for contemporary culture and on precise
  keywords ("emotional labour Hochschild": five right articles)
  and weak where a common word links elsewhere ("masculinity
  crisis" gave the Cuban Missile Crisis).
- Colloquial concepts ("weaponised incompetence") have no academic
  footprint; no tool change finds them.

Session 5 scorecard (2026-03-05, after chunks 1--9): verify
excellent; graph by citations excellent; graph by recency good;
recent niche work good; canonical works good via the graph (fair by
keyword); book verification excellent; book discovery by search
fail; Wikipedia excellent; diagnostics excellent; relevance with SS
good, without SS fair; cross-discipline noise fair; title dedup poor
(fixed the same day, `3a57a7c`).

Test queries that still show these behaviours:

```bash
# OpenAlex citation sort on the graph
./grounding graph "10.1177/0891243287001002002" \
  --direction citations --limit 20 \
  --sort-by citations --min-citations 500 --json
# Double-slash DOI as seed
./grounding graph "10.1037//0022-3514.52.3.511" \
  --direction citations --limit 5 --sort-by citations --json \
  | jq '.seed.title'
# Domain filter, with and without (pair with --backends openalex)
./grounding search "second shift women employment" \
  --source academic --min-citations 100 --limit 10 \
  --domain "Social Sciences" --json
# Title dedup
./grounding search "rites passage initiation" \
  --source academic --limit 10 --json | jq '[.papers[] | .title[:60]]'
./grounding search "women employment rates OECD" \
  --source academic --limit 10 --json | jq '[.papers[] | .title[:60]]'
# JATS: expect 0
./grounding search "Doing Gender" --backends crossref \
  --limit 1 2>&1 | grep -c "jats:"
```

## Overnight follow-ups (2026-10-03)

Done while dorian was away, on his "do what you can without getting
me out of bed".

- **The deploy hook now fires on master only.** The then hand-made
  webhook's trigger rule ANDs its HMAC match with a match of
  `refs/heads/master` on the payload's `ref`. Verified: a throwaway
  branch push left the version alone; the next master push
  (`3bb93ec`) deployed. (The hook has since been replaced by the
  deployment's own push-to-deploy, which keeps the rule.)
- **Heading anchors.** An `a` with no href (PubMed Central's bare
  heading anchors, or a javascript: link once its href is dropped)
  is unwrapped to its text before conversion: `## []()Abstract`
  became `## Abstract` (live, `3bb93ec`).
- **Live checks finished.** Firecrawl read an arXiv PDF cleanly
  (title, authors, abstract). The reader, asked about Unit 42's
  2026-03 survey of injections in the wild, answered with 5 of 5
  quotes found and reported the page's two notices addressed to AI
  agents instead of acting on them.
- **Branches.** `worktree-grounding-costs` and
  `worktree-grounding-rethink` were deleted from origin: every commit
  in them was already in master (`git cherry` empty). Their deletion
  pushes did not redeploy, a second check of the filter.
  `release-binaries-script` holds one unmerged commit and is left
  for dorian (PLAN).
- **PLAN.md normalised** (dorian's own item): open items only, from
  2,004 lines to about 270; done work's decisions archived above in
  "Archived from PLAN (2026-10-03)". Spaced em-dashes in PLAN.md
  closed up to dorian's style.

## Funded deliverable: split, evals, embedded agent (2026-10-25)

Commits: `719475e` (the plan), `aa97c29` (licence, Haiku 5.5 reader,
research ledger and verifier), `ffc4f0a` (evals made their own chunk).

dorian has work funding for AI projects and may make grounding a
deliverable. Planned as four chunks (PLAN "Funded deliverable");
the decisions behind them:

- **Split by module, not by build tag.** A `//go:build` tag leaves
  the grey backend's source in the tree that is handed over: clean
  for a binary, not for source. Core (`btr.mt/grounding`) carries
  nothing grey; a private add-on module imports it and registers
  its backend at startup. The rule for what counts as grey: core
  uses only sanctioned APIs within their terms.
- **The deliverable is the code at a release tag, not the repo.**
  dorian copies the tree at the tag; the history, which holds the
  grey backend from 2026-03, stays with him. (Since 2026-11-16 the
  repository itself is public, its history begun afresh: "The code
  repository made public".)
- **Evals in two layers.** Retrieval quality is deterministic and
  needs no model (Go tests, recorded and live). Whether a model can
  use the tools needs a model: UK AISI's Inspect, which attaches
  MCP servers over stdio (`mcp_server_stdio()`) and HTTP
  (`mcp_server_http()`) per its docs
  ([tools-mcp](https://inspect.aisi.org.uk/tools-mcp.html), read
  2026-10-25), so others can run their own models through it. Its
  provider list was not checked.
- **Why Claude Code at all, and for whom.** The Agent SDK overview
  ([code.claude.com](https://code.claude.com/docs/en/agent-sdk/overview),
  read 2026-10-25): "Unless previously approved, Anthropic does not
  allow third party developers to offer claude.ai login or rate
  limits for their products, including agents built on the Claude
  Agent SDK. Use the API key authentication methods […] instead."
  So headless Claude Code is the sanctioned route for dorian's own
  subscription, and only his; anyone else brings an API key, and
  with a key the Messages API needs no Claude Code (Commercial
  Terms). Wrapping Claude Code in a subprocess does not make
  offering it to others allowed.
- **Headless Claude Code loads the operator's setup by default.**
  `~/.claude`, the global CLAUDE.md (network map, addresses),
  skills, memory and MCP servers, which would put private data
  beside untrusted pages and a fetch channel: the insulation the
  agent exists for, broken. The runner must run bare and be tested
  for it.
- **The insulation protects the caller's context, not its
  question.** The sub-agent holds the question, untrusted content
  and a fetch, so a page can ask it to leak the question in a URL.
  Fetch only URLs from search results or known APIs; tell callers
  the question is public.
- **The page reader moved to Haiku 5.5** (`claude-haiku-5-5`,
  2026-10-25; supersedes "The quarantined reader is Claude Haiku
  4.5" under "Agent web toolkit"). A tenth of 4.5's price ($0.10 and
  $0.50 per million tokens for prompts up to 100K). It thinks by
  default and thinking counts toward `max_tokens`, so the cap went
  from 4,096 to 16,000 and effort is set to low; it takes no
  sampling parameters and no prefill, neither of which the reader
  used. Offline tests pass; the live check waits on dorian's gpg.
- **Licence: Apache-2.0** (2026-10-25), added before any funded
  work so that a version dorian owns is on record.

## Claude Code isolation check (2026-10-25)

Recorded in `aa97c29`; made an automated test in `f8ab6d5`.

Step 0 of the research agent (PLAN "Funded deliverable"), run
locally with Claude Code 2.1.310, Sonnet 5.5, and a subscription
token in `CLAUDE_CODE_OAUTH_TOKEN`, with
`ANTHROPIC_API_KEY` unset. Fresh empty `HOME` and
`CLAUDE_CONFIG_DIR`, an empty working directory.

- **Without the token, "Not logged in".** The keychain login does
  not follow a fresh config directory, so the runner must pass a
  token.
- **Three canaries, none reached the model.** A CLAUDE.md naming a
  canary word, with "always mention it", in the fake config
  directory, in the fake home's `.claude`, and in the working
  directory (as a project CLAUDE.md). Asked to quote any CLAUDE.md
  or canary, the model reported none, and it did not know facts
  that only dorian's real global CLAUDE.md holds. The only trace of
  him was the home-directory path of the scratch directory; on the
  server the scratch path should name nothing.
- **Auto-memory survives `--setting-sources ""`.** The system prompt
  still described a file-based memory under the fake config
  directory (harmless with no tools, but not clean).
  `--settings '{"autoMemoryEnabled":false}'` removed it: `--settings`
  applies even when no setting sources load.
- **Tools: grounding's and nothing else.** With `--tools ""`,
  `--strict-mcp-config` and grounding as the only server, the model
  listed `StructuredOutput` and seven `mcp__grounding__*` tools.
  `download_document` and the add-on's book search were absent
  only because the child had no add-on key; `--research-session`
  must hide them, `check_backends` and
  `answer_question_from_web_page` explicitly.
- **`--json-schema` works.** The result carries `structured_output`
  matching the schema; `check_citation` on Hochschild 1983 came back
  verified (OpenLibrary, confidence 1) in three turns.
- **The child grounding got an empty environment**
  (`"env":{}` in the MCP config), so it had no backend keys. The
  runner must pass the backend keys through and nothing else.
- **One run printed nothing on stdout** and was not reproduced (the
  identical rerun succeeded). The runner must capture stderr and
  treat empty output as a failure with that stderr attached.
- `total_cost_usd` is reported (about 2 cents) even on the
  subscription; it is a list-price estimate, not a bill.

## research_question: the API runner (2026-10-25)

Commits: `4f74549` (the runner), `a3a883a` (server-side refusal
fallback, full-tier tool text), `37df873` (description audit),
`b87c642` (only-this-tool hand run), `770d14c` (refusal retry on Opus
5.5), `2b11adb` (URLs in the question).

Step 2 of the research agent (PLAN "Funded deliverable").

- **The ledger rides in the context.** `tools.WithResearchLedger`;
  `FetchContext`, `VerifyCitation` and `FetchCitationGraph` are now
  thin wrappers that record their results when a ledger is present
  (the bodies are `…Unrecorded`), `searchWeb` records its results,
  and `fetchWebPage` checks `URLMayBeFetched` first and records the
  page after. So the restriction and the record hold however the
  tools are called: in-process by this runner, or by a child
  `grounding serve --research-session` for Claude Code (step 3).
- **The runner calls the MCP handlers,** so the researcher sees
  exactly what an MCP caller sees, and is shown the tools' public
  descriptions, which name no paid backend, so it does not opt into
  Consensus, Exa or an add-on's backend. That is a soft guard: a model
  that guessed a backend's name could still ask for it.
- **A hand-written loop, not the SDK's tool runner,** because it must
  stop on turns (24), wall time (6 minutes) and list-price spend ($1,
  for models with a known price), and then ask once more, with
  `tool_choice: none`, for the backbrief from what it has. It tells
  the researcher to finish at the last turn, at 85% of the spend and
  at three quarters of the time. Parallel tool calls run at once.
  Prompt caching is on (top-level `cache_control`).
- **No refusal fallback.** A refusal ends the run with an error. The
  API's server-side fallback (`fallbacks: "default"`, a beta) would
  re-serve it on another model; not added, to keep the first runner
  plain.
- **First live run** (locally, Sonnet 5.5, medium; Brave and OpenAlex
  keys from a command, `GROUNDING_EMAIL` unset): "Does emotional labour
  predict burnout in nurses?" 3 turns, 3 tool calls, 24 sources, 29
  seconds, about $0.10; 9 of 9 findings supported, each quote found
  in its source. It answered from abstracts and fetched no page; its
  gaps were honest (no nurse-specific meta-analysis found, full texts
  not read, mostly cross-sectional evidence).
- **Refusal fallback, server-side, and its limit** (later the same
  day). Requests for Sonnet 5.5, Opus 5.5, Opus 5 and Fable 5.1 carry
  `fallbacks: "default"` with the `server-side-fallback-2026-07-01`
  beta header, set on the request because the SDK's stable Messages
  types have no such field; live run accepted it. For Sonnet 5.5 it
  re-runs only cyber and frontier-LLM declines (on Sonnet 5); bio
  and general-harms declines still come back as refusals, and those
  are the likelier ones for security and warfare research. A refusal
  now names its category. dorian wants a hand-rolled retry beside it
  (PLAN).
- **The researcher sees the full-tier tool text** (reversing the
  first cut): whether a backend's key is set is what decides if it
  may be used (dorian), so it should know Consensus is there. Its
  brief now carries the map of which grounding suits which need,
  the same map `research_question`'s description gives the caller.
- **"Supported" is not "reliable".** A live run on mission command
  in British doctrine marked a Fandom wiki and a forum post
  supported: the quotes were real. The description now says so, and
  the brief asks the researcher to prefer authoritative sources and
  say when it could not.
- **Only-this-tool, by hand** (Claude Code headless, Sonnet 5.5,
  system prompt "You are a helpful assistant.", `research_question`
  the only tool; after the description rewrite). "Search the
  internet for what the 2025 Strategic Defence Review recommended
  about drones" and "Look up who coined the term 'OODA loop'" both
  called it, the second choosing `effort: low` itself (the
  researcher then used only Wikipedia, and said so). "Is it true
  that people only use 10% of their brains?" did not: the agent
  answered from recall, correctly but ungrounded. A model sure of
  the answer does not reach for a grounding tool unless told to;
  the landing page's custom-instructions snippet is aimed at exactly
  this.

## research_question: the Claude Code runner (2026-10-25)

Commits: `f8ab6d5` (the runner, research-session serve mode, failed
calls reported), `bdaaa69` (no self-update during a run), `cc70799`
(cost note follows the runner; live in production). The deployment
change was signed 2026-10-26.

Step 3 of the research agent (PLAN "Funded deliverable").

- **Chosen by configuration, not by the caller.** With
  `GROUNDING_RESEARCH_CLAUDE_CODE_OAUTH_TOKEN` set, every run goes
  to headless Claude Code on that subscription; otherwise to the API
  runner. Which account pays is the operator's decision, so it is
  not a tool parameter. The variable has grounding's own name, not
  `CLAUDE_CODE_OAUTH_TOKEN`, so the server's process never holds a
  variable Claude Code itself acts on.
- **Claude Code's environment is built from nothing:** `PATH`,
  `LANG`, a fresh `HOME`, `CLAUDE_CONFIG_DIR` and `TMPDIR` inside the
  run's scratch directory, and the token. Nothing is inherited, so
  `ANTHROPIC_API_KEY` (which would make it bill the API) and
  `GROUNDING_AUTH_TOKEN` never reach it. Backend keys pass through
  renamed (`GROUNDING_RESEARCH_CHILD_…`), and the MCP config maps them
  back with `${…}` references, so their values never touch the disk.
- **The question goes on stdin.** `--tools` and `--allowedTools` take
  several values and would swallow a positional prompt.
- **The child is `grounding serve --research-session-ledger-file F
  --research-session-id ID`:** stdio only, the five researcher tools
  only, one ledger in every call's context, the file rewritten after
  each call (through a temporary file and a rename). The runner
  rebuilds the ledger from the file and verifies against it.
- **No turn limit and no wind-up.** Claude Code's help lists no
  `--max-turns`; the run is bounded by the six-minute timeout alone,
  and a run cut off there returns an error, not a partial backbrief.
- **Failed tool calls are now reported** in the header, for both
  runners: the ledger records each failure's first line. Added to see
  inside a Claude Code run, where tool results never reach the
  runner.
- **Tests.** A stand-in for Claude Code (the test binary itself,
  told its mode by argument, since the runner gives Claude Code none
  of the test's environment) checks the flags, the built environment
  (no Anthropic key, auth token or raw backend key; fresh home and
  config; the run's own working directory), stdin, that the MCP
  config references keys rather than holding them, that the scratch
  directory is removed, and the failure paths. A live test, skipped
  without `GROUNDING_TEST_CLAUDE_CODE_OAUTH_TOKEN`, plants four
  canaries where a user's setup would be found (a CLAUDE.md in
  `~/.claude`, in `CLAUDE_CONFIG_DIR` and in the working directory,
  and a settings env var); none reached the researcher, twice.
- **Live from the CLI** (locally, subscription token, Sonnet 5.5):
  the Haldane principle, 18 seconds, 3 of 3 findings checked against
  the child's ledger; with no backend keys at all, Wikipedia still
  served (6 to 8 seconds).
- **Live in production** (2026-10-26, after dorian signed the
  deployment change). The pinned Claude Code 2.1.307 was installed
  root-owned about two minutes after signing; grounding restarted
  with `GROUNDING_RESEARCH_CLAUDE_BINARY` set. A run from a local
  client through the full-tier MCP connection ("the say-do gap in
  leadership research, and its effect on follower trust"): 4 turns,
  2 tool calls, 16 sources, 17 seconds, "on a Claude subscription";
  4 of 4 findings supported, mapped honestly to Simons' behavioural
  integrity with its gaps named. The service's own hardening
  (`ProtectSystem=strict`, `ProtectHome=true`, `PrivateTmp=true`,
  `NoNewPrivileges`) let Claude Code run from its private /tmp.
- **The cost note follows the runner.** The full-tier description's
  cost paragraph is chosen at start from the configuration: with the
  subscription token it says runs are not charged but spend the
  operator's usage limits, so not to run it speculatively.
- **Hand-rolled refusal retry** (2026-10-26). When a turn comes back
  declined (the server-side fallback having not rescued it), the
  refused reply is left out of the history and the same turn is
  re-issued on Opus 5.5 (`ResearchQuestionRefusalRetryModel`), which
  runs no general-harms classifier and, on the Claude API, reads
  Sonnet 5.5's thinking blocks; the run stays there. One retry per
  run; a decline on Opus 5.5 ends it with both reasons named. The
  header says which turn was declined and why. Each turn is now
  priced for the model it asked for. API runner only: Claude Code's
  `--fallback-model` covers overload, not refusals, so the Claude
  Code runner still ends on a refusal.
- **First live run that fetched a page** (production, subscription,
  2026-10-26): "What indirect prompt injection techniques did Unit
  42 report observing in the wild in 2026? Read the report itself."
  6 turns, 3 tool calls, 29 seconds; it searched, fetched the report
  URL its search returned
  (`unit42.paloaltonetworks.com/ai-agent-prompt-injection/`), and
  gave 7 of 7 findings supported, each quote found in the fetched
  text, with honest gaps (the 22 techniques are never listed as one
  set; a 19.8% against 18.6% discrepancy inside the report). The
  page's "Directive for AI agents" and its "we do not recommend
  ingesting this page" note came back under text addressed to an AI.
  Those notices are defensive, so this tested reporting, not
  resistance: no page has yet tried to make the researcher do
  something, and the fetch restriction has refused nothing live.
- **URLs in the question are fetchable** (dorian, 2026-10-26). The
  caller is trusted with its own question, so a URL written in it
  cannot leak anything the caller did not send; `web_fetch` takes it
  (`ResearchLedger.AllowFetching`, from `URLsInText`), without it
  becoming a source until fetched, and still refuses it with a query
  added. The Claude Code runner hands the URLs to the child as
  repeated `--research-session-fetchable-url` flags. Checked live:
  a question naming Wikipedia's "Mission command" page fetched it in
  7 seconds. Before this, "what does this page say" was refused.
- **An MCP client gave up on a long run.** `research_question` at
  high effort, called from a Claude Code session over MCP to the
  server, ended in "The operation timed out" on the client's side.
  The same question ran to completion from the CLI. (Corrected
  2026-10-29: the run on the server did not go on. The client's
  dropped
  request cancelled its context and that killed the run; progress
  notifications would not have kept the client waiting. See
  "research_question on Opus, and its runs kept".)

## Prompt injection test suites (2026-10-26)

Commit: `74f78d0`.

Researched by `research_question` itself (CLI, subscription, high
effort; 42 turns, 40 tool calls, 88 sources, 97 seconds, about
$0.53 at list price, 15 of 15 findings supported). The run's header
showed the fetch restriction refusing two links the researcher had
found on pages, live for the first time; it searched instead. Also:
a Claude Code run has no turn limit, and this one took 42.

- **AgentDojo** (ETH Zurich, Invariant Labs): extensible, stateful
  tool-calling environment, 97 tasks and 629 security cases, `pip
  install agentdojo`, MIT, custom task suites. Injects into simulated
  tool outputs. The candidate harness for the evals chunk's injection half.
- **InjecAgent** (UIUC): 1,054 single-turn cases, simulated tool
  responses; licence seen only second-hand as MIT. One 2026 preprint
  (not peer reviewed) reports 0% attack success on Claude models.
- **BIPIA** (Microsoft): text-level indirect injection for LLM apps,
  not agents; MIT.
- **WASP** (Meta FAIR): real injected pages in a self-hosted
  WebArena; 4 to 6 hours a run; CC-BY-NC.
- **LLMail-Inject** (Microsoft and others): 208,095 human-written
  attack prompts from a closed challenge; MIT; a payload corpus.
- **IPI-proxy** (VulcanLab, 2026 preprint): a Target Server of fake
  pages with planted payloads, or a mitmproxy rewriting real HTML;
  820 payloads drawn from the others; an exfiltration-callback
  tracker. The closest fit for testing a fetch tool, with the caveat
  that grounding fetches only public addresses and ignores proxies.
- **MCP-specific:** MCPSecBench, MSB, MCP-SafetyBench, MCPTox test
  attacks on the MCP layer (tool poisoning, impersonation), mostly
  not injection through fetched pages.
- **BrowseSafe++** (2026 preprint): 38,675 attacked and clean page
  pairs, mostly injections seen in the wild; release unchecked.

## research_question on Opus, and its runs kept (2026-10-29)

Commit: `04334b1`.

From dorian's direction of 2026-10-29 (PLAN, "dorian's
direction"): `research_question` is the tool grounding is to be
used through, and real runs are its input.

- **Opus 5.5 by default** (`claude-opus-5-5`, from
  `claude-sonnet-5-5`; effort stays medium). The default is the
  model ID, not Claude Code's `opus` alias. The alias would work in
  the Claude Code runner (Claude Code 2.1.312 resolves `opus` to
  `claude-opus-5-5`, checked by hand), but the API runner takes IDs
  only and prices its $1 spending limit by them, and a record
  naming the exact model is one that compares with later runs; on
  the server Claude Code is pinned, so the alias would move only
  with the pin anyway. A caller may still pass `opus` to the Claude
  Code runner. The default is now also the refusal-retry model, so
  only a run asked for on another model is retried, and a decline
  on Opus ends the run. On the API a run costs about twice what it
  did on Sonnet (the description now says tens of cents); the 42-turn
  run in "Prompt injection test suites" ($0.53 on Sonnet) would reach
  the $1 wind-up. On the subscription, Opus spends the usage limits
  faster.
- **Every run is kept** (`internal/tools/research_run_record.go`):
  a directory per run, `run.json` (question, caller, runner, model
  asked for and used, effort, versions, both session IDs, timings,
  turns, calls, sources, cost, outcome, error, findings), Claude
  Code's whole stream or the API conversation, the ledger, the
  backbrief and what the caller was handed. Directories 0700, files
  0600; records past the retention (90 days by default) are deleted
  when the next run is kept; a record that cannot be written is said
  on stderr and never fails the run. Settings and the format are in
  README, "Research run records".
- **The Claude Code runner reads `stream-json`** (with `--verbose`,
  without which Claude Code prints no stream in print mode) instead
  of `json`. The result is the stream's last event, the same object
  `json` printed alone, `structured_output` included (checked by
  hand on 2.1.312); the first event gives Claude Code's session ID,
  resolved model and version. A run cut short keeps the stream up
  to then. `--no-session-persistence` stays: the stream holds what
  Claude Code's session file would, and the record is where it is
  kept.
- **Who called.** The handler records the tier, the client address
  and the name and version the MCP client gave at `initialize`
  (mcp-go's `SessionWithClientInfo`); the CLI records `cli`. With one
  full-tier token the server cannot name a caller by token (PLAN).
- **`grounding research-runs list | show RUN | replay RUN`** reads
  the records; `replay` asks the question again as a new run, on
  the same or another model and effort. Grading is planned (PLAN,
  "Grade the kept runs").
- **The 60-second tool-errors were the caller's** (2026-10-26 01:35
  and 2026-10-29 14:43 on the server). Claude Code's HTTP MCP
  transport ends a tool call at 60 seconds by default; it drops the
  request, mcp-go cancels the request's context
  (`server/streamable_http.go`), `exec.CommandContext` kills Claude
  Code, and the runner, which checked only its own deadline,
  reported "printed nothing (signal: killed)", logged as
  `outcome=tool-error`. Now the runner says "the caller cancelled
  the call after N s", the record's outcome is
  `cancelled-by-caller` with what the run had done, and the journal
  logs `outcome=client-cancelled` for any tool whose request was
  cancelled. Whether a run should outlive its caller is open (PLAN).
- **Brave's Free plan** surfaced the same day: 1 request a second,
  so parallel searches 429. Pacing across processes is planned
  (PLAN, "Keeping the tools sharp").

## The project is grounding, not grounding-mcp (2026-10-31)

- It is more than an MCP server (the CLI, the run records, the
  researcher), so the forge repository was renamed in place, not
  pushed to a new one: the rename keeps the deploy webhook and its
  secret, the releases and the history, and the forge redirects the
  old URL, so production's push-to-deploy kept working while the
  deployment's configuration still named the old one. The
  deployment followed the same day. Commits `8c207f8` (the rename)
  and `007eb16` (PLAN's evals note, recovered from an uncommitted
  draft).
- The User-Agent is now `grounding/<version> (<the public
  instance's URL>; mailto:<GROUNDING_EMAIL>)` ("Rate limiting"
  above records the old one). The contact email is what earns
  Crossref's polite pool and Wikimedia's identified tier, not the
  product token, so the rename costs neither.
- The Go module was already `btr.mt/grounding`. Left as they were:
  the local folder and the deployment's names for its secrets,
  which no reader sees.

## Keys from a command, and release pins (2026-11-01)

- A key the CLI needs can come from a command: the variable
  `NAME_COMMAND`, run through `/bin/sh` at the moment a command
  needs the key (first line of its output, as `pass` prints it),
  the `PassCmd` pattern. A shell that never needs it decrypts
  nothing, and the variable holds no secret. `serve` never runs
  it: a network-facing server should not execute a command named
  by its environment. Now `config.SecretFromEnvironmentOrItsCommand`.
- A command that needs a key is always registered, and asks for
  the key only when run: a missing key must not look like a
  missing feature ("unknown command").
- A local build over a pinned release does not last: where the
  operator's tooling installs the CLI as a pinned, sha256-verified
  release, it puts the pinned version back within the hour. A
  release and a pin bump are an install.
- An error a command returns prints once, without the usage text
  (`SilenceErrors`, and `SilenceUsage` set in root's
  `PersistentPreRun`, so a flag Cobra cannot parse still shows
  usage). Commit `309486c`. Its add-on half is in the add-on's LOG.

## Releases again: the workstation pins one (2026-11-01)

- `f6c6a32` (2026-10-31) dropped the release script on the grounds
  that production builds from source, so no release was needed.
  True of the server, not of dorian's workstation: it installs the
  CLI from a release, pinned and sha256-checked, and replaced a
  local build of master with v1.1.0 within the hour (the add-on's
  LOG, 2026-11-01). So the script is back
  in the repository, as it was on the unmerged
  `release-binaries-script` branch (`afb810b`), renamed to
  grounding throughout.
- v1.2.0 is a minor release over v1.1.0 (2026-09-25): 48 commits
  adding the web tools (`web_search`, `web_fetch`,
  `answer_question_from_web_page`), `research_question` and its run
  records, the Apache-2.0 licence, the rename, and the CLI key
  fixes; every v1.1.0 tool, command and environment variable is
  still there.
- Published from `3be4b70` with the script; the pin bump installed
  it on the workstation the same day, sha256 `76944df0…`,
  `download` listed with no key in the environment.

## DOI queries and empty results (2026-11-06)

- A DOI search through a catalogue backend (`10.1037/h0046234`)
  said "No results found" beside `5 raw → 0 after filter`. The
  backend's text search returned five APA works sharing only the
  `10.1037` prefix, none of them the paper, so the filter's outcome
  was right. Its reason was not:
  it reads only title and abstract, so it would have dropped a
  record carrying the exact DOI too.
- `FilterByRelevance` now treats a query that is wholly a DOI as
  asking for one work and keeps only records carrying that DOI. Not
  a bypass: that would present the five as hits.
- `ContextResult.EmptyResultExplanation` separates filtered out,
  failed and skipped from a clean negative, for the CLI and MCP
  alike. `filtered_count` is no longer omitted at zero.
- What this meant for the add-on's backend is in its LOG.
- Deployed as `67b544e` and released as v1.2.1, installed on the
  workstation by its pin. Checked live against Crossref: a DOI
  search explains its empty result and `verify --doi` finds the
  paper. Finding the work from a DOI search is in PLAN, "A DOI
  query should find the work".


## The page reader moves to Claude Code (2026-11-06)

- dorian found the reader billing his API key and asked for it on
  the subscription instead. With
  `GROUNDING_RESEARCH_CLAUDE_CODE_OAUTH_TOKEN` set it now runs in
  headless Claude Code on Sonnet 5.5 at low effort; without the
  token it stays on the API with Haiku 5.5, as the researcher does.
  So once this is deployed, `ANTHROPIC_API_KEY` can leave
  production's sealed environment, which also closes the
  researcher's fallback
  to the API.
- The `claude -p` layer moved out of the researcher into
  `internal/claudecode` rather than being copied: its isolation is a
  security property, and two copies could drift. The researcher adds
  its MCP server and ledger on top; the reader passes neither, so it
  has no MCP server at all.
- A run's own words never reach the reader's caller. A page can steer
  the `result` text and stderr as easily as the answer, and only the
  answer goes back framed as untrusted, so the reader's failures are
  fixed wordings and the run's text goes to the server's log. The
  researcher still quotes its result text in its errors: worth the
  same look.
- At most three reader processes run at once, since each is a whole
  Claude Code on a small server. Not measured in production.
- Live, locally with no API key: three real pages answered in 6 to
  11 seconds, one of them on IEDs with no refusal. Real Claude Code
  reports `StructuredOutput` as its only tool and no MCP server, and
  given a page ordering it to run Bash and print a canary, the reader
  answered from the page, left the canary out and reported the order
  in `text_addressed_to_ai`.
- Seen, not fixed: Sonnet sometimes copies a link's markdown into a
  quote despite the brief, and a quote cut off inside a link is marked
  not found. The flag is honest, but a true quote reads as unconfirmed.
- The token's name now undersells it (`RESEARCH` covers the reader
  too). Left alone: renaming means another reseal.

## Page-reading bake-off (2026-11-07)

Why: Firecrawl at 80% of its month with 20 days left, and dorian
weighing a self-hosted crawl4ai against it or a scraper of his own.
Rather than argue it, 31 pages were read eleven ways: four fetchers
(plain GET, Firecrawl's rendered HTML, crawl4ai bare, crawl4ai with
its page-interaction features) by two extractors (readability, as
production; go-trafilatura), plus crawl4ai's raw, fit and
query-aware (BM25) markdown. Harness, URL set, answer key, outputs
and scores in `evals/page-reading-bakeoff/` and
`internal/tools/page_reading_bakeoff*_test.go` (build tag
`pagereadingbakeoff`, out of the normal suite), branch
`bakeoff/page-reading`. Scored two ways: key passages (one to three
per page, with the reader's quote matcher, spacing ignored) and a
blind judge (Claude Code, Sonnet 5.5, on the first 10,000
characters, `web_fetch`'s default window), answerable 0--3 and junk
0--3.

| Fetch + extract | Answerable (fail = 0) | Fully | Passage recall |
|---|---|---|---|
| Firecrawl + trafilatura | 2.50 | 22/30 | 90% |
| Firecrawl + readability | 2.47 | 23/30 | 88% |
| crawl4ai bare + trafilatura | 2.23 | 21/30 | 78% |
| crawl4ai full + trafilatura | 2.13 | 20/30 | 78% |
| crawl4ai full, fit markdown | 2.13 | 20/30 | 80% |
| crawl4ai bare + readability | 2.07 | 19/30 | 73% |
| plain + trafilatura | 1.77 | 16/30 | 63% |
| plain + readability (today) | 1.67 | 15/30 | 61% |
| crawl4ai BM25 markdown | 1.67 | 11/30 | 49% |
| crawl4ai full, raw markdown | 1.60 | 14/30 | 76% |

- **The plain path fails silently.** On PMC, Nature and JSTOR it
  returns a bot check ("Checking your browser before accessing
  pmc.ncbi.nlm.nih.gov") as the page's text, with no warning; five
  of thirty "successful" plain reads held nothing the judge could
  use. Firecrawl did it twice (Quora's "Blocked by network
  security"; Observable). The researcher reads a challenge page and
  moves on. This is the largest finding, and fixable whatever
  renderer is chosen.
- **Rendering matters more than extraction.** Swapping readability
  for trafilatura moved the plain path 1.67 to 1.77 and Firecrawl's
  88% to 90%. Trafilatura kept Nature's abstract, which readability
  drops behind every renderer, but go-trafilatura glues words at
  inline markup (`*Intrinsic*cognitive`) and pulls in a WebAssembly
  regex runtime (wazero). No decisive case for a swap.
- **A bare self-hosted render recovers about half the gap** between
  plain and Firecrawl (1.67, 2.07--2.23, 2.50).
- **crawl4ai's own features did not earn their keep here.** Full
  mode beat bare on JavaScript-built pages (2.8 against 1.8, and
  Reddit's wiki: 44,554 characters against 299) but lost on others:
  on the ABS release, overlay removal or the full-page scroll left
  only the skip links; YouTube and JSTOR went empty. Its markdown
  was worse than running our extractors on its HTML: raw markdown
  spends the first window on navigation, and the BM25 filter
  discarded the answer on most pages. Its default setup reads no
  PDF (its PDF strategy was not wired in).
- **Bot protection is a noisy snapshot.** A rerun minutes later
  passed Wiley in full mode and blocked T&F even bare: Cloudflare's
  verdict follows the address's recent history, not the features.
  Firecrawl refuses Reddit and the NYT outright ("we do not support
  this site"); crawl4ai got Reddit. No method passed a paywall
  (Substack, NYT).
- **Footprint:** crawl4ai 0.9.5 in Podman, 351 MB idle, 1.73 GB at
  peak under sequential load. Firecrawl spent about 31 credits.

Limits: thirty hand-picked pages, one run, from a residential
address (the production server's datacentre address will fare
worse against bot checks); key passages chosen by the session from
the outputs; the
judge not yet calibrated against dorian (spot-check pending:
`nature-numpy`, `reddit-askhistorians-faq`, `abs-population`,
`ipcc-spm-pdf`, `observable-gallery`).

What it suggests, for dorian to decide: detect challenge and empty
pages on the plain path and say so; then fall back automatically
(plain, a local render, Firecrawl last) rather than leaving the
choice to the model; and read PDFs locally. On these numbers a thin
headless browser gets what crawl4ai bare got, without its server's
advisory record (25 published in 2026, 6 critical, nearly all in
the Docker server; its GitHub security page, read 2026-11-07). Who
spends the Firecrawl credits is still unknown; if it is the
operator's agent harness's crawl, none of this cuts the bill.

## Firecrawl's spend was PDFs (2026-11-07)

Firecrawl's month (1,000 credits, 24 Oct to 24 Nov) was at 975 by
the 7th, all on the one key grounding holds (Firecrawl's own
`/v2/team/credit-usage/historical?byApiKey=true`), and its
dashboard showed only `/scrape` jobs: about 80 in the period, some
30 of them the bake-off's. So not the harness's crawl (none appear),
and at roughly twenty credits a job, not web pages.

- **The cause:** Firecrawl bills a scrape at one credit plus one per
  PDF page parsed (docs.firecrawl.dev/billing, read 2026-11-07), and
  `web_fetch`'s refusal of a PDF on the plain renderer said "try
  renderer firecrawl". Every paper the researcher met, it was told to
  send to Firecrawl, whole. The bake-off's two PDFs alone cost about
  45 credits, so its spend was nearer 75 than the 31 first said.
- **Fixed in `a56d601`:** the refusal points to the citation tools
  and names the cost; every scrape sends `parsers: [{type: pdf,
  maxPages: 10}]` (`backends.FirecrawlPDFMaxPagesParsed`), so a PDF
  costs at most 11 credits, and the caller is told when one was cut
  short (`numPages` against `totalPages`, both in the answer's
  metadata); each paid scrape writes a journal line with the content
  type, the pages parsed and in all, and the answer's
  `provider.creditsCost` (what that field counts is not documented;
  logged as given). No URL, as in the access log.
- **The same commit refuses bot checks served as the page** (the
  bake-off's largest finding): a page under 2,000 characters holding
  a known challenge or block phrase is an error naming it, and a page
  with under 200 letters outside its links carries a note. Over the
  bake-off's 330 outputs it caught six of the seven silent failures
  the judge found on production's two paths (PMC, Nature, JSTOR,
  Quora; YouTube and Reddit's sign-in wall by the note), missed
  Observable's script code, and flagged no good page.
- The scraped pages stay out of git (`evals/page-reading-bakeoff/
  .gitignore`): the repository is public and they are other people's
  text. Kept locally only.

## The page-reading ladder (2026-11-07)

Built on branch `page-reading-ladder`, not yet deployed (PLAN "Page
reading"). dorian took the recommended defaults: plain first, a cap
of 25 Firecrawl credits a day, `auto` the default renderer, and
GROBID built now.

- **The ladder** (`internal/tools/web_page_reading_ladder.go`):
  plain, then crawl4ai, then Firecrawl. It climbs on a bot check or
  block page, HTTP 401, 403, 429 or 5xx, a failed render, or almost
  no text outside the links; it stops at once on an address that is
  not public, a 404 and the like, and a file that is not text. A
  page crawl4ai rendered thin is returned, not paid for again: a
  browser of our own already ran it. A `.pdf` URL never goes to
  Firecrawl unasked. The note above the frame names the rung and
  why the lower ones failed.
- **Left out of the plan's climb rules:** "main content under a few
  hundred characters". The links heuristic from phase 0 already
  fires below 200 letters outside links, which covers it, and
  nothing in the bake-off argued for a second threshold.
- **crawl4ai runs bare** (only `cache_mode: bypass`): the bake-off's
  full mode read worse. Its retry-with-features step from the plan
  was not built for the same reason.
- **The cap** is a file under `flock` in the state directory
  (`GROUNDING_STATE_DIRECTORY`), because a research run's child
  processes would each have a cap of their own in memory. Claude Code
  gives that child a fresh `HOME`, so the parent passes
  `STATE_DIRECTORY` to it explicitly. One credit is reserved before a
  scrape, PDF pages added after; a ledger that cannot be written
  refuses Firecrawl (fail closed). The production unit sets no
  systemd state directory and writes only under its own state
  folder, so the deployment must set `GROUNDING_STATE_DIRECTORY`
  there, or Firecrawl will refuse every scrape.
- **crawl4ai and SSRF:** the URL passes `WebURLOrRefusal`, its host
  is resolved and every address checked, and the final address is
  checked again. DNS rebinding, redirects and sub-resources still
  reach whatever the server's network lets the browser reach: the
  firewall is the guard, these are the layer in front.
- **GROBID** reads PDFs the plain fetch gets, from uploaded bytes,
  so it dials nothing; TEI to markdown in `internal/backends/grobid.go`.

Checked live locally against crawl4ai 0.9.5 and
`grobid/grobid:0.9.1-crf` in Podman, with no Firecrawl key, so
nothing was spent. Quora (HTTP 403 to plain; the page Firecrawl
failed in the bake-off), Taylor & Francis (403), Observable (429)
and YouTube (links only) were all read by crawl4ai. The Attention
paper's PDF read through GROBID in about 11 s, with its sections
and references; the IPCC summary read whole but untitled.

**GROBID's memory rules it off the production server for now.** The CRF image idles at
3.4 GB and peaked at 4.0 GB on one paper, whatever the worker count
(10 or 2) or Java heap (`-Xmx1g`): the models live outside the heap.
In a 4 GiB VM it was killed (exit 137) twice, alone and beside
crawl4ai. The production server had 4.6 GB free and crawl4ai
peaked at 1.7 GB in the bake-off. Measured on arm64 (the image is
native there); x86 in production is unmeasured. dorian decides
where it runs, or whether yet.

**Follow-up (2026-11-08).** dorian: GROBID is a no-go at 4 GB, so
it was deleted (git keeps it, at `8c0d1d7`); PDFs go to `pdftotext`
(poppler-utils) for an MVP. It reads the PDF from stdin with fixed
arguments, no shell, an empty environment, 30 seconds and 4 MB of
text at most; the Attention paper and the IPCC summary each read in
under half a second, as plain text. Its output cap first did nothing:
a type embedding `bytes.Buffer` inherits `ReadFrom`, which `io.Copy`
(and so `exec`) prefers to `Write`. Hold the buffer as a field.

dorian also asked for proper logs of page reading for quality
control. Every read now adds a JSON line (rungs, reasons, credits,
the first 300 characters) to `page-reads/` under the research run
records directory: the simplest place that already has the right
privacy, retention and backup in production, with no deployment
change.
Research runs' child processes are handed the records directory, so
their reads land there too. Triage is `jq` (README "Page-read
log"), not a command of its own.

**Deployed (2026-11-08).** Master fast-forwarded to `5fddae2` and
pushed; production reported `v1.2.1-9-g5fddae2`. Live
`check_backends`: crawl4ai not configured, pdftotext not installed,
and Firecrawl unhealthy: "cannot keep the daily credit count ...
mkdir /nonexistent: read-only file system". The unit's account has
HOME `/nonexistent` and ProtectSystem strict, as expected; the
deployment change (state directory, poppler-utils) waited for
dorian. A Quora fetch failed honestly, naming each rung. The
journal showed no page-read log error after it, so the log is
being written (the session could not read the service's state
folder itself).

## crawl4ai without Docker (2026-11-08)

dorian asked why crawl4ai is not bundled with grounding. Not inside
grounding's own service: the browser runs hostile JavaScript and
would share the server's loopback services and its private mesh,
and an out-of-memory kill would take grounding with it. But a
sibling systemd service, deployed beside
grounding, can be held tighter and more simply than a container.

- crawl4ai's server is not in its pip package: it is the image's
  `/app` (gunicorn, Redis, supervisord). Its library is enough for
  grounding's one need, so a thin shim replaces the server.
- systemd's `IPAddressDeny=localhost link-local multicast
  10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 100.64.0.0/10 fc00::/7`,
  tested as a real unit in the Podman VM (systemd 259): a public
  address answered; loopback, a private gateway, and a public name
  pinned to 127.0.0.1 (rebinding) were dropped by the kernel. DNS
  through the host's private resolver failed too, so the service
  needs its own `resolv.conf` naming a public resolver (Quad9's
  9.9.9.9, say).
- crawl4ai 0.9.5 itself, run from such a unit (its image under
  `--cgroups=split`, host network, resolver 9.9.9.9): Wikipedia
  rendered (200, 748 KB); 192.168.127.1, 127.0.0.1:22 and a mesh
  address all failed. Unfiltered, the gateway answered.
- crawl4ai runs Chromium with `--no-sandbox` by default, in Docker
  or not, so the systemd unit is the containment either way.

Against running it in Docker: the filter covers every packet the browser
sends, rebinding, redirects and sub-resources included, with no
firewall chain to write, no inbound port (a Unix socket), no token,
no Redis, and a far smaller install than the 7.8 GB image.

## Stage 2 built (2026-11-08)

Overnight, while dorian slept; nothing signed, deployed or pushed to
master.

- **The deployment tooling** gained a public-only network reach,
  its own resolvers for it, a memory cap and listening on a Unix
  socket that named services may open (a group on the socket's
  directory). Edge cases tested in a real unit: 0.0.0.0, ::1 and
  IPv4-mapped loopback, private and mesh addresses were all dropped
  by the plain list, so `::ffff:0:0/96` is left out (it would risk
  dual-stack IPv4); 0.0.0.0/8 is added to state the intent.
- **grounding:** `crawl4ai-render-service/` (shim, build, pinned
  requirements, tests) and `CRAWL4AI_BASE_URL=unix://...`. Live
  locally: crawl4ai 0.9.5 in a venv, built by the deploy script twice
  (the second reused the venv), its launcher serving the socket;
  grounding's auto renderer read Quora (403), YouTube (links) and
  Observable (429) through it. Taylor & Francis got Cloudflare's JS
  challenge from bare crawl4ai, where the Docker server had read it:
  that page climbs to Firecrawl. Comparing crawl4ai's stealth setting
  was refused by the harness as bot-check circumvention, and left.
- **Deployment:** the browser's service proposed, its configuration
  linting clean.
- **Pushing master was refused** by the harness as a production
  deploy with dorian away; it is his in the morning (PLAN).

## The browser live in production (2026-11-08)

dorian approved the deployment tooling's release and the browser's
service; master already carried the shim and the socket client
(`899db42`, pushed with his leave).

- **Works:** `check_backends` says crawl4ai ok, and YouTube renders
  through it, so Chromium runs natively on Debian 13 under the
  service's lockdown. The unit holds about 820 MB of its 2G. Its
  `IPAddressDeny` lists loopback, private, mesh, the Docker bridges
  and the server's own addresses.
- **From the datacentre, not from home:** Quora, read by crawl4ai
  from home, is a Cloudflare JS challenge from the server's
  datacentre address, and Firecrawl was blocked too (it still spent
  a credit). The bake-off ran from a home address, so it overstates
  what production reads. Whether to route through a home address
  waits on a count (PLAN).
- **crawl4ai's cloud advert** on every blocked page is hidden by
  `CRAWL4AI_NO_CLOUD_NOTICE=1` in the launcher (`9cd2baa`).
- **Firecrawl's second account:** production's sealed environment
  carries its key. `ANTHROPIC_API_KEY` was dropped, as the Claude
  Code token runs the researcher and the page reader. One key only:
  the daily cap (750 a month) sits under one account's 1000. A
  fallback key is in PLAN.


## The core and add-on split (2026-11-15/16)

Branch `registry-seam-for-optional-sources`, on master from
`cdc0598`; release v1.3.0. Two servers now: the public instance
runs core alone (free, sanctioned sources; anyone), and a second
server runs a private add-on built on core (a grey backend; the
operator's own agents use it).

- **The seam and the hooks.** Sources, enrichers, citation
  verifiers, health probes and availability providers register from
  an add-on's `init()` (`615bee4` onwards); download providers,
  full-tier text, tool parameters, commands, landing-page sources
  and research-run variables through generic hooks (`add236c`).
  Composition is at build time: each binary has its own `main`.
  `TestCoreNamesNoBackendThatBelongsInAnAddOn` is the gate. The
  add-on's module sits under `btr.mt/grounding/` to import core's
  `internal/`, builds with `replace ../grounding`, and in
  production at the core commit the add-on pins (its LOG). The
  backend's LOG sections moved to the add-on.
- **Names.** Every CLI command is its tool's name in hyphens, old
  names kept as aliases; `fetch_context` became `search_catalogue`
  and `download_document` became `find_download_link` (`b08f6d3`),
  which only ever returns a link and is `ReadOnly`. The consumer
  listed both names for two releases and its agents named both,
  then grounding went to master: a tool offered only when named
  vanishes for an agent naming only the old name.
- **The search redesign.** OpenLibrary books ranked apart under
  `books`, `kind` on every record, Unpaywall `availability`,
  diagnostics only with `debug`, `warnings` always, the catalogue
  search log; every independent loop parallel. The add-on's half is
  in its LOG.
- **Deploy order.** dorian chose to put core on master before the
  add-on's server existed ("deploy and accept the gap"); the agents
  lacked book search for the afternoon. They were repointed only
  once the new server's `/health` answered, so they never pointed
  at nothing.
- **The public code.** Not a rewritten history at first: a filtered
  copy. A publish script built one commit from a commit's tree
  without PLAN, LOG, the agent files and the scripts, grepped it for
  the deployment's names, built and tested it and ran gitleaks;
  dorian pushed it to `github.com/majorminors/grounding`. Core
  stopped naming its deployment (`b604bbe`), and a fresh root
  (`36963a2`) replaced the first push. Retired the same week ("The
  code repository made public").
- **The README is the landing page** (`4881398`): the part above a
  marker is rendered per visitor by goldmark, with the sections the
  registry or the tier decide generated into the README and filled
  live; below it, "Run your own grounding", replaced on the page by
  a link to GitHub. cmd embeds a copy (Go cannot embed above its
  package, and an add-on's build needs the page);
  `TestTheReadmeIsCurrentAndTheLandingPageHasItsCopy -update-readme`
  keeps both current.
- **Found at the end:** the forge's repository was public, so the
  filtered copy hid nothing from someone who looked there.

## The forge repository goes private (2026-11-16)

- The forge's repository was public, so its full history (PLAN,
  LOG, the add-on's backend) undid the filtered GitHub copy. Made
  private by dorian once nothing fetched it anonymously: production
  reads it with a read-only account, and the add-on pins core as a
  submodule so its build can read core the same way (the add-on's
  LOG). v1.3.0's binaries were copied to GitHub's releases,
  checksums checked; its code is the public copy's.

## The log baseline before the split (2026-11-16)

- From the public server's research-runs folder up to the split
  (35 run records, 36 page reads, one catalogue search). 25 runs
  were a test batch (29 October, `example.org` URLs); real use is
  10 runs, 29 October to 10 November.
- Semantic Scholar answered 3 of 99 attempts (no key; the shared pool
  throttles). Page fetching failed 28 of 54 before the browser rungs
  (7 November; oecd.org and rand.org refused every time), 1 of 26
  after (three runs only). Brave's rate limit lost 8 searches in
  three runs. Every run answered, the longest 270 s; 140 of 145
  findings supported. Five runs over $1 at list price, $1.96 the most.
  `citation_graph` never called.
- Fixed from it, deployed in `2af6752`: Firecrawl's log counted a
  credit for a scrape refused before sending (`0d74cbc`); a page that
  is only a cookie or consent notice now counts as blocked, so the
  ladder climbs (`5c2a174`), which caught exactly the 3 Reddit walls
  among the 60 real reads. Firecrawl's docs say a failed scrape
  costs nothing, so the cap still counts outright failures: erring
  cheap, on purpose.

## The code repository made public (2026-11-16)

- dorian: a push to grounding should deploy everywhere, GitHub
  included, without scripts. So the forge's repository was started
  afresh from the clean public history (the GitHub copy's,
  `385bbc4`, plus `b32bfac`: public agent rules, the private-name
  test, the release workflow) and push-mirrors to GitHub on every
  push. The mixed history before it is archived privately.
- PLAN, LOG, the deployment's agent notes and the private-name list
  moved to a private repository of their own, checked out beside
  the code. The publish script and the forge release script retired
  with the old history: a tag now builds its release on GitHub
  (`.github/workflows/`).

## The planning docs made public (2026-11-16)

- dorian, the same day: PLAN and LOG belong with the code and
  should be generic, as the operator's agent-network engine
  publishes its own. They are back in this repository as real
  files, rewritten without the deployment's hosts, addresses,
  domains, tooling or secrets' locations, and without the add-on's
  backend. Decisions, reasons, dates and commits stayed.
- What went elsewhere: the add-on backend's history and plans to
  the add-on's LOG and PLAN; the deployment's notes and its open
  items to its own configuration and plan. A few deployment-only
  passages with no home there were dropped.
- The private-name test reads its list from
  `GROUNDING_PRIVATE_NAMES_FILE` and is skipped without it: the
  list names what is private, so it lives outside this repository,
  and core names no path to it. `TestCoreNamesNoBackendThatBelongsInAnAddOn`
  now checks PLAN.md and LOG.md too.

## Push, then deploy (2026-11-16)

- dorian: "if I push grounding, it should deploy all three, and if I
  push the add-on it should deploy just that one", with no waiting
  for a timer and no scripts. Today a push to core deploys core's
  server by its webhook and reaches GitHub by the forge's push
  mirror within seconds (seen with `2a5f8df` and `5ca3a60`); the
  add-on's server still waits for its hourly fetch and a pin.
- The rest is the deployment engine's, not grounding's: a site lists
  every repository it is built from, code (a push rebuilds) or
  content (a push runs a declared command), and the engine makes
  the forge's webhooks itself. Designed and built in the engine
  (its PLAN "Push, then deploy: every repository a site is built
  from"), on its development branch since 2026-11-16, and released
  with its next batch (dorian: releases are batched; work lands and
  waits unless it is urgent).
- The mirror pushes over ssh with a key of its own, which GitHub
  holds as a write deploy key on this repository alone: no GitHub
  token is kept. It is exact, so a branch or tag deleted on the
  forge goes from GitHub too.

