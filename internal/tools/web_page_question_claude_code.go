package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/claudecode"
	"btr.mt/grounding/internal/config"
)

// The Claude Code page reader runs the quarantined read in headless
// Claude Code on a Claude subscription, for its holder's own use; anyone
// else runs the API reader on their own key. It has no MCP servers and no
// built-in tools, so a page that steers it can change what it says and
// nothing else, as with the API reader.
//
// What a run says outside its answer's JSON shape never reaches the
// caller: a page can steer that text as easily as the answer, and only
// the answer goes back framed as untrusted. Failures are worded here, and
// the run's own words go to the server's log.

const (
	// webPageReaderClaudeCodeModel is better than the API reader's
	// Haiku because a subscription charges for it in usage rather than
	// per call.
	webPageReaderClaudeCodeModel  = "claude-sonnet-5-5"
	webPageReaderClaudeCodeEffort = "low"

	webPageReaderClaudeCodeMaxDuration = 3 * time.Minute

	// Each read is a whole Claude Code process, so a burst of calls must
	// not start one each on a small server.
	webPageReaderClaudeCodeMaxConcurrentRuns = 3
)

var webPageReaderClaudeCodeRunSlots = make(chan struct{}, webPageReaderClaudeCodeMaxConcurrentRuns)

type claudeCodePageReader struct {
	// claudeCommand is the Claude Code executable and any leading
	// arguments (tests run a stand-in).
	claudeCommand []string
	oauthToken    string
}

// pageReaderFromConfig is Claude Code on the subscription when the token
// is set, and otherwise the API reader on ANTHROPIC_API_KEY.
func pageReaderFromConfig(cfg config.Config) pageReader {
	if cfg.ResearchClaudeCodeOAuthToken == "" {
		return backends.NewClaudePageReader(cfg.AnthropicAPIKey, "")
	}
	claudeBinary := cfg.ResearchClaudeBinary
	if claudeBinary == "" {
		claudeBinary = "claude"
	}
	return &claudeCodePageReader{claudeCommand: []string{claudeBinary}, oauthToken: cfg.ResearchClaudeCodeOAuthToken}
}

func (reader *claudeCodePageReader) ModelName() string {
	return webPageReaderClaudeCodeModel
}

// Read runs one system prompt and one user message, constrains the reply
// to schema, and returns the reply's JSON text.
func (reader *claudeCodePageReader) Read(ctx context.Context, system, user string, schema map[string]any) (string, error) {
	select {
	case webPageReaderClaudeCodeRunSlots <- struct{}{}:
		defer func() { <-webPageReaderClaudeCodeRunSlots }()
	case <-ctx.Done():
		return "", fmt.Errorf("the caller cancelled the call while it waited for the page reader: %w", ctx.Err())
	}

	encodedSchema, err := json.Marshal(schema)
	if err != nil {
		return "", err
	}
	directories, err := claudecode.NewIsolatedRunDirectories("grounding-page-reader-")
	if err != nil {
		return "", err
	}
	defer directories.Remove()

	runCtx, cancel := context.WithTimeout(ctx, webPageReaderClaudeCodeMaxDuration)
	defer cancel()
	run := claudecode.Invocation{
		Command:      reader.claudeCommand,
		OAuthToken:   reader.oauthToken,
		Directories:  directories,
		Model:        webPageReaderClaudeCodeModel,
		Effort:       webPageReaderClaudeCodeEffort,
		SystemPrompt: system,
		JSONSchema:   encodedSchema,
		Stdin:        user,
	}.Run(runCtx)

	if ctx.Err() != nil {
		return "", fmt.Errorf("the caller cancelled the call after %s, which stopped the page reader", run.Duration.Round(time.Second))
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("the page reader ran past its time limit of %s", webPageReaderClaudeCodeMaxDuration)
	}
	_, resultLine := claudecode.ReadStream(run.Stdout)
	if resultLine == nil {
		log.Printf("page reader: Claude Code stopped before its result (%v): %s", run.ExitError, firstCharacters(string(run.Stderr), 600))
		return "", errors.New("the page reader stopped before it answered; the server's log has why")
	}
	var result claudecode.Result
	if err := json.Unmarshal(resultLine, &result); err != nil {
		log.Printf("page reader: Claude Code's result was not in its JSON shape: %v", err)
		return "", errors.New("the page reader's result could not be read; the server's log has why")
	}
	if !result.HasStructuredOutput() {
		log.Printf("page reader: no answer (%s %s): %s", result.Subtype, result.TerminalReason, firstCharacters(result.Result, 600))
		return "", errors.New("the page reader finished without an answer, perhaps because it declined the page or its usage limit was reached; the server's log has why")
	}
	return string(result.StructuredOutput), nil
}
