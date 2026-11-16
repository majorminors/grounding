# Agent instructions

Located at ./AGENTS.md. Symlinked at CLAUDE.md and GEMINI.md.

## Project: grounding

Go CLI + MCP server for grounding AI reasoning. See README.md for architecture.

## Planning conventions

- `PLAN.md` --- future items and chunks. Sections: Completed, Next,
  Observations, Deferred.
- `LOG.md` --- decisions, patterns, and operational knowledge worth
  persisting across sessions (rate limiting, backends, deployment,
  known limitations).

Both live in the root and are public, like the code: read them at
the start of a session if context isn't otherwise clear. They are
written generically, so nothing about a particular deployment goes
in them either (see the last item under "Working Here").

## Pick-up

_This block is a fragment, not the procedure._ Run it **with** the
generic `pick-up` skill, which owns the git, planning-doc and
chunking steps. If that skill isn't in your runtime, say so and ask
for it---do not improvise an orientation from this block.

Todo tags: `+grounding`

## Commands

```bash
go build -o grounding .     # Build
go build -ldflags "-X btr.mt/grounding/cmd.version=$(git describe --tags --always)" -o grounding .  # Build with version stamp
go test -short ./...        # Test (offline; httptest only)
go test ./...               # Test including live-API smoke tests
./grounding --help          # CLI usage
./grounding schema          # Dump MCP tool definitions
```

Env vars are listed in README.md. The binary does not read `.env`.

## Working Here

- Business logic goes in `internal/tools/*.go`
- Handlers and CLI commands go in `cmd/*.go`
- New backends implement interfaces in `internal/backends/types.go`
- Tool registration happens in `cmd/*.go` `init()` functions
- Relevance filtering and quality scoring live in `internal/tools/relevance.go`
- Text normalisation utilities in `internal/backends/text.go`
- `http.go` is the shared client: per-backend rate limiters
  (constructors call `ensureRateLimit`, first config wins), 429
  retry + cooldown for every backend, the contact email in the
  User-Agent (that is how Crossref's polite pool and Wikimedia's
  identified tier are earned), and a server-only GET cache
- Full-tier-only tools set `RequiresAuth: true` on their `ToolDef`;
  that one flag hides them from public `tools/list` and the landing
  page, and the handler should still check the tier
- Anything that names a paid or hidden backend goes in
  `FullTierDescription`, never in `Description` or a parameter
  description: parameter text is shown to every caller.
  `TestPublicToolParametersNameNoHiddenBackend` in `cmd` enforces it
- Core uses only sanctioned APIs, within their terms; anything grey
  goes in an add-on, a separate module that registers through core's
  hooks (README "Add-ons"). Core never names an add-on's backend, in
  code or docs: `TestCoreNamesNoBackendThatBelongsInAnAddOn` enforces it
- Paid or slow backends (Consensus, Exa, and any an add-on adds) are opt-in
  by name, never in a default set. The one exception is Firecrawl as
  the `auto` renderer's last rung (2026-11-07), and only
  because `FIRECRAWL_DAILY_CREDIT_CAP` bounds it: a paid backend that
  runs unasked needs the operator's say and a cap of its own
- Two audiences, two texts: `Description` is for the model;
  `Title` and `SummaryForPeople` are for people (landing page,
  `/llms.txt`, client tool pickers). Every tool needs both, plus a
  deliberate `ReadOnly` (true unless it writes a file); it becomes
  the MCP `readOnlyHint`. `TestEveryToolIsDescribedForPeopleAndAnnotated`
  enforces it, and the hidden-backend rule covers the people text too
- Text a tool returns from the open web goes through
  `tools.FramedAsExternalContent`, and its description carries
  `tools.UntrustedExternalContentDescriptionSentence`; never compose a
  second frame. A URL that comes from a caller is fetched only through
  `backends.WebPageFetcher` (its dial-time guard refuses loopback,
  private and mesh addresses), or checked with
  `backends.WebURLOrRefusal` before it is handed to a third party.
  A browser renderer (crawl4ai, Firecrawl) also loads whatever the
  page asks for, from its own host: the pre-check covers the URL
  alone, so the renderer's host must refuse the rest (README
  "The browser renderer: crawl4ai")
- Public-tier tool calls pass through a per-client limiter
  (`cmd/public-tier-call-limit.go`); full tier is exempt. Keep new
  HTTP handlers behind the same wrapper chain in `serve.go`
- README.md is also the landing page: its part above `<!-- the
  landing page ends here -->` is rendered for each visitor, with the
  sections marked `generated` filled from the tool registry
  (`cmd/landing-page-and-readme-are-one-document.go`). Edit the
  README, never a copy; after editing it or any tool's text, run
  `go test ./cmd -run TestTheReadmeIsCurrentAndTheLandingPageHasItsCopy -update-readme`
- `research_question`'s researcher gets only the tools in
  `tools.ResearchQuestionTools`. Each records what it shows into the
  run's ledger (`tools.ResearchLedgerFromContext`), and anything that
  fetches a caller-reachable URL checks `URLMayBeFetched` first.
  Adding a tool to that list widens what a hostile page can steer the
  researcher into: treat it as a security decision, not a
  convenience
- Every `research_question` run is kept as a record
  (`internal/tools/research_run_record.go`, README "Research run
  records"): the input for improving the researcher. Something new a
  runner learns about a run goes into its record; a record must never
  fail a run, and must stay the server user's alone
- The code repository is public: the forge mirrors every push to
  GitHub. So nothing about a deployment, a server's host, a network
  or a person's setup goes in the code, its comments, the README,
  PLAN.md, LOG.md or this file; that belongs with the deployment's
  own configuration. Nor does anything naming an add-on's backend:
  its history and plans go in the add-on's own LOG and PLAN.
  `TestPublicCodeCarriesNoPrivateName` checks every tracked file
  against a list of private names kept outside this repository:
  point it at one with `GROUNDING_PRIVATE_NAMES_FILE=PATH go test
  -short ./...` (one Go regular expression per line, matched
  case-insensitively, `#` for comments). Without the variable it is
  skipped
