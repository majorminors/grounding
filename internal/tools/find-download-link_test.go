package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

// The description promises callers a "caution" field whenever a source
// may have found a different document: it must serialise under that
// name when set and be left out when empty.
func TestAFoundDocumentCopysCautionSerialisesOnlyWhenSet(t *testing.T) {
	withCaution, _ := json.Marshal(FoundDocumentCopy{FilePath: "/tmp/x.pdf", Caution: "check it"})
	if !strings.Contains(string(withCaution), `"caution"`) {
		t.Errorf("caution missing: %s", withCaution)
	}
	withoutCaution, _ := json.Marshal(FoundDocumentCopy{FilePath: "/tmp/x.pdf"})
	if strings.Contains(string(withoutCaution), "caution") {
		t.Errorf("caution present when empty: %s", withoutCaution)
	}
}

// Unpaywall downloads are named after the DOI; the separators a DOI
// carries must not become directories.
func TestDOIFilename(t *testing.T) {
	if got := doiFilename("10.1371/journal.pmed.0020124"); got != "10.1371_journal.pmed.0020124" {
		t.Errorf("unexpected filename %q", got)
	}
}

func withDownloadProvidersForThisTest(t *testing.T, providers ...DownloadProvider) {
	t.Helper()
	saved := downloadProvidersAddedByAddOns
	t.Cleanup(func() { downloadProvidersAddedByAddOns = saved })
	downloadProvidersAddedByAddOns = providers
}

func providerTaking(name string, takes func(DocumentToDownload) bool, calls *int, err error) DownloadProvider {
	return DownloadProvider{
		Name:  name,
		Takes: takes,
		Download: func(ctx context.Context, client *backends.Client, cfg config.Config, document DocumentToDownload, parameters map[string]any, directory string) (*FoundDocumentCopy, error) {
			*calls++
			if err != nil {
				return nil, err
			}
			return &FoundDocumentCopy{Source: name, Title: document.Title}, nil
		},
	}
}

func TestTheFirstProviderThatTakesTheDocumentDownloadsIt(t *testing.T) {
	t.Setenv("GROUNDING_EMAIL", "")
	var md5Calls, bookCalls int
	withDownloadProvidersForThisTest(t,
		providerTaking("files", func(d DocumentToDownload) bool { return d.MD5 != "" }, &md5Calls, nil),
		providerTaking("books", func(d DocumentToDownload) bool { return d.IsBook() }, &bookCalls, nil),
	)
	got, err := FindDownloadLink(context.Background(), DocumentToDownload{Title: "The New Monogamy", Author: "Tammy Nelson"}, nil, "")
	if err != nil || got.Source != "books" || md5Calls != 0 || bookCalls != 1 {
		t.Errorf("got %+v, err %v; md5 provider called %d times, book provider %d", got, err, md5Calls, bookCalls)
	}
}

// Without GROUNDING_EMAIL there is no Unpaywall lookup, and the error
// says so as well as the provider's own reason.
func TestADOIWithNoOpenAccessLookupAndAFailingProviderNamesBoth(t *testing.T) {
	t.Setenv("GROUNDING_EMAIL", "")
	var calls int
	withDownloadProvidersForThisTest(t, providerTaking("files", func(d DocumentToDownload) bool { return d.DOI != "" }, &calls, errFakeKeyMissing))
	_, err := FindDownloadLink(context.Background(), DocumentToDownload{DOI: "10.1086/226906"}, nil, "")
	if err == nil || !strings.Contains(err.Error(), "GROUNDING_EMAIL") || !strings.Contains(err.Error(), errFakeKeyMissing.Error()) {
		t.Errorf("err = %v", err)
	}
}

var errFakeKeyMissing = &fakeError{"set FAKE_KEY, or set FAKE_KEY_COMMAND to a command that prints it"}

type fakeError struct{ message string }

func (e *fakeError) Error() string { return e.message }

// Core alone has no source for an MD5 or a book, and says so without
// asking anything of the network.
func TestCoreAloneRefusesAnMD5OrABookPlainly(t *testing.T) {
	t.Setenv("GROUNDING_EMAIL", "")
	withDownloadProvidersForThisTest(t)
	for _, document := range []DocumentToDownload{{MD5: "d6e1dc51a50726f00ec438af21952a45"}, {Title: "The New Monogamy"}} {
		if _, err := FindDownloadLink(context.Background(), document, nil, ""); err == nil || !strings.Contains(err.Error(), "no download source") {
			t.Errorf("%+v: err = %v", document, err)
		}
	}
}
