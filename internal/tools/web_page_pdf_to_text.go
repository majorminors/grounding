package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"btr.mt/grounding/internal/backends"
)

// A PDF the plain fetch gets is read by pdftotext (poppler-utils), when
// the host has it: plain text, no headings or structure, and nothing
// from a scan, but a few megabytes of memory where GROBID needed four
// gigabytes (LOG "The page-reading ladder"). The citation tools remain
// the better way to a paper's text.
//
// The PDF is untrusted, so pdftotext gets it on stdin with fixed
// arguments and no shell, an empty environment, a time limit and a cap
// on what it may print; on a server it also runs inside the service's
// own sandbox, where there is one.

const (
	pdfToTextTimeLimit     = 30 * time.Second
	pdfToTextMaxOutputSize = 4 << 20 // bytes of text; a long report fits
)

// pdfToTextProgram is the program's name or path; tests replace it.
var pdfToTextProgram = "pdftotext"

// pdfToTextAvailable reports whether the host has pdftotext.
func pdfToTextAvailable() bool {
	_, err := exec.LookPath(pdfToTextProgram)
	return err == nil
}

// webPageTextFromPDF reads a fetched PDF with pdftotext.
func webPageTextFromPDF(ctx context.Context, page *backends.WebPage) (*WebPageText, error) {
	ctx, cancel := context.WithTimeout(ctx, pdfToTextTimeLimit)
	defer cancel()
	// -enc UTF-8: whatever the locale; "-" "-": read stdin, print stdout.
	command := exec.CommandContext(ctx, pdfToTextProgram, "-enc", "UTF-8", "-nopgbrk", "-q", "-", "-")
	command.Env = []string{}
	command.Stdin = bytes.NewReader(page.Body)
	output := &cappedBuffer{limit: pdfToTextMaxOutputSize}
	command.Stdout = output
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, finalReadingError{fmt.Errorf("pdftotext took longer than %s to read the PDF", pdfToTextTimeLimit)}
		}
		return nil, finalReadingError{fmt.Errorf("pdftotext could not read the PDF (%v); it may be damaged or encrypted. Renderer \"firecrawl\" may read it, for a credit plus one a page", err)}
	}
	text := strings.TrimSpace(strings.ToValidUTF8(output.String(), ""))
	if text == "" {
		return nil, finalReadingError{errors.New("the PDF has no text to read: it may be a scan, which would need OCR")}
	}
	notes := []string{"Note: a PDF, read as plain text by pdftotext: no headings or layout, and figures and tables may read out of order."}
	if output.truncated {
		notes = append(notes, fmt.Sprintf("Note: the PDF's text ran past %d MB, and only that much was read.", pdfToTextMaxOutputSize>>20))
	}
	return &WebPageText{
		RequestedURL:         page.RequestedURL,
		FinalURL:             page.FinalURL,
		ContentType:          page.ContentType,
		Renderer:             WebFetchRendererPlain,
		Text:                 text,
		ExtractedMainContent: true,
		NotesFromGrounding:   notes,
		fromCache:            page.FromCache,
	}, nil
}

// cappedBuffer keeps the first limit bytes written to it and drops the
// rest, so a PDF cannot fill memory with text. The buffer is a field,
// not embedded: an embedded bytes.Buffer brings its ReadFrom, which
// io.Copy (and so exec) prefers to Write, and the cap is skipped.
type cappedBuffer struct {
	kept      bytes.Buffer
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.kept.Len()
	if len(p) > room {
		b.truncated = true
		if room > 0 {
			b.kept.Write(p[:room])
		}
		return len(p), nil
	}
	return b.kept.Write(p)
}

func (b *cappedBuffer) String() string { return b.kept.String() }
