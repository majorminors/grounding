package main

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The code repository is public, so no file in it may name what is
// private to a deployment: its hosts, addresses, domains or people.
// Those names are themselves private, so the list lives beside the
// code in private/ (AGENTS.md), one regular expression per line,
// matched case-insensitively; without private/ the test is skipped.
const privateNamesThePublicCodeMustNotCarryPath = "private/names-the-public-code-must-not-carry"

func TestPublicCodeCarriesNoPrivateName(t *testing.T) {
	listed, err := os.ReadFile(privateNamesThePublicCodeMustNotCarryPath)
	if os.IsNotExist(err) {
		t.Skip("no private/ here: nothing to check against")
	}
	if err != nil {
		t.Fatal(err)
	}
	var patterns []string
	for _, line := range strings.Split(string(listed), "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			patterns = append(patterns, line)
		}
	}
	if len(patterns) == 0 {
		t.Fatalf("%s lists no names", privateNamesThePublicCodeMustNotCarryPath)
	}
	privateName := regexp.MustCompile(`(?i)` + strings.Join(patterns, "|"))
	// Only what git tracks goes public; a built binary or a cache in
	// the checkout is not checked.
	tracked, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("git ls-files: %v", err)
	}
	for _, path := range strings.Split(strings.TrimRight(string(tracked), "\x00"), "\x00") {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue // a link, or a file deleted but not yet committed
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for number, line := range strings.Split(string(content), "\n") {
			if match := privateName.FindString(line); match != "" {
				t.Errorf("%s:%d names %q, which is private to a deployment", path, number+1, match)
			}
		}
	}
}
