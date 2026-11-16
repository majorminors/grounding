package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"btr.mt/grounding/internal/config"
)

func TestResearchRunRecordIsKeptPrivatelyAndFoundAgain(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "research-runs")
	record := newResearchRunRecord(ResearchQuestionOptions{Question: " Who coined emotional labour? ", Caller: ResearchRunCaller{Through: "mcp", ClientName: "claude-code"}}, testResearchNow)
	record.SessionID, record.Model, record.Effort = "research-0abc", "claude-opus-5-5", "medium"
	record.claudeCodeStream, record.ledger, record.backbrief = []byte(`{"type":"result"}`), []byte(`{"sources":[]}`), []byte(`{"summary":"s"}`)
	record.finish(context.Background(), "the backbrief as handed over", nil, testResearchNow.Add(17*time.Second))

	runDirectory, err := WriteResearchRunRecord(directory, record)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(runDirectory) != "20261025T120000Z-research-0abc" {
		t.Errorf("run directory %s", runDirectory)
	}
	for path, wantMode := range map[string]os.FileMode{
		directory: 0o700, runDirectory: 0o700,
		filepath.Join(runDirectory, researchRunRecordFile):           0o600,
		filepath.Join(runDirectory, researchRunClaudeCodeStream):     0o600,
		filepath.Join(runDirectory, ResearchRunReturnedToCallerFile): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != wantMode {
			t.Errorf("%s has mode %o, want %o", path, info.Mode().Perm(), wantMode)
		}
	}
	if _, err := os.Stat(filepath.Join(runDirectory, researchRunAPIConversationFile)); !os.IsNotExist(err) {
		t.Error("a file the run did not produce must not be written")
	}

	for _, name := range []string{"research-0abc", "research-0a", "20261025T12", filepath.Base(runDirectory)} {
		found, err := FindResearchRunDirectory(directory, name)
		if err != nil || found != runDirectory {
			t.Errorf("finding %q: %s, %v", name, found, err)
		}
	}
	records, err := ListResearchRunRecords(directory)
	if err != nil || len(records) != 1 {
		t.Fatalf("list: %v, %v", records, err)
	}
	if got := records[0]; got.Question != "Who coined emotional labour?" || got.Outcome != "answered" || got.DurationSeconds != 17 ||
		got.Caller.ClientName != "claude-code" || got.Model != "claude-opus-5-5" {
		t.Errorf("record read back as %+v", got)
	}
}

func TestResearchRunRecordsPastTheRetentionAreDeleted(t *testing.T) {
	directory := t.TempDir()
	keepDays := 30
	cfg := config.Config{ResearchRunRecordsDirectory: directory, ResearchRunRecordsKeepDays: &keepDays}
	old := &ResearchRunRecord{SessionID: "research-old", StartedAt: testResearchNow.Add(-31 * 24 * time.Hour)}
	if _, err := WriteResearchRunRecord(directory, old); err != nil {
		t.Fatal(err)
	}
	keepResearchRunRecord(cfg, &ResearchRunRecord{SessionID: "research-new", StartedAt: testResearchNow}, testResearchNow)
	records, _ := ListResearchRunRecords(directory)
	if len(records) != 1 || records[0].SessionID != "research-new" {
		t.Errorf("after the sweep: %+v", records)
	}

	keepForever := 0
	cfg.ResearchRunRecordsKeepDays = &keepForever
	WriteResearchRunRecord(directory, old)
	keepResearchRunRecord(cfg, &ResearchRunRecord{SessionID: "research-newer", StartedAt: testResearchNow}, testResearchNow)
	if records, _ := ListResearchRunRecords(directory); len(records) != 3 {
		t.Errorf("0 days keeps every record: %d kept", len(records))
	}
}

func TestResearchRunRecordsDirectoryDefaultsAndOff(t *testing.T) {
	t.Setenv("STATE_DIRECTORY", "")
	t.Setenv("XDG_STATE_HOME", "")
	home, _ := os.UserHomeDir()
	if got := ResearchRunRecordsDirectory(config.Config{}); got != filepath.Join(home, ".local", "state", "grounding", "research-runs") {
		t.Errorf("default %s", got)
	}
	t.Setenv("XDG_STATE_HOME", "/xdg")
	if got := ResearchRunRecordsDirectory(config.Config{}); got != "/xdg/grounding/research-runs" {
		t.Errorf("XDG %s", got)
	}
	t.Setenv("STATE_DIRECTORY", "/var/lib/grounding:/var/lib/other")
	if got := ResearchRunRecordsDirectory(config.Config{}); got != "/var/lib/grounding/research-runs" {
		t.Errorf("systemd %s", got)
	}
	if got := ResearchRunRecordsDirectory(config.Config{ResearchRunRecordsDirectory: "/srv/runs"}); got != "/srv/runs" {
		t.Errorf("configured %s", got)
	}
	if got := ResearchRunRecordsDirectory(config.Config{ResearchRunRecordsDirectory: "off"}); got != "" {
		t.Errorf("off %s", got)
	}
	if got := researchRunRecordsKeepDuration(config.Config{}); got != ResearchRunRecordsDefaultKeepDays*24*time.Hour {
		t.Errorf("default retention %s", got)
	}
}

func TestResearchRunRecordOutcomes(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, test := range map[string]struct {
		ctx  context.Context
		err  error
		want string
	}{
		"answered":  {context.Background(), nil, "answered"},
		"failed":    {context.Background(), errors.New("no backbrief"), "failed"},
		"cancelled": {cancelled, errors.New("killed"), "cancelled-by-caller"},
	} {
		record := &ResearchRunRecord{StartedAt: testResearchNow}
		record.finish(test.ctx, "", test.err, testResearchNow)
		if record.Outcome != test.want {
			t.Errorf("%s: outcome %s", name, record.Outcome)
		}
	}
	record := &ResearchRunRecord{StartedAt: testResearchNow}
	record.setOutcome("timed-out")
	record.finish(context.Background(), "", errors.New("time limit"), testResearchNow)
	if record.Outcome != "timed-out" || !strings.Contains(record.Error, "time limit") {
		t.Errorf("the runner's own outcome must stand: %+v", record)
	}
}

func TestAPIRunnerKeepsTheConversationInTheRecord(t *testing.T) {
	registerFakeResearchTools(t)
	var calls []string
	model := &fakeResearcherModel{replies: func(turn int, _ anthropic.MessageNewParams) string {
		if turn == 0 {
			return toolUseReply(`"web_search","input":{"query":"emotional labour"}`)
		}
		return backbriefReply(ResearchBackbrief{Summary: "Hochschild."})
	}}
	record := &ResearchRunRecord{}
	if _, err := researchQuestion(context.Background(), model, fakeResearchExecutor(t, &calls), ResearchQuestionOptions{Question: "q"}, testResearchNow, record); err != nil {
		t.Fatal(err)
	}
	conversation := string(record.apiConversation)
	for _, want := range []string{`"system"`, `"tool_result"`, `"replies"`, `Hochschild.`} {
		if !strings.Contains(conversation, want) {
			t.Errorf("conversation missing %s", want)
		}
	}
	if record.Turns != 2 || record.ToolCalls != 1 || !strings.Contains(string(record.ledger), "emotional-labour") || !strings.Contains(string(record.backbrief), "Hochschild.") {
		t.Errorf("record %+v", record)
	}
}
