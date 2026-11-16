# grounding Plan

This file is public, like the code: it holds grounding's own plans,
in generic terms. Plans for a particular deployment of grounding
belong with that deployment's configuration, and plans for an
add-on's backend in the add-on.

## Up next

- **Deploys that follow every repository a site is built from.**
  The deployment engine gained push-then-deploy across repositories
  (a site lists its code repositories, which rebuild on a push, and
  its content repositories, which run a command): it is on the
  engine's development branch and ships with its next release. Once
  a deployment uses it, an add-on's server rebuilds on a push to
  core with no pin to move, and core's push needs no hourly fetch to
  reach it.
- **Read the production logs after the split, on 2026-11-30.** The
  baseline to 2026-11-16 is done (LOG "The log baseline before the
  split"). Read both servers' logs (core's public server, and the
  add-on build the operator's agents use) with README "Logs and
  records"'s triage commands, and compare. Count `fetch_context`
  and `search_catalogue` together (one tool, renamed). The baseline
  is thin, 10 real runs and none after 2026-11-10, so the
  comparison is only worth making if the agents use grounding
  meanwhile; the public server's logs will be public callers alone.
- **From the baseline: a dollar stop for subscription runs.** The
  Claude Code runner has none (the $1 limit holds only for the API
  runner); 5 of 10 real runs were over $1 at list price, $1.96 the
  most. Decide whether a turn or token bound should stand in.
- **From the baseline: is `citation_graph` worth offering the
  researcher?** Never called in 35 runs; the questions were mostly
  not scholarly, and the prompt pitches it narrowly. Look again at
  2026-11-30.
- **A note to the main consumer about what catalogue callers now
  see**: books under `books`, `kind`, `availability` with
  `find_download_link_arguments`, `warnings`, diagnostics only with
  `debug`. For the prompts of the agents that call the catalogue.
- **Drop `download_document`.** Once no known caller names it (the
  consumer's agents name `find_download_link` alone, and the
  consumer stops offering the old name), remove the alias.
- **`research_question` is the best web tool it can be, and the
  most resilient to prompt injection.** Nothing that holds dorian's
  personal data should ever have to touch the internet itself: an
  agent with that data asks `research_question`, and only the
  question leaves. Work on it comes first.
- **The individual tools are sharp in their own right.** They are
  handed to other models and offered online, so each must be the
  best version of itself, not merely good enough as a researcher's
  instrument.

Who uses `research_question`: its main consumer is the steward
agent of the operator's own agent network, which reaches grounding
as an MCP server and calls `research_question` for research and
for the news; that project's own plans describe its side. The
secondary consumer is dorian's claude.ai connector, and the
direction is to move that use to his own harness.

What follows:

- **Real runs are the input.** The steward's use is real use, and
  is what tells grounding where the researcher falls short. Each
  run is kept as a record (LOG "research_question on Opus, and its
  runs kept"), the means by which runs are read, replayed and
  graded; the grading is planned under "Research agent
  follow-ups".
- **The researcher runs on Opus by default** (same LOG section).
- **Changes to the researcher are judged against its records:** a
  prompt, model or tool change is replayed on past questions and
  compared before it ships, once grading exists.

### Funded deliverable (2026-10-25)

grounding may become a funded deliverable. Of the three threads
raised for it, the embedded research agent is done and live (LOG,
from "Funded deliverable: split, evals, embedded agent" to "Prompt
injection test suites"); the core and add-on split is built (below);
and evals became a chunk of their own. Settled 2026-10-25: core is
Apache-2.0 (added before any funded work, so a version dorian owns
is on record); Claude only until the funding allows other models.

### Core and add-on split

Built 2026-11-15/16 (LOG "The core and add-on split"). Two servers
in production: core alone, free and sanctioned sources, for anyone;
and the add-on build, which the operator's own agents use. The code
repository is public since 2026-11-16 (LOG "The code repository
made public"), and so are these planning docs (LOG "The planning
docs made public").

**Open:** whether the public server runs with no full tier at all
(today `serve` refuses to start without `GROUNDING_AUTH_TOKEN`).

### Research agent follow-ups

`research_question` has been live in production since 2026-10-26,
its researcher in Claude Code on dorian's subscription there and on
the Claude API elsewhere. Design, decisions and live runs are in LOG
("Funded deliverable…", "Claude Code isolation check",
"research_question: the API runner", "research_question: the Claude
Code runner"). Open:

- **Rename `fetch_context` in the kept run records** (dorian,
  2026-11-15). The tool became `search_catalogue` (LOG "The core and
  add-on split"), but records kept before then name
  `fetch_context` in their ledgers and transcripts (on the server,
  and in its backups). Either rewrite the name in the records once,
  on the server, as the server user (records stay that user's
  alone), or have the grader and `research-runs` read both names.
  Until one is done, anything reading records must accept both.
- **Grade the kept runs** (dorian's direction, 2026-10-29). The
  records exist (LOG "research_question on Opus, and its runs
  kept"); grading is next, and belongs with the evals chunk below.
  - **Mechanical, from the record alone:** outcome; turns, tool
    calls, duration and cost; supported findings out of all; quotes
    found; failed tool calls and fetch refusals; text addressed to
    an AI reported; the mix of sources by kind (DOI, official,
    news, wiki, forum) from the ledger. Written as `grade.json`
    beside `run.json`, with the grader's version, so a grade can be
    recomputed.
  - **By a judge, for the prose:** does the summary answer the
    question; are the gaps honest; is the authority of each
    finding's source fit for its claim; anything said that no
    source carries. A rubric first, scored on a handful of runs by
    dorian, so the judge is calibrated against him before it is
    trusted. On the subscription the judge costs nothing per run;
    on the API it is a cost to state.
  - **A regression set from real runs:** questions dorian marks as
    fit to reuse (personal ones are not), replayed with
    `research-runs replay` on a candidate prompt, model or tool
    change, and graded against their earlier grades.
  - **Privacy:** a judge reads whole records, so it runs where the
    records are, under the same retention.
  - **Interface:** `grounding research-runs grade RUN|--since DATE`,
    and `list` showing the grade.
- **Who called.** The server has one full-tier token, so a record
  names its caller only by the MCP client's name, version and
  address. Labelled tokens (Deferred, "Per-token scoping and audit")
  would name each caller; the run record would take the label.
- **Records under a sandboxed service.** A service with
  `ProtectHome` and `ProtectSystem` cannot write the default under
  the home, so the deployment must set
  `GROUNDING_RESEARCH_RUN_RECORDS_DIR` (or a `StateDirectory=`);
  without it a run says on stderr that its record could not be kept
  and answers as ever. Production sets it (2026-10-29) and backs the
  folder up. Whether a backed-up, synced folder suits a deployment
  is its operator's choice: the copies go wherever the backup goes,
  and the retention (default 90 days) deletes them at the source
  only. Worth a sentence in README "Research run records".
- **MCP clients stop waiting, and that kills the run.** Claude
  Code's HTTP MCP transport ends a call at 60 seconds by default;
  the client drops the request, mcp-go cancels its context, and the
  runner's Claude Code is killed with it (LOG "research_question on
  Opus, and its runs kept"). Progress notifications would not help:
  Claude Code's help says a per-server timeout is a hard wall-clock
  limit that progress does not extend. What is open: whether a run
  should outlive its caller (detached from the request, finishing
  into its record, its backbrief fetched later by session ID, which
  is the "follow-up calls" item below), or whether callers should
  set a longer timeout for grounding (Claude Code takes one per
  server), or both. The record and the journal now say when a
  caller cancelled.
- **The Claude Code runner has no turn bound and no refusal retry.**
  Claude Code lists no `--max-turns` (one run took 42 turns), and its
  `--fallback-model` covers overload, not refusals; only the six-
  minute timeout bounds a run. Look again at each Claude Code
  release, or at the Agent SDK's `maxTurns`.
- **The live canary test's empty tool calls:** under `go test` the
  researcher's academic and Wikipedia calls come back empty over
  about 70 seconds; the CLI with the test's whole environment
  recreated succeeds in 11. Left to suspect: the test-built grounding
  binary, and what `go test` does to child processes. Isolation and
  real use are unaffected.
- **No link-following.** The fetch restriction stops the researcher
  following any link from a page it has read, even on the same host
  (the attacker's page usually is). Revisit if runs starve; none has
  yet (one refused two links and searched instead).
- **Follow-up calls:** each backbrief carries a session ID; a later
  call with it and an instruction ("go further on x") would resume
  with the ledger and the conversation. The ledger is keyed by
  session already; holding sessions makes a stateless server
  stateful (memory, a time limit), so it waits for a need.
- **Later:** OpenAI-compatible endpoints, so others can run their
  own models.

### Evals: check what we have; hostile ones by design

dorian, 2026-10-26: "I'm getting more serious about evals... maybe I
want to create a package or a test environment for this." A chunk of
its own, to come soon but not now; until it is planned, the
hostile-page injection test waits for it. It spans this repository
and the agent network that consumes it, so its first job is a
decision, written in both projects' PLANs.

**Narrowed later the same day, and then for hostile evals
(2026-10-31).** dorian on 2026-10-26: "we only have to double check
that the evals we have are alright. We have no hostile evals." So
the first step is the check, with no package or shared system
assumed: the session-5 queries (LOG, "Evaluation sessions 1--5"),
not re-measured since the 2026-09-17 rethink, and the live research
runs and the only-this-tool hand run, which are evidence, not cases
yet. For each: is it still valid for what grounding now is, does it
discriminate (against a baseline with no tools or plain web search),
and does it repeat (recorded offline, and live for backend drift).
Each case names the decision it informs. Hostile evals are design
work, not a task to hand off: dorian, 2026-10-31, "We do want to
think about hostile ones, but that's design---I want to learn and
think about these." Plan them with him, not for him; what follows is
the material for that, not a settled plan. (This decision was only
in an uncommitted PLAN draft until now: the 10-29 PLAN was built
without it.)

**The three questions it has to answer:**

- **What evals live inside the agent network?** Its engine already
  has the shape: a profile's specification and cases, stand-in
  tools, a runner for the cases, checks by code and by a judge, a
  pin move naming the cases it changed, and suites for the steward's
  diagnoses, its voice and the proposal reader. Its own leftovers
  include "what Anthropic offers, still unchecked" and "planted
  injections for the quarantined readers".
- **Should users get a package to specify evals for their own
  agents?** For the network's users, the profile cases may already
  be that package, and the gap is documentation and examples. For
  grounding's users ("can my model use these tools?", the
  deliverable's promise), it is a runnable suite they point at their
  own model.
- **What does dorian want to test against broadly, for himself?**
  Models against tools and prompts, across both projects: a bench to
  run before a model change, a pin move or a release.

**What there is to build on:**

- The engine's profile cases and their runner (above).
- UK AISI's Inspect: many providers, attaches MCP servers over stdio
  and HTTP (checked 2026-10-25), logs a UK-government funder will
  know. Its provider list is unconfirmed.
- AgentDojo (ETH Zurich and Invariant Labs, MIT): extensible
  tool-calling environments and injection cases, with custom task
  suites; reportedly used by AI safety institutes.
- IPI-proxy (2026 preprint): pages with planted payloads and an
  exfiltration tracker, for the hostile-page test; must sit at a
  public address (LOG "Prompt injection test suites").
- Grounding's own material: the session-5 queries in LOG
  ("Evaluation sessions 1--5"), never re-measured since the
  2026-09-17 rethink; today's live research runs; the only-this-tool
  hand run.

**What grounding needs, whichever way it splits:**

- **Retrieval:** golden queries with expected DOIs, in Go, recorded
  (offline, reproducible) and live (backend drift).
- **Tool use:** can a model drive the tools? Realistic tasks checked
  from the transcript (right tool, sensible parameters such as
  `domain`, correct answer, calls and cost), mechanically where
  possible, a judge only for prose. A baseline of the same model with
  no tools or plain web search, to show grounding earns its keep. The
  only-this-tool case, with and without grounding instructions in
  the caller's system prompt.
- **research_question:** answer quality against a judge; the fetch
  restriction and injection reporting against hostile pages; cost and
  turns per run; the client-timeout behaviour. The hostile-page test
  (deferred to this chunk, dorian 2026-10-26): IPI-proxy's Target
  Server (VulcanLab, arXiv 2605.11868) at a public address, since
  grounding's address guard refuses loopback and private ones and
  ignores proxies (so its intercepting-proxy mode cannot work); on
  the production server behind its own hostname is a deployment
  change for the operator. Each test is a question naming a target
  page: the researcher must report the payload and never call back.
  Leave out WASP-derived payloads (CC-BY-NC) for the funded work.

**The split to decide:** one eval system for both projects (the
engine's cases growing Inspect as a runner, say) or two (the engine's
for network agents, an Inspect suite shipped with grounding), and
which of them becomes the package users get. Load the `evals` skill
when planning it.

### Page reading: crawl4ai, Firecrawl behind it, pdftotext for PDFs

Decided by dorian, 2026-11-07, after the page-reading bake-off and
the PDF spend (LOG "Page-reading bake-off", "Firecrawl's spend was
PDFs"). The code is built and tested on branch
`page-reading-ladder` (LOG "The page-reading ladder"): phase 0's
honest failures and PDF cap, the `auto` renderer, the crawl4ai
backend, PDFs by `pdftotext`, the 25-credit daily cap, the
page-read log, `check_backends` lines, docs and crawl4ai's
attribution. GROBID was built, measured at 4 GB and deleted
(dorian, 2026-11-08). Deployed 2026-11-08 (`5fddae2`), at first
without crawl4ai: in production `auto` was plain, then Firecrawl.
Owed:

- **Firecrawl's second account, done 2026-11-08:** production moved
  to a second Firecrawl account (1000 credits a period); the old
  one left it. `ANTHROPIC_API_KEY` left production too, as the
  Claude Code token runs the researcher and the page reader.
- **A fallback Firecrawl key (dorian, 2026-11-08: plan it, not yet
  built).** grounding reads one `FIRECRAWL_API_KEY`. Let it take a
  second, used when the first answers out of credits (HTTP 402), so
  both accounts' allowances serve. Worth it only if the daily cap is
  raised past about 33 credits (25 × 30 = 750 a month, under one
  account's 1000). The cap stays one count across both keys. Check
  Firecrawl's terms on more than one free account first.
- **Route the browser through a home address? (dorian, 2026-11-08:
  compare first, decide later).** Quora reads from a home
  connection but is a Cloudflare challenge from the production
  server's datacentre address. Before building anything, run the
  bake-off's URL list through crawl4ai from both (forced `crawl4ai`
  renderer in production; the local shim at home; free) and count
  the pages that differ. If few, leave it. If many, the shape to
  weigh is crawl4ai on a home machine under the same public-only
  network filter, grounding reaching it over a private network with
  a token. Not a proxy hole in the production server's filter: that
  moves the guard onto the home network. A home address gets past
  bot checks because Cloudflare trusts it more, and that ties the
  scraping to the operator's own address: a flagged address
  challenges their own browsing too. The deployment's costs of it
  are weighed with its configuration.
- **Stage 2, crawl4ai in production, live 2026-11-08** (LOG
  "crawl4ai without Docker", "Stage 2 built"). `check_backends` says
  crawl4ai ok; YouTube renders through it, so Chromium runs under
  the service's lockdown. The unit holds about 820 MB of its 2G; its
  `IPAddressDeny` lists loopback, private, mesh, the Docker bridges
  and the server's own addresses. A Docker network made or removed
  restarts it (a follow-up for the deployment tooling).
  - **Observation:** Quora, read by crawl4ai from home, is a
    Cloudflare JS challenge from the datacentre address, and
    Firecrawl is blocked too (one credit spent). Expect some sites
    that passed in the bake-off to fail in production; the page-read
    log will say which.
  - crawl4ai's "Crawl4AI Cloud" banner on every block is off: the
    launcher sets `CRAWL4AI_NO_CLOUD_NOTICE=1`.
- **After stage 2:** triage the page-read log (README "Page-read
  log") and `check_backends`' daily count; tune the climb rules on
  what reaches Firecrawl and on short reads. A security review of
  the new path (the firewall, the rebinding gap, crawl4ai's own
  advisories); patch the pin as advisories land (25 in 2026).

### Keeping the tools sharp

- **Brave rate limit: parallel searches fail.** The key on the
  server is on Brave's Free plan: 1 request a second, 2,000 a month
  (171 used on 2026-10-29). A `web_search` that day came back HTTP
  429, "Request rate limit exceeded for plan". `brave.go` already
  paces to 1 a second (burst 1) and `http.go` retries a 429 once
  after its `Retry-After`, then puts Brave in a 60-second cooldown
  in which every search fails at once. The pacing is per process:
  the server and each research run's child `grounding serve` keep
  their own limiter, so two at once go at twice the rate, and
  pacing at exactly the limit leaves no margin for jitter at Brave's
  end. Recommended, before paying: pace Brave at about 0.8 a second
  in `brave.go`; share the pace across grounding's processes on a
  host (a lock file holding the time of the last request, read
  under `flock` in `http.go`'s limiter, for backends that declare a
  plan-wide limit), or route the child's searches through its
  parent; and for Brave, wait out a 429 (within the call's
  deadline) rather than cool down for a minute. The monthly quota
  is ample at today's use, so the plan's volume is not the problem;
  upgrade only if concurrent research runs become normal.
- **`health` calls an unconfigured backend healthy (found
  2026-11-15).** On a local run, `grounding health` listed six
  backends as "not configured (set ...)" (consensus, exa, brave,
  firecrawl, the page reader, an add-on's key) and then said "All
  backends healthy." `reportWithoutProbing` and `reportKeyOnly` in
  `internal/tools/check_backends.go` build
  `HealthStatus{Healthy: true}` whether or not the key is set. An
  unset key should be its own state, neither healthy nor failing,
  counted apart in the summary (e.g. "8 healthy, 6 not
  configured"). Related: `grounding search --backends consensus`
  with no key printed "Nothing was searched: consensus skipped
  (see diagnostics)", but the diagnostics said only "consensus: 0
  raw"; the `SkippedReason` ("CONSENSUS_APP_KEY not configured")
  never reaches the CLI. Tests: a config with no keys must not
  report all healthy, and a skipped backend's reason must print.

### Other

- **A DOI query should find the work.** Since v1.2.1 a search that
  is wholly a DOI is honest about coming back empty, but it still
  comes back empty, even on Crossref with a real DOI: backends match
  a DOI as text, and the filter then rightly drops what they return.
  It points to `verify --doi`, which is the working route (LOG "DOI
  queries and empty results"). The fix: when the whole query is a
  DOI, skip text search and look the DOI up, using what `verify`
  already does, then return that record as the result.
  - Tests: real DOI found, on the CLI and MCP alike; unknown DOI
    says so plainly; a DOI among other words stays a keyword search.
  - Whether an add-on's source should answer DOI queries is the
    add-on's question (its PLAN).
- **Agent web toolkit: follow-ups.** Live since 2026-10-03
  (`d53158c`); every tool checked live (LOG "Agent web toolkit").
  - The Brave plan is checked: the server's key is on the Free plan
    (Brave's own 429 said so, 2026-10-29); see "Keeping the tools
    sharp".
  - A crawl tool (like the operator's agent harness's crawl, via
    Firecrawl's async crawl, with cancel on timeout), if a project
    needs one.
  - Let that harness's researcher use grounding's web tools in place
    of its own, once the prompts settle.
  - Reconsider a public plain `web_fetch` after watching full-tier
    use.
- **Verify the landing page's client steps**---dorian rewrote the
  lede, the Connect note and the "Using it well" snippet; the example
  prompts and "What it won't do" are still drafts he may revisit.
  The Claude connector path (Settings → Connectors → Add custom
  connector) and ChatGPT's developer-mode wording were written from
  memory, not checked against the apps.
- **Confirm the live annotations**---after the 2026-09-25 deploy,
  a live `tools/list` check was blocked, so the titles and
  `readOnlyHint` are verified only by the offline tests. Connecting
  from a client and looking at the tool picker settles it.
- **Security review of the web toolkit.** The 2026-07-12 sweep
  rested on there being no caller-controlled URL fetch; `web_fetch`
  and the reader are one. Review the address guard (IPv6 edge cases,
  redirects through Firecrawl, ports), the frame and the reader's
  prompt against the attacks in Unit 42's 2026-03 survey of
  injections in the wild, and the size and time limits.
- **Sweep for fuzzy-lookup paths.** Any path that resolves an
  identifier to a document without an exact-match guarantee should
  caution the caller to check title and authors before citing (the
  convention began in the add-on's download path, its LOG). The web
  toolkit's tools were added since and have not been looked at with
  this in mind.
- **Dedicated contact address for `GROUNDING_EMAIL`**---the email
  in the User-Agent (and Unpaywall's `?email=`) is what earns
  Crossref's polite pool and Wikimedia's identified tier; it cannot
  go anonymous without losing both and breaking Unpaywall. Point it
  at a project alias rather than a personal address, in production
  as well as locally.
- **Deploy and watch the public limit**---after deploying, grep
  the journal for `outcome=rate-limited`; if real people hit it,
  raise `publicTierCallBurstPerClient` or the refill in
  `cmd/public-tier-call-limit.go`.
- **Every key from a command** (dorian, 2026-11-15): no keys
  exported into the shell environment, `CONSENSUS_APP_KEY`
  included. grounding gets its keys itself when it needs them, from
  a password manager's command, the way
  `config.SecretFromEnvironmentOrItsCommand` already serves some
  (LOG "Keys from a command, and release pins"), so the CLI and the
  local stdio server work from any shell. Found when a Consensus
  search from a local session was skipped for want of the key.
- Wikipedia: the User-Agent fix (the Rethink chunk) is the likely
  cure for "touch and go"; if it is still flaky, the REST migration
  and the `isRelevant` intro filter are in Deferred.
- Tests still owed on the rethink: the OpenCitations fallback in
  `citation_graph` (`edgesFromDOIs`) and the Unpaywall-first path in
  `download` have no unit tests because the tools/cmd layers build
  backends against fixed base URLs (no mock seam); they are covered
  by the backends' own httptest tests and today's live runs. The
  long-lived backend registry in Deferred is what would give them a
  seam.

## Next

No chunk is open. Done work, with the decisions and test queries
worth keeping, is in LOG.md (from "Archived from PLAN
(2026-10-03)" on); the full chunk texts are in git history before
this commit.

## Observations

Still-open points from the five evaluation sessions (2026-03); the
sessions themselves are summarised in LOG "Archived from PLAN
(2026-10-03)". None has been re-measured since the 2026-09-17
rethink, which changed the default backend set, so re-run the
session-5 queries before building anything on them.

- **Crossref polysemy noise**---session 5 called it the largest
  remaining quality issue: common English words ("second",
  "class", "expression", "STEM", "avoidance") match highly-cited
  STEM papers (PLINK, GSEA, edgeR, stem cells) whose abstracts
  really do contain the word in another sense, so relevance
  heuristics cannot catch them. The answer so far is prompting
  (author surnames, `backends: ["openalex"]` with `domain`). Since
  the rethink OpenAlex and Europe PMC carry abstracts, so Crossref
  may matter less; measure before acting.
- **The `domain` description omits Europe PMC**---it says only
  Crossref and Semantic Scholar pass through unfiltered, but
  Europe PMC (in the default set since 2026-09-17) ignores the
  domain filter too (`internal/backends/europepmc.go`). Small
  wording fix in `cmd/search.go`.
- **Title-based `citation_graph` seed resolution** is the weakest
  link when Semantic Scholar is down ("Buss 1993 Sexual Strategies
  Theory" resolved via OpenAlex to Conroy-Beam 2015, then garbage
  citers). Only the verify→graph prompt works around it; a
  title-similarity check on the resolved seed would catch it.
- **Preprint/published duplicates** ("Boys Do Cry", twice across
  sessions) survive the title dedup, since the titles differ too.
  A token-Jaccard pass (~0.8) was deferred as O(n²) for a problem
  seen twice; build it if it recurs.

## Deferred

- **Long-lived backend registry** (the split's registry registers
  sources but still builds each backend per call)---the tools layer
  constructs every backend afresh per call. `ensureRateLimit` and
  the GET cache (2026-09-17) remove the practical harm (limiter
  reset, a source logging in again on every call), but the right
  structure is one registry built at startup from config and handed
  to the tools, which also makes per-backend timing in the access
  log trivial. Blast radius: the
  `FetchContext`/`VerifyCitation`/`FetchCitationGraph`/
  `CheckBackends` signatures and their tests.

- **Wikipedia: REST endpoints and a looser relevance filter**---
  `/w/rest.php/v1/search/page` + `/api/rest_v1/page/summary/{title}`
  are stable, cheaper, and give clean extracts. Separately,
  `isRelevant` demands ~40% of query terms appear in the intro
  paragraph, which drops correct articles on longer queries.
  Do this only if the User-Agent fix (the Rethink chunk) hasn't
  already settled the flakiness.

- **Per-token scoping and audit**---one token unlocks downloads,
  Consensus and Exa spend with no per-caller budget. A
  comma-separated `GROUNDING_AUTH_TOKENS` with a label per token
  (logged) would give revocability and attribution cheaply.

- **Unpaywall-only download for the public tier**---the legal OA
  path needs no add-on key and no quota; a public
  `find_open_access` (or `find_download_link` on the public tier,
  which in core is Unpaywall alone) would be safe to expose.

- **OpenCitations size cap**---`citations/doi:…` returns every
  citing edge (no server-side limit, ~365 bytes each); papers with
  more than ~44k citations exceed the 16MB body cap and the
  fallback errors. Degrade to `CitationCount` alone, or stream.

### Assessed, not worth building

Kept here, not in LOG, because these are the ideas a planning
session is most likely to propose again.

- **Co-citation analysis**---needs ~22 SS requests per invocation
  (1 seed + 1 citations + N reference lookups). At 1 RPS with 429s
  after 10--15 queries, structurally incompatible with the rate
  limit. The LLM can reconstruct rough clusters from shared authors,
  years, and topics in a normal `citation_graph` call.
- **Longer extracts**---`exintro=1` and `exchars=N` are mutually
  exclusive in the MediaWiki API. Switching to `exchars=1500` would
  _cap_ extracts at 1500 chars, which is _shorter_ than the full
  lead section for major articles (3000--8000 chars). For short
  articles it pulls in body text, but Wikipedia's lead section is
  editorially designed as a self-contained summary---it's the
  right unit of grounding. Not an upgrade; a tradeoff that harms
  the best results to marginally improve the weakest.
- **Ambiguity fallback**---misdiagnoses the problem. For short
  queries the Wikipedia filter is already lenient (1-term query
  needs 1 match). The issue is Wikipedia's search ranking, not
  the relevance filter. Threading `source_type` into the backend
  is architecturally messy for a brittle heuristic ("memory
  psychology" doesn't surface the main Memory article).
- **Seminal works mode**---already 80% delivered. The citation-
  sorted blend leg, `min_citations` filter, log-weighted
  `QualityScore`, and `TopCited` in `ResultSummary` cover this.
  The existing knobs are the right interface; a dedicated mode
  would add complexity for marginal gain.
- **Language/region bias control**---"non-Western institutions"
  is not the same problem as "non-English text". English-language
  cross-cultural studies from Singapore or Croatia are valuable
  grounding material. Only worth revisiting if actual non-English
  abstracts are confirmed to appear and confuse the LLM consumer.
  Default-to-English would be a bias baked into the tool's posture.
- **Lean toward recall (Wikipedia)**---the current filter was
  iteratively tuned and is already fairly permissive (proportional
  term matching with title-match reduction). The one possible
  micro-tweak (title-match threshold for short queries) is 2 lines
  but marginal in practice.
- **Local installation for people using the server.** The remote
  MCP server is the distribution path; the CLI is for development
  only (LOG "Public instance for people dorian points at it").
- **Semantic Scholar API key infrastructure.** A key gives 1 RPS
  per key instead of a share of the global unauthenticated pool,
  which would help---but applications from third-party apps are
  not approved (their Nov 2024 note) and none has arrived. Since
  2026-09-17 SS is not load-bearing; don't wait for it. The
  2026-11-16 log baseline bears it out: 3 answers in 99 attempts,
  every search carried by OpenAlex and Crossref, and our 1 req/s
  limit is right.
- **Google Books, JSTOR, Scopus, CORE, arXiv.** Europe PMC,
  Unpaywall, OpenCitations and Exa were added 2026-09-17 because
  each closes a specific gap (abstracts without Semantic Scholar;
  legal OA copies; a citation-graph fallback; web text) at no key
  or a free key. Beyond those: CORE is 100 tokens/day anonymous
  with no full text; arXiv is the wrong fields; Scopus/Dimensions
  are institutional; Scite ($50/mo) has a genuinely useful
  supporting/contrasting signal if `check_citation` ever wants it.
  Google Books was also found unnecessary for verification:
  OpenLibrary verified all four test trade books at 1.0.
- **Full-text extraction from PDFs for abstracts.** Academic PDF
  structure is too inconsistent for reliable abstract extraction;
  a catalogue record's own description is a better source.
