package backends

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// downloadTimeout bounds a single document transfer.
	downloadTimeout = 5 * time.Minute

	// maxDownloadBytes caps a single document transfer. Scanned books
	// run to a few hundred MB; anything larger is not a document we
	// want to be holding.
	maxDownloadBytes = 512 << 20
)

// ContainedPath joins dir and filename and refuses any result that
// would escape dir. The filename comes from upstream data (an MD5, a
// DOI) that is validated elsewhere; this is the last line.
func ContainedPath(dir, filename string) (string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving download dir: %w", err)
	}
	destPath := filepath.Join(absDir, filename)
	if destPath != absDir && !strings.HasPrefix(destPath, absDir+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing path %q outside download dir", filename)
	}
	return destPath, nil
}

// FetchToFile streams url into destPath, creating the directory as
// needed. It bypasses the rate-limited API client on purpose: this is
// a one-off large transfer from a CDN, not an API call. Returns the
// bytes written and the response Content-Type. A partial file is
// removed on failure.
func FetchToFile(ctx context.Context, userAgent, url, destPath string) (int64, string, error) {
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return 0, "", fmt.Errorf("creating download dir: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", fmt.Errorf("creating download request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("downloading file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, "", fmt.Errorf("download HTTP %d", resp.StatusCode)
	}

	file, err := os.Create(destPath)
	if err != nil {
		return 0, "", fmt.Errorf("creating file: %w", err)
	}
	defer file.Close()

	written, err := io.Copy(file, io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		os.Remove(destPath)
		return 0, "", fmt.Errorf("writing file: %w", err)
	}
	if written > maxDownloadBytes {
		os.Remove(destPath)
		return 0, "", fmt.Errorf("download exceeds %d bytes", maxDownloadBytes)
	}

	return written, resp.Header.Get("Content-Type"), nil
}
