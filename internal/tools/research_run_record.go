package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

// Every research_question run is kept as a record: what was asked, by
// whom, on what model, what the researcher did (the whole Claude Code
// stream, or the whole conversation on the API), the ledger, the
// backbrief and what the caller was handed. The records are the input
// for improving the researcher: runs to read, replay and, later, grade
// (PLAN, "Research agent follow-ups").
//
// A record holds the question and whole pages, and questions can be
// personal, so the directories are the server user's alone (0700, files
// 0600), and records older than the retention are deleted when the next
// run is kept. Writing a record never fails a run: a run whose record
// cannot be written still answers, and says why on stderr.

// ResearchRunRecordsDefaultKeepDays is how long a record is kept when
// GROUNDING_RESEARCH_RUN_RECORDS_KEEP_DAYS is unset.
const ResearchRunRecordsDefaultKeepDays = 90

// The files in a run's directory. Only those the run produced are
// written.
const (
	researchRunRecordFile           = "run.json"
	researchRunClaudeCodeStream     = "claude-code-stream.jsonl"
	researchRunClaudeCodeStderr     = "claude-code-stderr.txt"
	researchRunAPIConversationFile  = "api-conversation.json"
	researchRunLedgerFile           = "ledger.json"
	researchRunBackbriefFile        = "backbrief.json"
	ResearchRunReturnedToCallerFile = "returned-to-caller.txt"
)

// ResearchRunCaller is who asked, as far as the server knows. The
// server has one full-tier token, so it cannot name the caller by its
// token; the MCP client's own name and the address it called from are
// what tell callers apart.
type ResearchRunCaller struct {
	// Through is "mcp" (a tool call) or "cli" (grounding
	// research-question).
	Through       string `json:"through"`
	Tier          string `json:"tier,omitempty"`
	ClientAddress string `json:"client_address,omitempty"`
	ClientName    string `json:"client_name,omitempty"`
	ClientVersion string `json:"client_version,omitempty"`
}

// ResearchRunRecord is one run's run.json, plus the run's artefacts,
// which are written beside it as files of their own.
type ResearchRunRecord struct {
	// SessionID is grounding's research session ID, the one the
	// caller is shown.
	SessionID string `json:"session_id"`
	// ClaudeCodeSessionID is Claude Code's own ID for the run.
	ClaudeCodeSessionID string            `json:"claude_code_session_id,omitempty"`
	Runner              string            `json:"runner"` // "claude-code" or "claude-api"
	GroundingVersion    string            `json:"grounding_version"`
	ClaudeCodeVersion   string            `json:"claude_code_version,omitempty"`
	Caller              ResearchRunCaller `json:"caller"`
	Question            string            `json:"question"`
	Model               string            `json:"model"`
	// ModelReportedByRunner is the model the run says it used: Claude
	// Code's resolution of an alias, or the model a refusal retry moved
	// to.
	ModelReportedByRunner string    `json:"model_reported_by_runner,omitempty"`
	Effort                string    `json:"effort"`
	StartedAt             time.Time `json:"started_at"`
	FinishedAt            time.Time `json:"finished_at"`
	DurationSeconds       float64   `json:"duration_seconds"`
	// Outcome is "answered", "failed", "timed-out", or
	// "cancelled-by-caller" (the caller's request ended before the run
	// did, which stops the run).
	Outcome           string   `json:"outcome"`
	Error             string   `json:"error,omitempty"`
	Turns             int      `json:"turns"`
	ToolCalls         int      `json:"tool_calls"`
	FailedToolCalls   []string `json:"failed_tool_calls,omitempty"`
	SourcesRetrieved  int      `json:"sources_retrieved"`
	CostUSD           float64  `json:"cost_usd"`
	CostKnown         bool     `json:"cost_known"`
	RunOnSubscription bool     `json:"run_on_subscription"`
	StoppedEarly      string   `json:"stopped_early,omitempty"`
	RefusalRetry      string   `json:"refusal_retry,omitempty"`
	Findings          int      `json:"findings"`
	FindingsSupported int      `json:"findings_supported"`
	TextAddressedToAI int      `json:"text_addressed_to_ai"`

	claudeCodeStream    []byte
	claudeCodeStderr    []byte
	apiConversation     []byte
	ledger              []byte
	backbrief           []byte
	returnedToCaller    string
	runnerSetTheOutcome bool
}

// newResearchRunRecord starts a run's record from what was asked.
func newResearchRunRecord(opts ResearchQuestionOptions, startedAt time.Time) *ResearchRunRecord {
	return &ResearchRunRecord{
		GroundingVersion: backends.Version, Caller: opts.Caller,
		Question: strings.TrimSpace(opts.Question), Model: opts.Model, Effort: opts.Effort,
		StartedAt: startedAt.UTC(),
	}
}

// fillFromBudget copies what the runner reports a run spent.
func (record *ResearchRunRecord) fillFromBudget(budget ResearchRunBudget) {
	record.SessionID = budget.SessionID
	record.Model, record.Effort = budget.Model, budget.Effort
	record.Turns, record.ToolCalls, record.SourcesRetrieved = budget.Turns, budget.ToolCalls, budget.SourcesRetrieved
	record.FailedToolCalls = budget.FailedToolCalls
	record.CostUSD, record.CostKnown, record.RunOnSubscription = budget.CostUSD, budget.CostKnown, budget.RunOnSubscription
	record.StoppedEarly, record.RefusalRetry = budget.StoppedEarly, budget.RefusalRetry
}

// fillFromVerifiedBackbrief counts the findings as the caller is shown
// them.
func (record *ResearchRunRecord) fillFromVerifiedBackbrief(verified VerifiedResearchBackbrief) {
	record.Findings = len(verified.Findings)
	record.FindingsSupported = 0
	for _, finding := range verified.Findings {
		if finding.Supported() {
			record.FindingsSupported++
		}
	}
	record.TextAddressedToAI = len(verified.Backbrief.TextAddressedToAI)
}

// setOutcome is for a runner that knows why a run ended better than
// finish can tell from the error.
func (record *ResearchRunRecord) setOutcome(outcome string) {
	record.Outcome, record.runnerSetTheOutcome = outcome, true
}

// finish closes the record on what the caller was handed.
func (record *ResearchRunRecord) finish(ctx context.Context, returned string, runErr error, finishedAt time.Time) {
	record.FinishedAt = finishedAt.UTC()
	record.DurationSeconds = finishedAt.Sub(record.StartedAt).Seconds()
	record.returnedToCaller = returned
	if runErr != nil {
		record.Error = runErr.Error()
	}
	if record.runnerSetTheOutcome {
		return
	}
	switch {
	case runErr == nil:
		record.Outcome = "answered"
	case errors.Is(ctx.Err(), context.Canceled):
		record.Outcome = "cancelled-by-caller"
	default:
		record.Outcome = "failed"
	}
}

// ResearchRunRecordsDirectory is where this server keeps its runs, or ""
// when keeping them is turned off: GROUNDING_RESEARCH_RUN_RECORDS_DIR,
// or by default research-runs under systemd's state directory for the
// service, or under the user's XDG state directory.
func ResearchRunRecordsDirectory(cfg config.Config) string {
	switch configured := strings.TrimSpace(cfg.ResearchRunRecordsDirectory); configured {
	case "off":
		return ""
	case "":
	default:
		return configured
	}
	stateDirectory := config.StateDirectory()
	if stateDirectory == "" {
		return ""
	}
	return filepath.Join(stateDirectory, "research-runs")
}

// researchRunRecordsKeepDuration is the retention, from
// GROUNDING_RESEARCH_RUN_RECORDS_KEEP_DAYS; 0 there keeps every record.
func researchRunRecordsKeepDuration(cfg config.Config) time.Duration {
	days := ResearchRunRecordsDefaultKeepDays
	if cfg.ResearchRunRecordsKeepDays != nil {
		days = *cfg.ResearchRunRecordsKeepDays
	}
	return time.Duration(days) * 24 * time.Hour
}

// researchRunDirectoryName sorts by start time and carries the session
// ID, so a run is found by either.
func researchRunDirectoryName(record *ResearchRunRecord) string {
	return record.StartedAt.UTC().Format("20060102T150405Z") + "-" + record.SessionID
}

// WriteResearchRunRecord writes a run's directory under directory and
// returns its path. The directory is written under a temporary name and
// renamed, so a listing never sees half a run.
func WriteResearchRunRecord(directory string, record *ResearchRunRecord) (string, error) {
	if record.SessionID == "" {
		record.SessionID = newResearchSessionID()
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	// MkdirAll leaves an existing directory's mode alone, and the
	// records must stay the server user's alone.
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", err
	}
	runDirectory := filepath.Join(directory, researchRunDirectoryName(record))
	writing, err := os.MkdirTemp(directory, ".writing-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(writing)

	encodedRecord, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", err
	}
	files := map[string][]byte{
		researchRunRecordFile:           append(encodedRecord, '\n'),
		researchRunClaudeCodeStream:     record.claudeCodeStream,
		researchRunClaudeCodeStderr:     record.claudeCodeStderr,
		researchRunAPIConversationFile:  record.apiConversation,
		researchRunLedgerFile:           record.ledger,
		researchRunBackbriefFile:        record.backbrief,
		ResearchRunReturnedToCallerFile: []byte(record.returnedToCaller),
	}
	for name, content := range files {
		if len(content) == 0 {
			continue
		}
		if err := os.WriteFile(filepath.Join(writing, name), content, 0o600); err != nil {
			return "", err
		}
	}
	if err := os.Rename(writing, runDirectory); err != nil {
		return "", err
	}
	return runDirectory, nil
}

// keepResearchRunRecord writes the run's record where this server keeps
// them and deletes those past the retention. Any failure is reported on
// stderr, never to the run's caller.
func keepResearchRunRecord(cfg config.Config, record *ResearchRunRecord, now time.Time) {
	directory := ResearchRunRecordsDirectory(cfg)
	if directory == "" {
		return
	}
	if _, err := WriteResearchRunRecord(directory, record); err != nil {
		fmt.Fprintf(os.Stderr, "research_question: the run's record could not be kept in %s: %v\n", directory, err)
		return
	}
	if keep := researchRunRecordsKeepDuration(cfg); keep > 0 {
		if err := DeleteResearchRunRecordsOlderThan(directory, now.Add(-keep)); err != nil {
			fmt.Fprintf(os.Stderr, "research_question: old run records could not be deleted from %s: %v\n", directory, err)
		}
	}
}

// DeleteResearchRunRecordsOlderThan deletes every run that started
// before cutoff, judged by its directory's name.
func DeleteResearchRunRecordsOlderThan(directory string, cutoff time.Time) error {
	names, err := researchRunDirectoryNames(directory)
	if err != nil {
		return err
	}
	for _, name := range names {
		startedAt, err := time.Parse("20060102T150405Z", strings.SplitN(name, "-", 2)[0])
		if err != nil || !startedAt.Before(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(directory, name)); err != nil {
			return err
		}
	}
	return nil
}

// researchRunDirectoryNames lists the runs' directories, oldest first.
func researchRunDirectoryNames(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// ListResearchRunRecords reads every run's run.json, oldest first. A
// directory without a readable run.json is skipped.
func ListResearchRunRecords(directory string) ([]ResearchRunRecord, error) {
	names, err := researchRunDirectoryNames(directory)
	if err != nil {
		return nil, err
	}
	var records []ResearchRunRecord
	for _, name := range names {
		record, err := ReadResearchRunRecord(filepath.Join(directory, name))
		if err == nil {
			records = append(records, record)
		}
	}
	return records, nil
}

// FindResearchRunDirectory finds a run by its directory's name, its
// session ID, or an unambiguous start of either.
func FindResearchRunDirectory(directory, wanted string) (string, error) {
	wanted = strings.TrimSpace(wanted)
	if wanted == "" {
		return "", errors.New("name a run: its session ID or directory")
	}
	names, err := researchRunDirectoryNames(directory)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, name := range names {
		_, sessionID, _ := strings.Cut(name, "-")
		if name == wanted || sessionID == wanted {
			return filepath.Join(directory, name), nil
		}
		if strings.HasPrefix(name, wanted) || strings.HasPrefix(sessionID, wanted) {
			matches = append(matches, name)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no run %q in %s", wanted, directory)
	case 1:
		return filepath.Join(directory, matches[0]), nil
	}
	return "", fmt.Errorf("%q names %d runs; give more of it", wanted, len(matches))
}

// ReadResearchRunRecord reads a run's run.json.
func ReadResearchRunRecord(runDirectory string) (ResearchRunRecord, error) {
	var record ResearchRunRecord
	encoded, err := os.ReadFile(filepath.Join(runDirectory, researchRunRecordFile))
	if err != nil {
		return record, err
	}
	err = json.Unmarshal(encoded, &record)
	return record, err
}

// ResearchRunArtefacts names the files a run's directory may hold, in
// the order they are worth reading.
var ResearchRunArtefacts = []string{
	ResearchRunReturnedToCallerFile, researchRunBackbriefFile, researchRunLedgerFile,
	researchRunClaudeCodeStream, researchRunAPIConversationFile, researchRunClaudeCodeStderr,
}
