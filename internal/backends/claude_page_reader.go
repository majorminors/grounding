package backends

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// ClaudePageReader is the quarantined reader behind
// answer_question_from_web_page: a separate Claude model that reads a
// fetched page and answers one question about it, so the calling agent
// never sees the page itself (the dual-LLM pattern; Claude Code's
// WebFetch works the same way). The reader gets no tools, so a page
// that steers it can change what it says and nothing else, and its
// answer comes back in a fixed JSON shape rather than free prose.
//
// Paid per call (Haiku 5.5: $0.10 per million input tokens, $0.50 per
// million output for prompts up to 100K tokens, at the 2026-10 list
// price), so full tier only.

const (
	ClaudePageReaderModel = "claude-haiku-5-5"

	// The answer is a few paragraphs and a handful of quotes. Haiku 5.5
	// thinks by default and its thinking counts toward this cap, so it
	// leaves room for both while keeping a non-streaming call well
	// inside the SDK's timeouts.
	claudePageReaderMaxTokens = 16000

	// Reading one page against one question needs little deliberation;
	// low effort lets the model think briefly or not at all.
	claudePageReaderEffort = anthropic.OutputConfigEffortLow

	claudePageReaderRPS = 2.0
)

// ClaudePageReader calls the Messages API.
type ClaudePageReader struct {
	client     anthropic.Client
	configured bool
}

// NewClaudePageReader creates a reader. An empty apiKey is allowed;
// Read then fails with a message naming the variable to set. baseURL
// is for tests and is empty otherwise.
func NewClaudePageReader(apiKey, baseURL string) *ClaudePageReader {
	ensureRateLimit("anthropic", claudePageReaderRPS, 2)
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL), option.WithMaxRetries(0))
	}
	return &ClaudePageReader{client: anthropic.NewClient(opts...), configured: apiKey != ""}
}

// ModelName is the model that reads the page.
func (r *ClaudePageReader) ModelName() string {
	return ClaudePageReaderModel
}

// Read sends one system prompt and one user message, constrains the
// reply to schema, and returns the reply's JSON text.
func (r *ClaudePageReader) Read(ctx context.Context, system, user string, schema map[string]any) (string, error) {
	if !r.configured {
		return "", errors.New("the page reader is not configured: neither ANTHROPIC_API_KEY nor GROUNDING_RESEARCH_CLAUDE_CODE_OAUTH_TOKEN is set")
	}
	if limiter := getRateLimiter("anthropic"); limiter != nil {
		if err := limiter.Wait(ctx); err != nil {
			return "", err
		}
	}

	message, err := r.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     ClaudePageReaderModel,
		MaxTokens: claudePageReaderMaxTokens,
		System:    []anthropic.TextBlockParam{{Text: system}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(user)),
		},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: claudePageReaderEffort,
			Format: anthropic.JSONOutputFormatParam{Schema: schema},
		},
	})
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			switch apiErr.StatusCode {
			case http.StatusTooManyRequests, 529:
				return "", fmt.Errorf("the page reader is overloaded or rate-limited (HTTP %d); try again shortly", apiErr.StatusCode)
			case http.StatusUnauthorized, http.StatusForbidden:
				return "", fmt.Errorf("the page reader's API key was refused (HTTP %d)", apiErr.StatusCode)
			default:
				return "", fmt.Errorf("the page reader failed (HTTP %d): %s", apiErr.StatusCode, firstCharacters(apiErr.RawJSON(), 400))
			}
		}
		return "", fmt.Errorf("the page reader failed: %w", err)
	}

	switch message.StopReason {
	case anthropic.StopReasonRefusal:
		return "", errors.New("the page reader declined to answer about this page")
	case anthropic.StopReasonMaxTokens:
		return "", errors.New("the page reader's answer ran past its length limit; ask a narrower question")
	}
	for _, block := range message.Content {
		if text, ok := block.AsAny().(anthropic.TextBlock); ok {
			return text.Text, nil
		}
	}
	return "", errors.New("the page reader returned no answer")
}
