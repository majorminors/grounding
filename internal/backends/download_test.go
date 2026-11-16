package backends

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainedPath(t *testing.T) {
	dir := t.TempDir()

	inside, err := ContainedPath(dir, "d41d8cd98f00b204e9800998ecf8427e.pdf")
	if err != nil {
		t.Fatalf("plain filename should be contained: %v", err)
	}
	if filepath.Dir(inside) != dir {
		t.Errorf("expected a path directly under %s, got %s", dir, inside)
	}

	// A traversal-shaped name must be refused, not silently cleaned
	// into a path outside the download directory.
	if _, err := ContainedPath(dir, "../../etc/passwd"); err == nil {
		t.Error("expected a traversal-shaped filename to be refused")
	}

	// A relative dir is resolved against the working directory, so the
	// containment check compares like with like.
	if _, err := ContainedPath(".", "x.pdf"); err != nil {
		t.Errorf("relative dir should resolve: %v", err)
	}
}

func TestFetchToFile_WritesBodyAndReportsContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "grounding/") {
			t.Errorf("expected our User-Agent, got %q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4 not really"))
	}))
	defer server.Close()

	// The destination's directory does not exist yet; FetchToFile
	// creates it.
	destPath := filepath.Join(t.TempDir(), "nested", "paper.pdf")
	written, contentType, err := FetchToFile(context.Background(), userAgent(""), server.URL, destPath)
	if err != nil {
		t.Fatalf("FetchToFile: %v", err)
	}
	if written != int64(len("%PDF-1.4 not really")) {
		t.Errorf("expected %d bytes written, got %d", len("%PDF-1.4 not really"), written)
	}
	if contentType != "application/pdf" {
		t.Errorf("expected content type to be reported, got %q", contentType)
	}
	body, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(body) != "%PDF-1.4 not really" {
		t.Errorf("file content mismatch: %q", body)
	}
}

func TestFetchToFile_NonSuccessLeavesNoFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	destPath := filepath.Join(t.TempDir(), "missing.pdf")
	if _, _, err := FetchToFile(context.Background(), userAgent(""), server.URL, destPath); err == nil {
		t.Fatal("expected an error for a 404")
	}
	if _, err := os.Stat(destPath); !os.IsNotExist(err) {
		t.Errorf("expected no file after a failed fetch, stat: %v", err)
	}
}
