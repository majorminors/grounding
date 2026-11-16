package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"btr.mt/grounding/internal/claudecode"
	"btr.mt/grounding/internal/config"
)

// The Claude Code runner runs the researcher in headless Claude Code
// (`claude -p`) on a Claude subscription, for its holder's own use;
// anyone else runs the API runner on their own key.
//
// The claudecode package isolates each run from the machine. Beside
// untrusted pages and a fetch tool, its one way out is one MCP server: a
// child `grounding serve` in research session mode, serving only the
// researcher's tools and keeping the run's ledger in a file.
//
// The child gets the backend keys and nothing else; Claude Code gets
// the subscription token and nothing else. Neither sees
// ANTHROPIC_API_KEY, which would make Claude Code bill the API.

// researchChildEnvironmentPrefix renames a backend key while it passes
// through Claude Code's environment, so Claude Code itself never sees
// a variable it might act on; the MCP config maps it back by name.
const researchChildEnvironmentPrefix = "GROUNDING_RESEARCH_CHILD_"

// researchChildBackendVariables are what the child grounding needs to
// reach the backends; never the Anthropic key, the auth token or a
// download directory.
var researchChildBackendVariables = []string{
	"GROUNDING_EMAIL", "SEMANTIC_SCHOLAR_API_KEY", "OPENALEX_API_KEY", "EXA_API_KEY",
	"OPENCITATIONS_TOKEN", "CONSENSUS_APP_KEY",
	"BRAVE_API_KEY", "FIRECRAWL_API_KEY", "FIRECRAWL_DAILY_CREDIT_CAP",
	"CRAWL4AI_BASE_URL", "CRAWL4AI_API_TOKEN",
}

type researchClaudeCodeRunner struct {
	// claudeCommand is the Claude Code executable and any leading
	// arguments (tests run a stand-in).
	claudeCommand []string
	// groundingBinary is the grounding executable the child runs.
	groundingBinary string
	oauthToken      string
	// backendEnvironment is each backend variable's value, by name.
	backendEnvironment map[string]string
}

func researchClaudeCodeRunnerFromConfig(cfg config.Config) (*researchClaudeCodeRunner, error) {
	groundingBinary, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("the Claude Code researcher cannot find grounding's own executable: %w", err)
	}
	claudeBinary := cfg.ResearchClaudeBinary
	if claudeBinary == "" {
		claudeBinary = "claude"
	}
	backendEnvironment := map[string]string{}
	for _, name := range append(append([]string(nil), researchChildBackendVariables...), environmentVariablesResearchRunsPassOnAddedByAddOns...) {
		if value := os.Getenv(name); value != "" {
			backendEnvironment[name] = value
		}
	}
	// The child counts Firecrawl's daily credits in the parent's ledger:
	// Claude Code gives it a HOME of its own for the run, from which it
	// would find a fresh ledger, and every run could spend the cap again.
	if stateDirectory := config.StateDirectory(); stateDirectory != "" {
		backendEnvironment["STATE_DIRECTORY"] = stateDirectory
	}
	// And it logs the pages it reads beside the parent's run records
	// (web_page_read_log.go), kept as long.
	recordsDirectory := ResearchRunRecordsDirectory(cfg)
	if recordsDirectory == "" {
		recordsDirectory = "off"
	}
	backendEnvironment["GROUNDING_RESEARCH_RUN_RECORDS_DIR"] = recordsDirectory
	if cfg.ResearchRunRecordsKeepDays != nil {
		backendEnvironment["GROUNDING_RESEARCH_RUN_RECORDS_KEEP_DAYS"] = strconv.Itoa(*cfg.ResearchRunRecordsKeepDays)
	}
	return &researchClaudeCodeRunner{
		claudeCommand:      []string{claudeBinary},
		groundingBinary:    groundingBinary,
		oauthToken:         cfg.ResearchClaudeCodeOAuthToken,
		backendEnvironment: backendEnvironment,
	}, nil
}

func (runner *researchClaudeCodeRunner) research(ctx context.Context, opts ResearchQuestionOptions, now time.Time, record *ResearchRunRecord) (string, error) {
	record.Model, record.Effort = opts.Model, opts.Effort
	opts, err := normalisedResearchQuestionOptions(opts)
	if err != nil {
		return "", err
	}
	record.Model, record.Effort, record.RunOnSubscription = opts.Model, opts.Effort, true

	directories, err := claudecode.NewIsolatedRunDirectories("grounding-research-")
	if err != nil {
		return "", err
	}
	defer directories.Remove()

	sessionID := newResearchSessionID()
	record.SessionID = sessionID
	ledgerFile := filepath.Join(directories.Root, "ledger.json")
	mcpConfigFile := filepath.Join(directories.Root, "mcp.json")
	if err := runner.writeMCPConfig(mcpConfigFile, ledgerFile, sessionID, opts.Question); err != nil {
		return "", err
	}
	schema, err := json.Marshal(researchBackbriefSchema)
	if err != nil {
		return "", err
	}

	var allowedTools []string
	for _, name := range ResearchQuestionTools {
		allowedTools = append(allowedTools, "mcp__grounding__"+name)
	}
	runCtx, cancel := context.WithTimeout(ctx, researchQuestionMaxDuration)
	defer cancel()
	run := claudecode.Invocation{
		Command:               runner.claudeCommand,
		OAuthToken:            runner.oauthToken,
		Directories:           directories,
		Model:                 opts.Model,
		Effort:                opts.Effort,
		SystemPrompt:          researcherSystemPrompt(now),
		JSONSchema:            schema,
		Stdin:                 "The question:\n" + opts.Question,
		MCPConfigFile:         mcpConfigFile,
		AllowedTools:          allowedTools,
		AdditionalEnvironment: runner.renamedBackendEnvironment(),
	}.Run(runCtx)
	runErr, duration := run.ExitError, run.Duration
	stdoutText, stderrText := string(run.Stdout), string(run.Stderr)

	// The record takes whatever the run left, however it ended.
	record.claudeCodeStream, record.claudeCodeStderr = run.Stdout, run.Stderr
	record.ledger, _ = os.ReadFile(ledgerFile)
	if partialLedger, err := ReadResearchLedgerSnapshotFile(ledgerFile); err == nil {
		record.ToolCalls, record.FailedToolCalls, record.SourcesRetrieved = partialLedger.ToolCalls(), partialLedger.FailedToolCalls(), len(partialLedger.Sources())
	}
	resultLine := record.fillFromClaudeCodeStream(run.Stdout)

	// A caller that stops waiting (an MCP client's own timeout, often
	// 60 seconds) cancels the request's context, which kills Claude
	// Code: that is the caller's doing, not a fault of the run.
	if ctx.Err() != nil {
		record.setOutcome("cancelled-by-caller")
		return "", fmt.Errorf("the caller cancelled the call after %s, which stopped the researcher", duration.Round(time.Second))
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		record.setOutcome("timed-out")
		return "", fmt.Errorf("the researcher ran past its time limit of %s", researchQuestionMaxDuration)
	}
	if strings.TrimSpace(stdoutText) == "" {
		return "", fmt.Errorf("the Claude Code researcher printed nothing (%v): %s", runErr, firstCharacters(strings.TrimSpace(stderrText), 600))
	}
	if resultLine == nil {
		return "", fmt.Errorf("the Claude Code researcher stopped before its result (%v): %s", runErr, firstCharacters(strings.TrimSpace(stderrText), 600))
	}
	var result claudecode.Result
	if err := json.Unmarshal(resultLine, &result); err != nil {
		return "", fmt.Errorf("the Claude Code researcher's result was not in its JSON shape: %w: %s", err, firstCharacters(string(resultLine), 400))
	}
	record.Turns, record.CostUSD, record.CostKnown = result.NumTurns, result.TotalCostUSD, true
	record.backbrief = result.StructuredOutput
	if !result.HasStructuredOutput() {
		return "", fmt.Errorf("the Claude Code researcher did not finish with a backbrief (%s %s): %s",
			result.Subtype, result.TerminalReason, firstCharacters(strings.TrimSpace(result.Result), 600))
	}
	var backbrief ResearchBackbrief
	if err := json.Unmarshal(result.StructuredOutput, &backbrief); err != nil {
		return "", fmt.Errorf("the researcher's backbrief was not in the agreed shape: %w", err)
	}

	ledger, err := ReadResearchLedgerSnapshotFile(ledgerFile)
	if err != nil {
		return "", fmt.Errorf("the research session's ledger could not be read, so nothing can be checked: %w", err)
	}
	budget := ResearchRunBudget{
		Model: opts.Model, Effort: opts.Effort, SessionID: ledger.SessionID,
		Turns: result.NumTurns, ToolCalls: ledger.ToolCalls(), Duration: duration,
		CostUSD: result.TotalCostUSD, CostKnown: true, RunOnSubscription: true,
		SourcesRetrieved: len(ledger.Sources()), FailedToolCalls: ledger.FailedToolCalls(),
	}
	record.fillFromBudget(budget)
	verified := VerifyResearchBackbrief(backbrief, ledger)
	record.fillFromVerifiedBackbrief(verified)
	return renderResearchBackbrief(opts.Question, verified, budget), nil
}

// fillFromClaudeCodeStream takes Claude Code's session ID, model and
// version from the stream's init event, and returns the stream's
// result event, or nil when the run stopped before printing one.
func (record *ResearchRunRecord) fillFromClaudeCodeStream(stream []byte) json.RawMessage {
	initialisation, resultLine := claudecode.ReadStream(stream)
	record.ClaudeCodeSessionID, record.ModelReportedByRunner, record.ClaudeCodeVersion = initialisation.SessionID, initialisation.Model, initialisation.ClaudeCodeVersion
	return resultLine
}

// writeMCPConfig names the child grounding as Claude Code's only MCP
// server. The backend keys are written as ${…} references to the
// renamed variables, so their values never touch the disk.
func (runner *researchClaudeCodeRunner) writeMCPConfig(path, ledgerFile, sessionID, question string) error {
	childEnvironment := map[string]string{}
	for name := range runner.backendEnvironment {
		childEnvironment[name] = "${" + researchChildEnvironmentPrefix + name + "}"
	}
	encoded, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{
			"grounding": map[string]any{
				"command": runner.groundingBinary,
				"args":    runner.childServeArguments(ledgerFile, sessionID, question),
				"env":     childEnvironment,
			},
		},
	})
	if err != nil {
		return err
	}
	return os.WriteFile(path, encoded, 0o600)
}

// childServeArguments start the child grounding as a research session,
// with the question's URLs made fetchable there.
func (runner *researchClaudeCodeRunner) childServeArguments(ledgerFile, sessionID, question string) []string {
	arguments := []string{"serve", "--research-session-ledger-file", ledgerFile, "--research-session-id", sessionID}
	for _, questionURL := range URLsInText(question) {
		arguments = append(arguments, "--research-session-fetchable-url", questionURL)
	}
	return arguments
}

// renamedBackendEnvironment is the backend keys under the names that
// carry them through Claude Code's environment to the child.
func (runner *researchClaudeCodeRunner) renamedBackendEnvironment() []string {
	var environment []string
	for name, value := range runner.backendEnvironment {
		environment = append(environment, researchChildEnvironmentPrefix+name+"="+value)
	}
	return environment
}
