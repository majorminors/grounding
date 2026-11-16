package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The release gate for publishing core (PLAN "Core and add-on split",
// phase 3): no Go file, test or doc of core names a backend that
// belongs in an add-on, so the public repository can be given away and
// the public build can leak nothing that is not in it. The pattern is
// written so this file does not match itself.
var namesThatBelongInAnAddOn = regexp.MustCompile(`(?i)\bann[a](s|'s|’s)?\b|\bannas[-_ ]archive|\bscid[b]\b|aarecor[d]|\blibgen\b|\bz-librar[y]\b|sci-hu[b]`)

// docsCoreGivesAway are the documents that go to the public repository
// with the code, the planning docs among them since 2026-11-16.
var docsCoreGivesAway = []string{"README.md", "AGENTS.md", "PLAN.md", "LOG.md"}

func TestCoreNamesNoBackendThatBelongsInAnAddOn(t *testing.T) {
	var files []string
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && (strings.HasPrefix(entry.Name(), ".") && path != ".") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, docsCoreGivesAway...)
	for _, path := range files {
		content, err := os.ReadFile(path)
		if os.IsNotExist(err) && path == "AGENTS.md" {
			continue // the public copy leaves the agent file out
		}
		if err != nil {
			t.Fatal(err)
		}
		for number, line := range strings.Split(string(content), "\n") {
			if match := namesThatBelongInAnAddOn.FindString(line); match != "" {
				t.Errorf("%s:%d names %q, which belongs in an add-on", path, number+1, match)
			}
		}
	}
}
