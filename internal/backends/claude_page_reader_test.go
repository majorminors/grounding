package backends

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func claudeReplyServer(t *testing.T, stopReason, text string, seen *map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "sk-test" {
			t.Errorf("unexpected request %s key=%q", r.URL.Path, r.Header.Get("X-Api-Key"))
		}
		body, _ := io.ReadAll(r.Body)
		if seen != nil {
			json.Unmarshal(body, seen)
		}
		w.Header().Set("Content-Type", "application/json")
		reply, _ := json.Marshal(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": ClaudePageReaderModel,
			"content":     []map[string]any{{"type": "text", "text": text}},
			"stop_reason": stopReason,
			"usage":       map[string]any{"input_tokens": 10, "output_tokens": 5},
		})
		w.Write(reply)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestClaudePageReaderSendsModelSystemAndSchema(t *testing.T) {
	var seen map[string]any
	server := claudeReplyServer(t, "end_turn", `{"status":"answered"}`, &seen)
	reader := NewClaudePageReader("sk-test", server.URL)
	schema := map[string]any{"type": "object", "additionalProperties": false}
	reply, err := reader.Read(context.Background(), "the brief", "the page", schema)
	if err != nil {
		t.Fatal(err)
	}
	if reply != `{"status":"answered"}` {
		t.Errorf("reply %q", reply)
	}
	if seen["model"] != ClaudePageReaderModel {
		t.Errorf("model %v", seen["model"])
	}
	if _, hasTools := seen["tools"]; hasTools {
		t.Error("the quarantined reader must be given no tools")
	}
	encoded, _ := json.Marshal(seen)
	for _, want := range []string{`"system":[{"text":"the brief"`, `"output_config":{"effort":"low","format":{"schema":{"additionalProperties":false,"type":"object"},"type":"json_schema"}}`, `"the page"`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("request missing %s:\n%s", want, encoded)
		}
	}
}

func TestClaudePageReaderTreatsRefusalAndCutOffAsErrors(t *testing.T) {
	for stopReason, want := range map[string]string{"refusal": "declined", "max_tokens": "length limit"} {
		server := claudeReplyServer(t, stopReason, `{`, nil)
		_, err := NewClaudePageReader("sk-test", server.URL).Read(context.Background(), "s", "u", map[string]any{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want an error mentioning %q, got %v", stopReason, want, err)
		}
	}
}

func TestClaudePageReaderWithoutKeyNamesTheVariable(t *testing.T) {
	_, err := NewClaudePageReader("", "").Read(context.Background(), "s", "u", nil)
	if err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("want an error naming ANTHROPIC_API_KEY, got %v", err)
	}
}
