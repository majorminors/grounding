package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/tools"
)

func TestPublicTierCallLimiterAllowsBurstThenRefuses(t *testing.T) {
	limiter := newPublicTierCallLimiterByClientAddress(3, time.Minute, time.Hour)
	start := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	for call := 1; call <= 3; call++ {
		if allowed, _ := limiter.allow("203.0.113.7", start); !allowed {
			t.Fatalf("call %d within the burst was refused", call)
		}
	}
	allowed, wait := limiter.allow("203.0.113.7", start)
	if allowed {
		t.Fatal("call past the burst was allowed")
	}
	if wait <= 0 || wait > time.Minute {
		t.Errorf("wait = %s, want within one refill interval", wait)
	}

	// A refused call spends nothing, so one refill interval later
	// exactly one call is allowed again.
	later := start.Add(time.Minute)
	if allowed, _ := limiter.allow("203.0.113.7", later); !allowed {
		t.Error("call after one refill interval was refused")
	}
	if allowed, _ := limiter.allow("203.0.113.7", later); allowed {
		t.Error("second call after one refill interval was allowed")
	}
}

func TestPublicTierCallLimiterKeepsClientsApart(t *testing.T) {
	limiter := newPublicTierCallLimiterByClientAddress(1, time.Hour, time.Hour)
	now := time.Now()
	limiter.allow("203.0.113.7", now)
	if allowed, _ := limiter.allow("198.51.100.4", now); !allowed {
		t.Error("one client's spending refused another client's call")
	}
}

func TestPublicTierCallLimiterForgetsIdleClients(t *testing.T) {
	limiter := newPublicTierCallLimiterByClientAddress(1, time.Hour, time.Hour)
	start := time.Now()
	limiter.allow("203.0.113.7", start)
	limiter.allow("198.51.100.4", start.Add(3*time.Hour))
	if _, remembered := limiter.allowances["203.0.113.7"]; remembered {
		t.Error("client idle past forgetAfter is still held in memory")
	}
}

func TestClientAddressOfRequest(t *testing.T) {
	cases := []struct {
		name          string
		forwardedFor  string
		remoteAddress string
		want          string
	}{
		{"caddy appends the real peer rightmost", "6.6.6.6, 203.0.113.7", "127.0.0.1:50000", "203.0.113.7"},
		{"single forwarded entry", "203.0.113.7", "127.0.0.1:50000", "203.0.113.7"},
		{"no proxy header falls back to the socket", "", "198.51.100.4:41234", "198.51.100.4"},
		{"ipv6 socket", "", "[2001:db8::1]:41234", "2001:db8::1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			request.RemoteAddr = c.remoteAddress
			if c.forwardedFor != "" {
				request.Header.Set("X-Forwarded-For", c.forwardedFor)
			}
			if got := clientAddressOfRequest(request); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestPublicTierCallLimitedHandlerOnlyLimitsPublicCallers(t *testing.T) {
	limiter := newPublicTierCallLimiterByClientAddress(1, time.Hour, time.Hour)
	calls := 0
	handler := publicTierCallLimitedHandler("search_catalogue", limiter,
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls++
			return mcp.NewToolResultText("ok"), nil
		})

	public := auth.WithTier(withClientAddress(context.Background(), "203.0.113.7"), auth.TierPublic)
	handler(public, mcp.CallToolRequest{})
	result, err := handler(public, mcp.CallToolRequest{})
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("second public call: want a tool error, got result=%+v err=%v", result, err)
	}
	if calls != 1 {
		t.Errorf("refused call still reached the tool (calls=%d)", calls)
	}

	full := auth.WithTier(withClientAddress(context.Background(), "203.0.113.7"), auth.TierFull)
	for i := 0; i < 5; i++ {
		if result, _ := handler(full, mcp.CallToolRequest{}); result.IsError {
			t.Fatal("full-tier call was rate limited")
		}
	}
}

func TestEveryToolIsDescribedForPeopleAndAnnotated(t *testing.T) {
	for _, tool := range tools.All() {
		if tool.Title == "" || tool.SummaryForPeople == "" {
			t.Errorf("%s needs a Title and a SummaryForPeople for the landing page and client pickers", tool.Name)
		}
		annotations := annotationsFor(tool)
		if annotations.ReadOnlyHint == nil || annotations.DestructiveHint == nil || annotations.OpenWorldHint == nil {
			t.Errorf("%s has an unset annotation hint", tool.Name)
		}
	}
	// The tool that finds a document's link writes nothing on the
	// server, so it says so (agents may then call it without asking).
	if findLink, _ := tools.Lookup("find_download_link"); !findLink.ReadOnly {
		t.Error("find_download_link writes nothing and must be marked ReadOnly")
	}
}

// The landing page is the other place a public caller reads tool text,
// so it gets the same leak check as tools/list.
func TestPublicLandingPageNamesNoHiddenToolOrBackend(t *testing.T) {
	recorder := httptest.NewRecorder()
	makeLandingHandler("secret-token")(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	page := recorder.Body.String()

	if match := regexp.MustCompile(`(?i)\b(consensus|exa|ann[a]|unpaywall|brave|firecrawl|haiku)\b`).FindString(page); match != "" {
		t.Errorf("public landing page names a full-tier backend: %q", match)
	}
	for _, tool := range tools.All() {
		visible := strings.Contains(page, tool.Name)
		if tool.RequiresAuth && visible {
			t.Errorf("public landing page lists full-tier tool %s", tool.Name)
		}
		if !tool.RequiresAuth && !visible {
			t.Errorf("public landing page is missing %s", tool.Name)
		}
	}
}

func TestFullTierLandingPageListsEverything(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer secret-token")
	makeLandingHandler("secret-token")(recorder, request)
	page := recorder.Body.String()
	for _, tool := range tools.All() {
		if !strings.Contains(page, tool.Name) {
			t.Errorf("full-tier landing page is missing %s", tool.Name)
		}
	}
}

// llms.txt is a third place public callers read tool text.
func TestPublicLlmsTxtNamesAuthorAndNoHiddenToolOrBackend(t *testing.T) {
	recorder := httptest.NewRecorder()
	makeLlmsTxtHandler("secret-token")(recorder, httptest.NewRequest(http.MethodGet, "/llms.txt", nil))
	body := recorder.Body.String()

	for _, want := range []string{"# Grounding", "Dorian Minors", "https://grounding.btr.mt/mcp", essayURL} {
		if !strings.Contains(body, want) {
			t.Errorf("llms.txt is missing %q", want)
		}
	}
	if match := regexp.MustCompile(`(?i)\b(consensus|exa|ann[a]|unpaywall|brave|firecrawl|haiku)\b`).FindString(body); match != "" {
		t.Errorf("public llms.txt names a full-tier backend: %q", match)
	}
	for _, tool := range tools.All() {
		if tool.RequiresAuth && strings.Contains(body, tool.Name) {
			t.Errorf("public llms.txt lists full-tier tool %s", tool.Name)
		}
	}
}

func TestLandingPageStructuredDataNamesAuthor(t *testing.T) {
	recorder := httptest.NewRecorder()
	makeLandingHandler("secret-token")(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	page := recorder.Body.String()

	match := regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`).FindStringSubmatch(page)
	if match == nil {
		t.Fatal("landing page has no JSON-LD block")
	}
	var document struct {
		Author struct {
			Name string `json:"name"`
		} `json:"author"`
	}
	if err := json.Unmarshal([]byte(match[1]), &document); err != nil {
		t.Fatalf("JSON-LD does not parse (html/template may have re-escaped it): %v\n%s", err, match[1])
	}
	if document.Author.Name != authorName {
		t.Errorf("JSON-LD author = %q, want %q", document.Author.Name, authorName)
	}
}
