package cmd

import (
	"os/exec"
	"testing"

	"btr.mt/grounding/internal/tools"
)

func TestADownloadNamesExactlyOneDocument(t *testing.T) {
	for _, c := range []struct {
		document tools.DocumentToDownload
		valid    bool
	}{
		{tools.DocumentToDownload{DOI: "10.1/x"}, true},
		{tools.DocumentToDownload{MD5: "d6e1dc51a50726f00ec438af21952a45"}, true},
		{tools.DocumentToDownload{Title: "The New Monogamy", Author: "Tammy Nelson"}, true},
		{tools.DocumentToDownload{Title: "The New Monogamy"}, true},
		{tools.DocumentToDownload{ISBN: "9781472104533"}, true},
		{tools.DocumentToDownload{Title: "The New Monogamy", ISBN: "9781472104533"}, true},
		{tools.DocumentToDownload{}, false},
		{tools.DocumentToDownload{Author: "Tammy Nelson"}, false},
		{tools.DocumentToDownload{DOI: "10.1/x", Title: "The New Monogamy"}, false},
		{tools.DocumentToDownload{DOI: "10.1/x", MD5: "d6e1dc51a50726f00ec438af21952a45"}, false},
	} {
		if err := c.document.Validate(); (err == nil) != c.valid {
			t.Errorf("%+v: err = %v, want valid %v", c.document, err, c.valid)
		}
	}
}

// The printed download command is pasted into a shell as it is.
func TestAPrintedTitleSurvivesTheShell(t *testing.T) {
	for _, title := range []string{"The New Monogamy", "Darwin's Dangerous Idea", `Cash $ "quotes" & ; semicolons`} {
		out, err := exec.Command("sh", "-c", "printf %s "+quotedForTheShell(title)).Output()
		if err != nil || string(out) != title {
			t.Errorf("%q came back as %q (err %v)", title, out, err)
		}
	}
}
