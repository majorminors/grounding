package tools

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// withoutPDFToText makes the host look as if it had no pdftotext.
func withoutPDFToText(t *testing.T) {
	t.Helper()
	saved := pdfToTextProgram
	pdfToTextProgram = "pdftotext-not-installed-here"
	t.Cleanup(func() { pdfToTextProgram = saved })
}

// withStandInPDFToText puts a shell script in pdftotext's place.
func withStandInPDFToText(t *testing.T, script string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pdftotext")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	saved := pdfToTextProgram
	pdfToTextProgram = path
	t.Cleanup(func() { pdfToTextProgram = saved })
}

func servesPDF(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte(body))
	}
}

func TestPlainFetchReadsAPDFWithPDFToTextOnStdin(t *testing.T) {
	// The stand-in checks its arguments and that the PDF came on stdin.
	withStandInPDFToText(t, `[ "$*" = "-enc UTF-8 -nopgbrk -q - -" ] || { echo "args: $*" >&2; exit 2; }
grep -q 'the paper' && printf 'Tides and the moon\n\nThe tide follows the moon.\n'`)
	readers, base := testPageReaders(t, servesPDF("%PDF-1.7 the paper"))
	out, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: base + "/paper.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Renderer: plain (application/pdf)", "read as plain text by pdftotext", "The tide follows the moon."} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q:\n%s", want, out)
		}
	}
}

func TestPDFToTextWithNothingToReadSaysItMayBeAScan(t *testing.T) {
	withStandInPDFToText(t, `cat >/dev/null`)
	readers, base := testPageReaders(t, servesPDF("%PDF-1.7 scanned"))
	_, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: base + "/scan.pdf"})
	if err == nil || !strings.Contains(err.Error(), "may be a scan") {
		t.Errorf("want a scan named, got %v", err)
	}
}

func TestPDFToTextOutputIsCapped(t *testing.T) {
	withStandInPDFToText(t, `cat >/dev/null; head -c 6000000 /dev/zero | tr '\0' 'a'`)
	readers, base := testPageReaders(t, servesPDF("%PDF-1.7 huge"))
	out, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: base + "/huge.pdf", MaxLength: 500})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ran past 4 MB") || !strings.Contains(out, "of 4194304.") {
		t.Errorf("want the text capped at 4 MB and said (%d chars):\n%q", len(out), firstCharacters(out, 900))
	}
}

func TestPDFToTextFailureIsFinalAndNamesFirecrawl(t *testing.T) {
	withStandInPDFToText(t, `cat >/dev/null; echo "Syntax Error" >&2; exit 1`)
	setUp := &ladderSetUp{site: servesPDF("%PDF-1.7 broken"), crawl4aiResult: crawl4aiReadTheArticle}
	_, err := setUp.readers(t).fetchWebPage(context.Background(), WebFetchOptions{URL: ladderTestPageURL})
	if err == nil || !strings.Contains(err.Error(), "pdftotext could not read") || !strings.Contains(err.Error(), `Renderer "firecrawl"`) {
		t.Errorf("want pdftotext's failure, got %v", err)
	}
	if setUp.crawl4aiCalls != 0 {
		t.Error("a PDF pdftotext could not read is not a page for a browser")
	}
}

// The real program, where installed, on a small hand-made PDF (poppler
// rebuilds the cross-reference table it lacks).
func TestRealPDFToTextReadsASmallPDF(t *testing.T) {
	if _, err := exec.LookPath("pdftotext"); err != nil {
		t.Skip("pdftotext is not installed")
	}
	pdf := "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
		"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
		"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 300 100]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>endobj\n" +
		"4 0 obj<</Length 44>>stream\nBT /F1 12 Tf 20 50 Td (Tides follow the moon) Tj ET\nendstream endobj\n" +
		"5 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"
	readers, base := testPageReaders(t, servesPDF(pdf))
	out, err := readers.fetchWebPage(context.Background(), WebFetchOptions{URL: base + "/tides.pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Tides follow the moon") {
		t.Errorf("want the PDF's text:\n%s", out)
	}
}
