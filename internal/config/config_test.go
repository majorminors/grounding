package config

import (
	"os"
	"testing"
)

func TestLoadConfig_Defaults(t *testing.T) {
	// Clear any existing env vars
	os.Unsetenv("GROUNDING_EMAIL")
	os.Unsetenv("SEMANTIC_SCHOLAR_API_KEY")

	cfg := LoadConfig()

	if cfg.Email != "" {
		t.Errorf("expected empty Email, got %q", cfg.Email)
	}
	if cfg.SemanticScholarKey != "" {
		t.Errorf("expected empty SemanticScholarKey, got %q", cfg.SemanticScholarKey)
	}
}

func TestLoadConfig_FromEnv(t *testing.T) {
	os.Setenv("GROUNDING_EMAIL", "test@example.com")
	os.Setenv("SEMANTIC_SCHOLAR_API_KEY", "secret-key-123")
	defer func() {
		os.Unsetenv("GROUNDING_EMAIL")
		os.Unsetenv("SEMANTIC_SCHOLAR_API_KEY")
	}()

	cfg := LoadConfig()

	if cfg.Email != "test@example.com" {
		t.Errorf("expected Email 'test@example.com', got %q", cfg.Email)
	}
	if cfg.SemanticScholarKey != "secret-key-123" {
		t.Errorf("expected SemanticScholarKey 'secret-key-123', got %q", cfg.SemanticScholarKey)
	}
}

func TestStateDirectoryPrefersGroundingsOwnSettingThenSystemdThenXDG(t *testing.T) {
	t.Setenv("GROUNDING_STATE_DIRECTORY", "/var/lib/grounding")
	t.Setenv("STATE_DIRECTORY", "/var/lib/systemd-state:/other")
	t.Setenv("XDG_STATE_HOME", "/xdg")
	if got := StateDirectory(); got != "/var/lib/grounding" {
		t.Errorf("own setting: got %q", got)
	}
	t.Setenv("GROUNDING_STATE_DIRECTORY", "")
	if got := StateDirectory(); got != "/var/lib/systemd-state" {
		t.Errorf("systemd's: got %q", got)
	}
	t.Setenv("STATE_DIRECTORY", "")
	if got := StateDirectory(); got != "/xdg/grounding" {
		t.Errorf("XDG: got %q", got)
	}
}

func TestFirecrawlDailyCreditCapDefaultsAndOff(t *testing.T) {
	for value, want := range map[string]int{"": DefaultFirecrawlDailyCreditCap, "off": 0, "OFF": 0, "40": 40, "0": DefaultFirecrawlDailyCreditCap, "-3": DefaultFirecrawlDailyCreditCap, "lots": DefaultFirecrawlDailyCreditCap} {
		t.Setenv("FIRECRAWL_DAILY_CREDIT_CAP", value)
		if got := LoadConfig().FirecrawlDailyCreditCap; got != want {
			t.Errorf("%q: want %d, got %d", value, want, got)
		}
	}
}
