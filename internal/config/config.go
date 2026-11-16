package config

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// SecretFromCommand runs command through /bin/sh and returns the first
// line it prints, trimmed (pass prints the secret on its first line).
// The command shares the terminal's stdin and stderr, so a gpg
// passphrase prompt reaches the person.
func SecretFromCommand(command string) (string, error) {
	shell := exec.Command("/bin/sh", "-c", command)
	var printed bytes.Buffer
	shell.Stdout = &printed
	shell.Stdin = os.Stdin
	shell.Stderr = os.Stderr
	if err := shell.Run(); err != nil {
		return "", fmt.Errorf("%q failed: %w", command, err)
	}
	firstLine, _, _ := strings.Cut(printed.String(), "\n")
	secret := strings.TrimSpace(firstLine)
	if secret == "" {
		return "", fmt.Errorf("%q printed nothing", command)
	}
	return secret, nil
}

// SecretFromEnvironmentOrItsCommand returns the secret in variable or,
// when that is unset and mayRunCommand (a command-line run, never the
// server), what the command in variable_COMMAND prints, which it then
// sets in variable for the rest of the process. It names both in its
// error when neither gives one.
func SecretFromEnvironmentOrItsCommand(variable string, mayRunCommand bool) (string, error) {
	if secret := os.Getenv(variable); secret != "" {
		return secret, nil
	}
	commandVariable := variable + "_COMMAND"
	command := os.Getenv(commandVariable)
	if !mayRunCommand || command == "" {
		if !mayRunCommand {
			return "", fmt.Errorf("%s not configured", variable)
		}
		return "", fmt.Errorf("set %s, or set %s to a command that prints it", variable, commandVariable)
	}
	secret, err := SecretFromCommand(command)
	if err != nil {
		return "", fmt.Errorf("%s: %w", commandVariable, err)
	}
	os.Setenv(variable, secret)
	return secret, nil
}

// Config holds configuration from environment variables.
type Config struct {
	// Email for Crossref/OpenAlex polite pool (higher rate limits).
	Email string

	// SemanticScholarKey improves rate limits on Semantic Scholar API.
	SemanticScholarKey string

	// OpenAlexAPIKey raises the OpenAlex daily budget tenfold (free).
	// Since 2026 OpenAlex meters keyless use at roughly 100 list
	// queries per day per IP, which a shared deployment exhausts fast.
	OpenAlexAPIKey string

	// ExaAPIKey enables the Exa web-search backend (paid per call).
	ExaAPIKey string

	// OpenCitationsAccessToken is optional; OpenCitations asks
	// applications to identify themselves with one.
	OpenCitationsAccessToken string

	// ConsensusAppKey is the API key for Consensus.app quick search.
	ConsensusAppKey string

	// BraveAPIKey enables web_search's keyword mode (Brave Search).
	BraveAPIKey string

	// FirecrawlAPIKey enables web_fetch's opt-in "firecrawl" renderer,
	// which runs a page's JavaScript before reading it (paid per page).
	FirecrawlAPIKey string

	// FirecrawlDailyCreditCap is the most Firecrawl credits grounding
	// spends in a UTC day, across all its processes on the host
	// (FIRECRAWL_DAILY_CREDIT_CAP; "off" for none). The "auto" renderer
	// climbs to Firecrawl unasked, so this is what bounds the bill.
	FirecrawlDailyCreditCap int

	// Crawl4AIBaseURL and Crawl4AIAPIToken reach a self-hosted crawl4ai
	// server (beside grounding, on loopback or a socket): the free browser rung of the "auto"
	// renderer, and the "crawl4ai" renderer.
	Crawl4AIBaseURL  string
	Crawl4AIAPIToken string

	// AnthropicAPIKey enables answer_question_from_web_page, whose
	// reader is a separate Claude model (paid per call).
	AnthropicAPIKey string

	// ResearchClaudeCodeOAuthToken, a `claude setup-token` token, makes
	// research_question run its researcher, and
	// answer_question_from_web_page its reader, in headless Claude Code
	// on that subscription instead of on the Claude API. Only for the
	// subscription holder's own use: Anthropic does not allow offering
	// claude.ai login to others.
	ResearchClaudeCodeOAuthToken string

	// ResearchClaudeBinary is the Claude Code executable for that
	// runner; "claude" (found on PATH) when unset.
	ResearchClaudeBinary string

	// ResearchRunRecordsDirectory is where research_question keeps a
	// record of every run; "off" keeps none, and unset means the default
	// (tools.ResearchRunRecordsDirectory).
	ResearchRunRecordsDirectory string

	// ResearchRunRecordsKeepDays is how many days a run's record is
	// kept; nil means the default, 0 keeps them all.
	ResearchRunRecordsKeepDays *int

	// AuthToken gates paid/restricted backends behind bearer auth.
	// When unset, all backends are available (local dev, stdio).
	AuthToken string
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() Config {
	return Config{
		Email:                    os.Getenv("GROUNDING_EMAIL"),
		SemanticScholarKey:       os.Getenv("SEMANTIC_SCHOLAR_API_KEY"),
		OpenAlexAPIKey:           os.Getenv("OPENALEX_API_KEY"),
		ExaAPIKey:                os.Getenv("EXA_API_KEY"),
		OpenCitationsAccessToken: os.Getenv("OPENCITATIONS_TOKEN"),
		ConsensusAppKey:          os.Getenv("CONSENSUS_APP_KEY"),
		BraveAPIKey:              os.Getenv("BRAVE_API_KEY"),
		FirecrawlAPIKey:          os.Getenv("FIRECRAWL_API_KEY"),
		FirecrawlDailyCreditCap:  parseCapEnv("FIRECRAWL_DAILY_CREDIT_CAP", DefaultFirecrawlDailyCreditCap),
		Crawl4AIBaseURL:          strings.TrimRight(os.Getenv("CRAWL4AI_BASE_URL"), "/"),
		Crawl4AIAPIToken:         os.Getenv("CRAWL4AI_API_TOKEN"),
		AnthropicAPIKey:          os.Getenv("ANTHROPIC_API_KEY"),
		AuthToken:                os.Getenv("GROUNDING_AUTH_TOKEN"),

		ResearchClaudeCodeOAuthToken: os.Getenv("GROUNDING_RESEARCH_CLAUDE_CODE_OAUTH_TOKEN"),
		ResearchClaudeBinary:         os.Getenv("GROUNDING_RESEARCH_CLAUDE_BINARY"),

		ResearchRunRecordsDirectory: os.Getenv("GROUNDING_RESEARCH_RUN_RECORDS_DIR"),
		ResearchRunRecordsKeepDays:  parseOptionalNonNegativeIntEnv("GROUNDING_RESEARCH_RUN_RECORDS_KEEP_DAYS"),
	}
}

// parseOptionalNonNegativeIntEnv is nil when key is unset or not a
// whole number of zero or more.
func parseOptionalNonNegativeIntEnv(key string) *int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v < 0 {
		return nil
	}
	return &v
}

// DefaultFirecrawlDailyCreditCap was suggested on 2026-11-07, when the
// old account ran out (LOG "Firecrawl's spend was PDFs"), and taken by
// dorian with the rest of the page-reading plan.
const DefaultFirecrawlDailyCreditCap = 25

// parseCapEnv reads a cap: "off" is 0 (no cap), and unset or anything
// but a whole number above zero is fallback.
func parseCapEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if strings.EqualFold(value, "off") {
		return 0
	}
	if v, err := strconv.Atoi(value); err == nil && v > 0 {
		return v
	}
	return fallback
}

// StateDirectory is where grounding keeps state across runs:
// GROUNDING_STATE_DIRECTORY, else systemd's state directory for the
// service, else the user's XDG state directory, else
// ~/.local/state/grounding; "" when none can be found. A unit that sets
// none of systemd's and may write only under, say, /var/lib/grounding
// names that.
func StateDirectory() string {
	if configured := strings.TrimSpace(os.Getenv("GROUNDING_STATE_DIRECTORY")); configured != "" {
		return configured
	}
	if stateDirectory := os.Getenv("STATE_DIRECTORY"); stateDirectory != "" {
		// systemd may list several, separated by colons.
		return strings.Split(stateDirectory, ":")[0]
	}
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "grounding")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "grounding")
}

func parseIntEnv(key string, fallback int) int {
	s := os.Getenv(key)
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return v
}
