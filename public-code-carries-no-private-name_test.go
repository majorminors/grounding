package main

import (
	"os"
	"path/filepath"
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
	err = filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		hidden := strings.HasPrefix(entry.Name(), ".") && entry.Name() != ".github" && entry.Name() != ".gitignore"
		if entry.IsDir() {
			if path != "." && (hidden || path == "private") {
				return filepath.SkipDir
			}
			return nil
		}
		if hidden {
			return nil // git's own files, editors' and the OS's
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for number, line := range strings.Split(string(content), "\n") {
			if match := privateName.FindString(line); match != "" {
				t.Errorf("%s:%d names %q, which is private to a deployment", path, number+1, match)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
