package cmd

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"btr.mt/grounding/internal/auth"
)

var updateReadme = flag.Bool("update-readme", false, "rewrite README.md's generated sections and cmd's copy of it")

const readmePath = "../README.md"
const landingPageCopyOfTheReadmePath = "landing-page-copy-of-the-readme.md"

// README.md's generated sections are what the registry says now, and
// cmd's copy (which the page is built from) is README.md. With
// -update-readme it makes them so.
func TestTheReadmeIsCurrentAndTheLandingPageHasItsCopy(t *testing.T) {
	readme, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	current, err := readmeWithItsGeneratedSectionsFor(string(readme), auth.TierPublic)
	if err != nil {
		t.Fatal(err)
	}
	copyOfIt, _ := os.ReadFile(landingPageCopyOfTheReadmePath)

	if *updateReadme {
		for path, text := range map[string]string{readmePath: current, landingPageCopyOfTheReadmePath: current} {
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	const fix = "go test ./cmd -run TestTheReadmeIsCurrentAndTheLandingPageHasItsCopy -update-readme"
	if current != string(readme) {
		t.Errorf("README.md's generated sections are stale: run %s", fix)
	}
	if string(copyOfIt) != string(readme) {
		t.Errorf("cmd/%s is not README.md: run %s", landingPageCopyOfTheReadmePath, fix)
	}
}

func TestTheLandingPageIsTheReadmeAboveItsMarkerWithALinkToTheCode(t *testing.T) {
	recorder := httptest.NewRecorder()
	makeLandingHandler("secret-token")(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	page := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, page)
	}
	for _, want := range []string{
		"social friction", // the opening, the README's own
		`id="what-it-does"`,
		"Under the hood",
		publicRepositoryOfTheCode,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("landing page is missing %q", want)
		}
	}
	for _, unwanted := range []string{"Run your own grounding", "Environment variables"} {
		if strings.Contains(page, unwanted) {
			t.Errorf("landing page shows %q, which is below the marker", unwanted)
		}
	}
}

func TestAnUnknownGeneratedSectionIsAnError(t *testing.T) {
	if _, err := readmeWithItsGeneratedSectionsFor("<!-- generated: no-such -->x<!-- end generated: no-such -->", auth.TierPublic); err == nil {
		t.Error("an unknown section passed silently")
	}
	if _, err := readmeWithItsGeneratedSectionsFor("<!-- generated: sources -->x<!-- end generated: tool-reference -->", auth.TierPublic); err == nil {
		t.Error("a mismatched pair passed silently")
	}
}
