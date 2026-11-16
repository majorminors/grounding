//go:build pagereadingbakeoff

package tools

// Scores the bake-off's outputs against the key passages in urls.json,
// with the same matcher answer_question_from_web_page checks quotes
// with. Writes scores.json and scores.md beside the outputs.
//
//	go test -tags pagereadingbakeoff -run TestPageReadingBakeoffScore -v ./internal/tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The first window web_fetch returns by default: what a researcher reads
// before it decides whether to page on.
const bakeoffFirstWindowCharacters = WebFetchDefaultMaxLength

type bakeoffPageWithPassages struct {
	bakeoffPage
	KeyPassages []string `json:"key_passages"`
}

type bakeoffCellScore struct {
	PageID                   string `json:"page_id"`
	Group                    string `json:"group"`
	Cell                     string `json:"cell"`
	OK                       bool   `json:"ok"`
	Characters               int    `json:"characters"`
	PassagesFound            int    `json:"passages_found"`
	PassagesFoundFirstWindow int    `json:"passages_found_in_first_window"`
	Passages                 int    `json:"passages"`
}

func withoutSpaces(text string) string {
	return strings.Join(strings.Fields(text), "")
}

func TestPageReadingBakeoffScore(t *testing.T) {
	pagesJSON, err := os.ReadFile(filepath.Join(bakeoffDirectoryFromPackage, "urls.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pages []bakeoffPageWithPassages
	if err := json.Unmarshal(pagesJSON, &pages); err != nil {
		t.Fatal(err)
	}

	var scores []bakeoffCellScore
	for _, page := range pages {
		if len(page.KeyPassages) == 0 {
			t.Logf("%s has no key passages; left out", page.ID)
			continue
		}
		for _, cell := range bakeoffCells {
			output, err := os.ReadFile(filepath.Join(bakeoffDirectoryFromPackage, "outputs", page.ID, cell+".md"))
			if err != nil {
				continue
			}
			text := string(output)
			score := bakeoffCellScore{PageID: page.ID, Group: page.Group, Cell: cell, Passages: len(page.KeyPassages)}
			if !strings.HasPrefix(text, "(failed: ") {
				score.OK = true
				score.Characters = len([]rune(text))
				// Spacing is ignored: go-trafilatura glues words at inline
				// markup ("*Intrinsic*cognitive load"), which a reader copes
				// with but an exact match would count as the passage missing.
				whole := withoutSpaces(textNormalisedForQuoteSearch(text))
				firstWindow := withoutSpaces(textNormalisedForQuoteSearch(string([]rune(text)[:min(len([]rune(text)), bakeoffFirstWindowCharacters)])))
				for _, passage := range page.KeyPassages {
					if quoteIsOnPage(withoutSpaces(passage), whole) {
						score.PassagesFound++
					}
					if quoteIsOnPage(withoutSpaces(passage), firstWindow) {
						score.PassagesFoundFirstWindow++
					}
				}
			}
			scores = append(scores, score)
		}
	}

	encoded, _ := json.MarshalIndent(scores, "", "  ")
	if err := os.WriteFile(filepath.Join(bakeoffDirectoryFromPackage, "scores.json"), append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	// The summary: per cell overall, then per cell by group.
	var report strings.Builder
	report.WriteString("| Cell | Pages read | All passages found | Passage recall | Recall in first 10k | Median chars |\n|---|---|---|---|---|---|\n")
	for _, cell := range bakeoffCells {
		var pagesSeen, pagesRead, pagesComplete, found, foundFirst, total int
		var lengths []int
		for _, s := range scores {
			if s.Cell != cell {
				continue
			}
			pagesSeen++
			total += s.Passages
			if s.OK {
				pagesRead++
				lengths = append(lengths, s.Characters)
			}
			found += s.PassagesFound
			foundFirst += s.PassagesFoundFirstWindow
			if s.PassagesFound == s.Passages {
				pagesComplete++
			}
		}
		if pagesSeen == 0 {
			continue
		}
		sort.Ints(lengths)
		median := 0
		if len(lengths) > 0 {
			median = lengths[len(lengths)/2]
		}
		fmt.Fprintf(&report, "| %s | %d/%d | %d | %.0f%% | %.0f%% | %d |\n", cell, pagesRead, pagesSeen, pagesComplete,
			100*float64(found)/float64(total), 100*float64(foundFirst)/float64(total), median)
	}

	groups := []string{}
	seenGroup := map[string]bool{}
	for _, page := range pages {
		if !seenGroup[page.Group] {
			seenGroup[page.Group] = true
			groups = append(groups, page.Group)
		}
	}
	report.WriteString("\nPassage recall by group (whole output):\n\n| Cell | " + strings.Join(groups, " | ") + " |\n|---|" + strings.Repeat("---|", len(groups)) + "\n")
	for _, cell := range bakeoffCells {
		row := "| " + cell + " |"
		for _, group := range groups {
			var found, total int
			for _, s := range scores {
				if s.Cell == cell && s.Group == group {
					found += s.PassagesFound
					total += s.Passages
				}
			}
			if total == 0 {
				row += " – |"
			} else {
				row += fmt.Sprintf(" %.0f%% |", 100*float64(found)/float64(total))
			}
		}
		report.WriteString(row + "\n")
	}
	if err := os.WriteFile(filepath.Join(bakeoffDirectoryFromPackage, "scores.md"), []byte(report.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + report.String())
}
