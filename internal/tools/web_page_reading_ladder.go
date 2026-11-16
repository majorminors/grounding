package tools

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"btr.mt/grounding/internal/backends"
)

// The "auto" renderer, web_fetch's default since 2026-11-07, climbs a
// ladder of renderers and stops at the first that reads the page
// (decided by dorian after the page-reading bake-off; PLAN and LOG
// "Page reading"):
//
//  1. plain: grounding's own guarded fetch, free, and nothing on the
//     page runs.
//  2. crawl4ai: a browser grounding runs itself, free but for memory.
//  3. Firecrawl: a paid browser with proxies that gets past more bot
//     checks, held to a daily credit cap.
//
// It climbs when a rung is blocked (a bot check or block page, HTTP
// 401, 403, 429 or 5xx, a failed render, no readable text) or reads a
// page with almost no text outside its links (a sign-in wall, or a page
// built by JavaScript). It stops at once on what no browser changes: an
// address that is not public, a page that is not there, a file that is
// not text.
//
// Firecrawl is for what is hard. When crawl4ai rendered the page and
// still found little text, that is the page, and it is returned with a
// note rather than paid for again. Firecrawl runs when a rung was
// blocked, or when no browser of our own could render the page. A URL
// that names a PDF is never sent to Firecrawl unasked: PDFs were what
// spent the last account's credits (LOG "Firecrawl's spend was PDFs").
//
// The caller is told which rung read the page and why the ones below it
// did not, above the frame.

type webPageRungOutcome int

const (
	webPageRungRead     webPageRungOutcome = iota // the page's text
	webPageRungDoubtful                           // text, but almost none outside links: try a browser, keep this
	webPageRungBlocked                            // no text worth having: climb
	webPageRungFinal                              // nothing another renderer would change: stop
)

// webPageRungResult is what one rung made of the page, and why.
type webPageRungResult struct {
	outcome webPageRungOutcome
	text    *WebPageText
	err     error
	// reason completes "the plain fetch ...", "crawl4ai ...".
	reason string
}

// judgeWebPageRung sorts a rung's text or error.
func judgeWebPageRung(text *WebPageText, err error) webPageRungResult {
	if err != nil {
		var final finalReadingError
		var httpErr *backends.HTTPError
		switch {
		case errors.Is(err, backends.ErrAddressNotPublic), errors.As(err, &final):
			return webPageRungResult{outcome: webPageRungFinal, err: err}
		case errors.Is(err, backends.ErrFirecrawlDailyCreditCapSpent):
			return webPageRungResult{outcome: webPageRungBlocked, err: err, reason: "was not tried: " + err.Error()}
		case errors.As(err, &httpErr):
			if webPageStatusAnotherRendererMayGetPast(httpErr.StatusCode) {
				return webPageRungResult{outcome: webPageRungBlocked, err: err, reason: fmt.Sprintf("was answered HTTP %d", httpErr.StatusCode)}
			}
			return webPageRungResult{outcome: webPageRungFinal, err: err}
		default:
			return webPageRungResult{outcome: webPageRungBlocked, err: err, reason: "failed (" + firstCharacters(err.Error(), 200) + ")"}
		}
	}
	if phrase := botCheckOrBlockPagePhrase(text.Text); phrase != "" {
		return webPageRungResult{outcome: webPageRungBlocked, reason: fmt.Sprintf("was served a bot check or block page (it says %q)", phrase)}
	}
	if strings.Contains(text.ContentType, "html") && webPageIsMostlyLinks(text.Text) {
		return webPageRungResult{outcome: webPageRungDoubtful, text: text, reason: "found almost no text outside the page's links"}
	}
	return webPageRungResult{outcome: webPageRungRead, text: text}
}

// webPageStatusAnotherRendererMayGetPast: 401, 403 and 429 are what
// anti-bot services answer a robot with, and 5xx includes Cloudflare's
// challenge (503); 404, 410 and the like mean the page is not there,
// which no browser changes.
func webPageStatusAnotherRendererMayGetPast(statusCode int) bool {
	return statusCode == 401 || statusCode == 403 || statusCode == 429 || statusCode >= 500
}

// readWebPageTextByClimbingTheLadder is the "auto" renderer.
func (readers webPageReaders) readWebPageTextByClimbingTheLadder(ctx context.Context, pageURL string, entry *webPageReadLogEntry) (*WebPageText, error) {
	var whyNot []string // "the plain fetch was answered HTTP 403", ...
	var doubtful *WebPageText

	plain := readers.tryRung(ctx, pageURL, WebFetchRendererPlain, readers.readWithPlainFetch, entry)
	switch plain.outcome {
	case webPageRungRead:
		return plain.text, nil
	case webPageRungFinal:
		return nil, explainedFetchError(plain.err)
	case webPageRungDoubtful:
		doubtful = plain.text
	}
	whyNot = append(whyNot, "the plain fetch "+plain.reason)

	if readers.crawl4ai.Configured() {
		rendered := readers.tryRung(ctx, pageURL, WebFetchRendererCrawl4AI, readers.readWithCrawl4AI, entry)
		switch rendered.outcome {
		case webPageRungRead, webPageRungDoubtful:
			// A page crawl4ai rendered and still found thin is thin:
			// Firecrawl would render the same page.
			return webPageTextWithLadderNotes(rendered.text, whyNot, rendered.outcome == webPageRungDoubtful), nil
		case webPageRungFinal:
			return nil, explainedFetchError(rendered.err)
		}
		whyNot = append(whyNot, "crawl4ai "+rendered.reason)
	} else {
		whyNot = append(whyNot, "crawl4ai is not set up here")
		entry.addRungNotAttempted(WebFetchRendererCrawl4AI, "not set up")
	}

	switch {
	case webPageURLNamesAPDF(pageURL):
		whyNot = append(whyNot, `a PDF is not sent to Firecrawl unasked (renderer "firecrawl" reads one, at a credit a page)`)
		entry.addRungNotAttempted(WebFetchRendererFirecrawl, "a PDF URL")
	case !readers.firecrawl.Configured():
		whyNot = append(whyNot, "Firecrawl is not set up here")
		entry.addRungNotAttempted(WebFetchRendererFirecrawl, "not set up")
	default:
		paid := readers.tryRung(ctx, pageURL, WebFetchRendererFirecrawl, readers.readWithFirecrawl, entry)
		switch paid.outcome {
		case webPageRungRead, webPageRungDoubtful:
			return webPageTextWithLadderNotes(paid.text, whyNot, paid.outcome == webPageRungDoubtful), nil
		case webPageRungFinal:
			return nil, explainedFetchError(paid.err)
		}
		whyNot = append(whyNot, "Firecrawl "+paid.reason)
	}

	if doubtful != nil {
		return webPageTextWithLadderNotes(doubtful, whyNot[1:], true), nil
	}
	return nil, fmt.Errorf("no renderer could read the page: %s", strings.Join(whyNot, "; "))
}

// tryRung reads the page with one renderer, judges the result and logs
// it.
func (readers webPageReaders) tryRung(ctx context.Context, pageURL, renderer string, read func(context.Context, string) (*WebPageText, error), entry *webPageReadLogEntry) webPageRungResult {
	started := time.Now()
	text, err := read(ctx, pageURL)
	result := judgeWebPageRung(text, err)
	entry.addRung(renderer, result, text, err, time.Since(started))
	return result
}

// webPageTextWithLadderNotes says, above the frame, which renderer read
// the page and why the ones below it did not, and notes a page with
// almost no text outside its links.
func webPageTextWithLadderNotes(text *WebPageText, whyNot []string, almostAllLinks bool) *WebPageText {
	var notes []string
	if len(whyNot) > 0 {
		reasons := strings.Join(whyNot, "; ")
		switch text.Renderer {
		case WebFetchRendererPlain:
			// Every browser failed, so the plain fetch's text is kept.
			notes = append(notes, fmt.Sprintf("Note: this is the plain fetch's text, kept because %s.", reasons))
		case WebFetchRendererFirecrawl:
			notes = append(notes, fmt.Sprintf("Note: read by Firecrawl's browser, for a credit, because %s.", reasons))
		default:
			notes = append(notes, fmt.Sprintf("Note: read by %s's browser because %s.", text.Renderer, reasons))
		}
	}
	if almostAllLinks {
		notes = append(notes, webPageMostlyLinksNote+".")
	}
	text.NotesFromGrounding = append(notes, text.NotesFromGrounding...)
	return text
}

// webPageURLNamesAPDF reports a URL whose path ends in .pdf.
func webPageURLNamesAPDF(pageURL string) bool {
	parsed, err := url.Parse(pageURL)
	return err == nil && strings.HasSuffix(strings.ToLower(parsed.Path), ".pdf")
}
