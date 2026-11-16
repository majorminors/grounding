// Package claudecode runs headless Claude Code (`claude -p`) on a Claude
// subscription, for that subscription holder's own use, isolated from
// the machine it runs on.
//
// Claude Code by default loads its user's whole setup: CLAUDE.md,
// skills, memory, settings, MCP servers. Beside untrusted pages that
// would be the very leak grounding's separate models exist to prevent,
// so every run gets a fresh, empty home, config directory and working
// directory, no setting sources, auto-memory off, no built-in tools, and
// only the MCP servers its caller names. `--bare` would do much of this
// but cannot be used: under it Claude Code reads only an API key, never
// a subscription's OAuth token (LOG "Claude Code isolation check").
//
// Claude Code's environment is built from nothing, never inherited, so
// no key of the server's reaches it; in particular not
// ANTHROPIC_API_KEY, which would make it bill the API.
package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// IsolatedRunDirectories are one run's own directories, all under Root,
// which a caller may also use for the run's other files.
type IsolatedRunDirectories struct {
	Root, Home, ConfigDirectory, WorkingDirectory string
}

// NewIsolatedRunDirectories makes a run's empty directories in a new
// temporary directory named from namePrefix.
func NewIsolatedRunDirectories(namePrefix string) (IsolatedRunDirectories, error) {
	root, err := os.MkdirTemp("", namePrefix)
	if err != nil {
		return IsolatedRunDirectories{}, err
	}
	directories := IsolatedRunDirectories{
		Root:             root,
		Home:             filepath.Join(root, "home"),
		ConfigDirectory:  filepath.Join(root, "config"),
		WorkingDirectory: filepath.Join(root, "work"),
	}
	for _, directory := range []string{directories.Home, directories.ConfigDirectory, directories.WorkingDirectory} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			os.RemoveAll(root)
			return IsolatedRunDirectories{}, err
		}
	}
	return directories, nil
}

// Remove deletes the run's directories and everything in them.
func (directories IsolatedRunDirectories) Remove() error {
	return os.RemoveAll(directories.Root)
}

// Invocation is one run of Claude Code.
type Invocation struct {
	// Command is the Claude Code executable and any leading arguments
	// (tests run a stand-in).
	Command     []string
	OAuthToken  string
	Directories IsolatedRunDirectories

	Model, Effort string
	SystemPrompt  string
	// JSONSchema is the shape the run must finish in, as JSON.
	JSONSchema []byte
	// Stdin is the prompt. It goes on stdin because --tools and
	// --allowedTools take several values and would swallow a positional
	// prompt.
	Stdin string

	// MCPConfigFile and AllowedTools are the run's only tools; a run
	// with neither has no tools at all.
	MCPConfigFile string
	AllowedTools  []string

	// AdditionalEnvironment is NAME=VALUE entries for the run's MCP
	// servers. Each should be named so Claude Code itself will not act
	// on it.
	AdditionalEnvironment []string
}

// CompletedRun is what a run left, however it ended.
type CompletedRun struct {
	Stdout, Stderr []byte
	Duration       time.Duration
	// ExitError is the error from waiting on the process: nil, or how
	// it failed. A run can print a failed result and still exit cleanly,
	// so read the result as well.
	ExitError error
}

// Run runs Claude Code until it exits or ctx ends, which kills it. The
// caller tells a deadline from a cancelled caller, each of which it
// words for itself.
func (invocation Invocation) Run(ctx context.Context) CompletedRun {
	command := exec.CommandContext(ctx, invocation.Command[0], invocation.arguments()...)
	command.Dir = invocation.Directories.WorkingDirectory
	command.Env = invocation.environment()
	command.Stdin = strings.NewReader(invocation.Stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr

	started := time.Now()
	exitError := command.Run()
	return CompletedRun{
		Stdout:    stdout.Bytes(),
		Stderr:    stderr.Bytes(),
		Duration:  time.Since(started),
		ExitError: exitError,
	}
}

func (invocation Invocation) arguments() []string {
	arguments := append(append([]string(nil), invocation.Command[1:]...),
		"-p",
		"--model", invocation.Model,
		"--effort", invocation.Effort,
		"--system-prompt", invocation.SystemPrompt,
		"--json-schema", string(invocation.JSONSchema),
		// The stream rather than its single result, so a caller can keep
		// everything the run did, and a run cut short still leaves what
		// it did up to then. Claude Code prints the stream in print mode
		// only with --verbose.
		"--output-format", "stream-json",
		"--verbose",
		"--setting-sources", "",
		"--settings", `{"autoMemoryEnabled":false}`,
		"--strict-mcp-config",
	)
	if invocation.MCPConfigFile != "" {
		arguments = append(arguments, "--mcp-config", invocation.MCPConfigFile)
	}
	arguments = append(arguments, "--tools", "")
	if len(invocation.AllowedTools) > 0 {
		arguments = append(arguments, "--allowedTools", strings.Join(invocation.AllowedTools, ","))
	}
	return append(arguments, "--no-session-persistence")
}

func (invocation Invocation) environment() []string {
	environment := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + invocation.Directories.Home,
		"CLAUDE_CONFIG_DIR=" + invocation.Directories.ConfigDirectory,
		"TMPDIR=" + invocation.Directories.Root,
		"LANG=" + os.Getenv("LANG"),
		"CLAUDE_CODE_OAUTH_TOKEN=" + invocation.OAuthToken,
		// The binary may be a pinned, root-owned install (a server's
		// package or deploy): it must not try to update itself, and a run
		// stays on the release it was given.
		"DISABLE_AUTOUPDATER=1",
		"DISABLE_UPDATES=1",
	}
	return append(environment, invocation.AdditionalEnvironment...)
}

// StreamInitialisation is what the stream's first event ("system",
// "init") says about the run.
type StreamInitialisation struct {
	SessionID         string `json:"session_id"`
	Model             string `json:"model"`
	ClaudeCodeVersion string `json:"claude_code_version"`
}

// Result is the part of the stream's result event grounding reads. The
// last line, of type "result", is what `--output-format json` would have
// printed alone.
//
// Result holds model text, so a caller reading an untrusted page must
// not pass it on as if it were grounding's own words.
type Result struct {
	IsError          bool            `json:"is_error"`
	Subtype          string          `json:"subtype"`
	Result           string          `json:"result"`
	NumTurns         int             `json:"num_turns"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	TerminalReason   string          `json:"terminal_reason"`
}

// HasStructuredOutput reports whether the run finished in its JSON
// shape.
func (result Result) HasStructuredOutput() bool {
	return !result.IsError && len(result.StructuredOutput) > 0 && string(result.StructuredOutput) != "null"
}

// ReadStream takes the run's initialisation and raw result event from
// its stream; the result is nil when the run stopped before printing
// one.
func ReadStream(stream []byte) (StreamInitialisation, json.RawMessage) {
	var initialisation StreamInitialisation
	var resultLine json.RawMessage
	for _, line := range bytes.Split(stream, []byte("\n")) {
		var event struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
		}
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		switch {
		case event.Type == "system" && event.Subtype == "init":
			json.Unmarshal(line, &initialisation)
		case event.Type == "result":
			resultLine = append(json.RawMessage(nil), line...)
		}
	}
	return initialisation, resultLine
}
