package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"codeberg.org/readeck/go-readability/v2"
	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

// web_fetch returns a page's main content as markdown, a window at a
// time, inside the external-content frame. The window and its paging
// parameters follow an earlier agent's WebFetch (and the reference MCP fetch
// server it took them from): max_length characters from start_index,
// with the next start_index said outside the frame.

const (
	WebFetchDefaultMaxLength = 10000
	WebFetchMinMaxLength     = 500
	WebFetchMaxMaxLength     = 40000

	// WebFetchRendererAuto, the default, climbs the ladder in
	// web_page_reading_ladder.go: plain, then crawl4ai, then Firecrawl.
	WebFetchRendererAuto      = "auto"
	WebFetchRendererPlain     = "plain"
	WebFetchRendererCrawl4AI  = "crawl4ai"
	WebFetchRendererFirecrawl = "firecrawl"

	// Readability on a page that is mostly navigation can return a
	// scrap; below this many characters the whole page is converted
	// instead, so the caller gets something rather than nothing.
	readableContentMinimumCharacters = 200
)

// WebFetchOptions are web_fetch's parameters.
type WebFetchOptions struct {
	URL        string
	Renderer   string // auto (default), plain, crawl4ai or firecrawl
	MaxLength  int
	StartIndex int
}

// WebPageText is a page converted to text, before windowing.
type WebPageText struct {
	RequestedURL string
	FinalURL     string
	Title        string
	ContentType  string
	Renderer     string
	Text         string
	// ExtractedMainContent is false when the page was converted whole
	// because no main content could be picked out.
	ExtractedMainContent bool
	// NotesFromGrounding are lines grounding writes above the frame
	// about how the page was read: a PDF cut short, a page that is
	// nearly all links.
	NotesFromGrounding []string

	// For the page-read log: whether grounding's cache answered, and the
	// Firecrawl credits this read spent.
	fromCache             bool
	firecrawlCreditsSpent int
}

// webPageReaders is what web_fetch and the quarantined reader read
// pages through; tests replace it.
type webPageReaders struct {
	fetcher   *backends.WebPageFetcher
	crawl4ai  *backends.Crawl4AIBackend // nil or unconfigured: no browser rung of our own
	firecrawl *backends.FirecrawlBackend
	// readLogDirectory is where each read is logged ("" for none, as in
	// tests); readLogKeep how long a day's log is kept (0: forever).
	readLogDirectory string
	readLogKeep      time.Duration
}

func webPageReadersFromConfig() webPageReaders {
	cfg := config.LoadConfig()
	var opts []backends.ClientOption
	if cfg.Email != "" {
		opts = append(opts, backends.WithEmail(cfg.Email))
	}
	client := backends.NewClient(opts...)
	// A browser render takes longer than an API answer.
	firecrawlClient := backends.NewClient(append(opts, backends.WithTimeout(90*time.Second))...)
	crawl4aiClient := backends.NewClient(append(opts, backends.WithTimeout(60*time.Second))...)
	firecrawlCreditLedger := ""
	if stateDirectory := config.StateDirectory(); stateDirectory != "" {
		firecrawlCreditLedger = filepath.Join(stateDirectory, "firecrawl-credits-spent-today")
	}
	readLogDirectory := ""
	if recordsDirectory := ResearchRunRecordsDirectory(cfg); recordsDirectory != "" {
		readLogDirectory = filepath.Join(recordsDirectory, webPageReadLogSubdirectory)
	}
	return webPageReaders{
		readLogDirectory: readLogDirectory,
		readLogKeep:      researchRunRecordsKeepDuration(cfg),
		fetcher:          backends.NewWebPageFetcher(client),
		crawl4ai:         backends.NewCrawl4AI(crawl4aiClient, cfg.Crawl4AIBaseURL, cfg.Crawl4AIAPIToken),
		firecrawl: backends.NewFirecrawl(firecrawlClient, cfg.FirecrawlAPIKey).
			WithDailyCreditCap(cfg.FirecrawlDailyCreditCap, firecrawlCreditLedger),
	}
}

// FetchWebPage runs web_fetch and returns what the caller sees: a few
// lines grounding writes itself, then the framed window of page text.
func FetchWebPage(ctx context.Context, opts WebFetchOptions) (string, error) {
	return webPageReadersFromConfig().fetchWebPage(ctx, opts)
}

func (readers webPageReaders) fetchWebPage(ctx context.Context, opts WebFetchOptions) (string, error) {
	ledger := ResearchLedgerFromContext(ctx)
	if ledger != nil {
		if err := ledger.URLMayBeFetched(opts.URL); err != nil {
			return "", err
		}
	}
	page, err := readers.readWebPageText(ctx, "web_fetch", opts.URL, opts.Renderer)
	if err != nil {
		return "", err
	}
	if ledger != nil {
		ledger.RecordFetchedPage(page)
	}
	return renderWebPageWindow(page, opts.StartIndex, opts.MaxLength), nil
}

// readWebPageText fetches pageURL with the named renderer and converts
// it to text, and logs the read (web_page_read_log.go). tool names the
// tool that asked. "auto" (or none) climbs the ladder; a named renderer
// is that rung alone, and says which other might read a page it cannot.
func (readers webPageReaders) readWebPageText(ctx context.Context, tool, pageURL, renderer string) (*WebPageText, error) {
	pageURL = strings.TrimSpace(pageURL)
	if pageURL == "" {
		return nil, errors.New("url is required")
	}
	entry := newWebPageReadLogEntry(tool, pageURL, renderer, time.Now())
	entry.Tier = auth.GetTier(ctx).String()
	if ledger := ResearchLedgerFromContext(ctx); ledger != nil {
		entry.ResearchSession = ledger.SessionID
	}
	text, err := readers.readWebPageTextLogging(ctx, pageURL, renderer, entry)
	entry.finish(text, err, time.Now())
	if logErr := writeWebPageReadLogEntry(readers.readLogDirectory, readers.readLogKeep, entry); logErr != nil {
		fmt.Fprintf(os.Stderr, "web page read log: %v\n", logErr)
	}
	return text, err
}

func (readers webPageReaders) readWebPageTextLogging(ctx context.Context, pageURL, renderer string, entry *webPageReadLogEntry) (*WebPageText, error) {
	var read func(context.Context, string) (*WebPageText, error)
	switch renderer {
	case "", WebFetchRendererAuto:
		return readers.readWebPageTextByClimbingTheLadder(ctx, pageURL, entry)
	case WebFetchRendererPlain:
		read = readers.readWithPlainFetch
	case WebFetchRendererCrawl4AI:
		if !readers.crawl4ai.Configured() {
			return nil, errors.New(`renderer "crawl4ai" is not set up here (no CRAWL4AI_BASE_URL); use "auto"`)
		}
		read = readers.readWithCrawl4AI
	case WebFetchRendererFirecrawl:
		read = readers.readWithFirecrawl
	default:
		return nil, fmt.Errorf("unknown renderer %q (want auto, plain, crawl4ai or firecrawl)", renderer)
	}
	started := time.Now()
	text, err := read(ctx, pageURL)
	entry.addRung(renderer, judgeWebPageRung(text, err), text, err, time.Since(started))
	if err != nil {
		return nil, explainedFetchError(err)
	}
	return webPageTextUnlessABotCheck(text)
}

// readWithPlainFetch fetches the page itself, through the guarded
// fetcher, and reads a PDF with pdftotext where the host has it.
func (readers webPageReaders) readWithPlainFetch(ctx context.Context, pageURL string) (*WebPageText, error) {
	page, err := readers.fetcher.Fetch(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	if page.ContentType == "application/pdf" && pdfToTextAvailable() {
		return webPageTextFromPDF(ctx, page)
	}
	text, err := webPageTextFromFetchedPage(page)
	if text != nil {
		text.fromCache = page.FromCache
	}
	return text, err
}

// readWithCrawl4AI renders the page in the self-hosted browser.
func (readers webPageReaders) readWithCrawl4AI(ctx context.Context, pageURL string) (*WebPageText, error) {
	rendered, err := readers.crawl4ai.Render(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	text := &WebPageText{
		RequestedURL: pageURL,
		FinalURL:     rendered.FinalURL,
		ContentType:  "text/html",
		Renderer:     WebFetchRendererCrawl4AI,
		fromCache:    rendered.FromCache,
	}
	finalURL, _ := url.Parse(text.FinalURL)
	title, markdown, extracted, err := markdownFromHTML([]byte(rendered.HTML), finalURL)
	if err != nil {
		return nil, err
	}
	text.Title, text.Text, text.ExtractedMainContent = title, markdown, extracted
	if text.Text == "" {
		return nil, errors.New("the page has no readable text, even after crawl4ai ran its JavaScript")
	}
	return text, nil
}

// readWithFirecrawl renders the page in Firecrawl's browser (paid).
func (readers webPageReaders) readWithFirecrawl(ctx context.Context, pageURL string) (*WebPageText, error) {
	rendered, err := readers.firecrawl.Scrape(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	text, err := webPageTextFromFirecrawlPage(rendered, pageURL)
	if text != nil {
		text.fromCache = rendered.FromCache
		if !rendered.FromCache {
			text.firecrawlCreditsSpent = 1 + rendered.PDFPagesParsed
		}
	}
	return text, err
}

// webPageTextUnlessABotCheck, for a renderer named by the caller,
// refuses a bot check or block page served in place of the page, saying
// so, and notes a page with almost no text outside its links, so
// neither is mistaken for the page's content.
func webPageTextUnlessABotCheck(text *WebPageText) (*WebPageText, error) {
	if phrase := botCheckOrBlockPagePhrase(text.Text); phrase != "" {
		if text.Renderer == WebFetchRendererFirecrawl {
			return nil, fmt.Errorf("the site served Firecrawl a bot check or block page instead of the page (it says %q); no renderer here gets further", phrase)
		}
		return nil, fmt.Errorf("the site served %s a bot check or block page instead of the page (it says %q); renderer \"auto\" (the default) climbs to a browser that may get through", text.Renderer, phrase)
	}
	if strings.Contains(text.ContentType, "html") && webPageIsMostlyLinks(text.Text) {
		note := webPageMostlyLinksNote
		if text.Renderer == WebFetchRendererPlain {
			note += ` (renderer "auto" climbs to a browser that runs it)`
		}
		text.NotesFromGrounding = append(text.NotesFromGrounding, note+".")
	}
	return text, nil
}

const webPageMostlyLinksNote = "Note: this page has almost no text outside its links: it may be a sign-in wall, or built by JavaScript"

// finalReadingError marks a failure no other renderer would change: the
// page is not text, or not there. The ladder stops at one.
type finalReadingError struct{ error }

func (e finalReadingError) Unwrap() error { return e.error }

func explainedFetchError(err error) error {
	if errors.Is(err, backends.ErrAddressNotPublic) {
		return errors.New("refused: the URL leads to an address that is not on the public internet (loopback, a private network, a mesh, or a cloud metadata service)")
	}
	var httpErr *backends.HTTPError
	if errors.As(err, &httpErr) {
		return fmt.Errorf("the site answered HTTP %d", httpErr.StatusCode)
	}
	return err
}

// webPageTextFromFetchedPage converts a fetched page by content type:
// HTML to markdown (main content first, whole page as the fallback),
// text-like types as they are, anything else refused.
func webPageTextFromFetchedPage(page *backends.WebPage) (*WebPageText, error) {
	text := &WebPageText{
		RequestedURL: page.RequestedURL,
		FinalURL:     page.FinalURL,
		ContentType:  page.ContentType,
		Renderer:     WebFetchRendererPlain,
	}
	body, err := utf8Body(page)
	if err != nil {
		return nil, err
	}

	switch {
	case page.ContentType == "text/html" || page.ContentType == "application/xhtml+xml" || page.ContentType == "":
		finalURL, _ := url.Parse(page.FinalURL)
		title, markdown, extracted, err := markdownFromHTML(body, finalURL)
		if err != nil {
			return nil, err
		}
		text.Title, text.Text, text.ExtractedMainContent = title, markdown, extracted
	case strings.HasPrefix(page.ContentType, "text/") ||
		page.ContentType == "application/json" || strings.HasSuffix(page.ContentType, "+json") ||
		page.ContentType == "application/xml" || strings.HasSuffix(page.ContentType, "+xml"):
		text.Text = strings.TrimSpace(string(body))
	case page.ContentType == "application/pdf":
		return nil, finalReadingError{fmt.Errorf("the URL is a PDF, and this server has no pdftotext to read it with. For a scholarly paper, look it up by its DOI or title with the citation tools instead. Renderer \"firecrawl\" reads a PDF for one credit plus one a page, so only its first %d pages", backends.FirecrawlPDFMaxPagesParsed)}
	default:
		return nil, finalReadingError{fmt.Errorf("the URL serves %s, not a page of text", page.ContentType)}
	}
	if text.Text == "" {
		return nil, errors.New("the page has no readable text (it may be built by JavaScript; renderer \"auto\" climbs to a browser that runs it)")
	}
	return text, nil
}

// webPageTextFromFirecrawlPage converts a Firecrawl render the way the
// plain renderer converts a fetched page: Firecrawl only runs the
// page's JavaScript, and markdownFromHTML picks out the main content.
// A document with no HTML (a PDF, which Firecrawl parses itself) is
// read from Firecrawl's markdown, whole.
func webPageTextFromFirecrawlPage(rendered *backends.FirecrawlPage, pageURL string) (*WebPageText, error) {
	text := &WebPageText{
		RequestedURL: pageURL,
		FinalURL:     rendered.FinalURL,
		Title:        strings.TrimSpace(rendered.Title),
		ContentType:  rendered.ContentType,
		Renderer:     WebFetchRendererFirecrawl,
	}
	if text.FinalURL == "" {
		text.FinalURL = pageURL
	}

	renderedDocumentIsPDF := strings.Contains(rendered.ContentType, "pdf") && rendered.Markdown != ""
	if rendered.RawHTML != "" && !renderedDocumentIsPDF {
		finalURL, _ := url.Parse(text.FinalURL)
		title, markdown, extracted, err := markdownFromHTML([]byte(rendered.RawHTML), finalURL)
		if err != nil {
			return nil, err
		}
		if title != "" {
			text.Title = title
		}
		text.Text, text.ExtractedMainContent = markdown, extracted
	} else {
		text.Text = strings.TrimSpace(rendered.Markdown)
		if rendered.PDFPagesInAll > rendered.PDFPagesParsed && rendered.PDFPagesParsed > 0 {
			text.NotesFromGrounding = append(text.NotesFromGrounding, fmt.Sprintf(
				"Note: Firecrawl read the first %d of this PDF's %d pages; the rest were not read.",
				rendered.PDFPagesParsed, rendered.PDFPagesInAll))
		}
	}
	if text.Text == "" {
		return nil, errors.New("the page has no readable text, even after Firecrawl ran its JavaScript")
	}
	return text, nil
}

// utf8Body transcodes the body to UTF-8 using the declared charset or
// what the document itself declares.
func utf8Body(page *backends.WebPage) ([]byte, error) {
	if utf8.Valid(page.Body) && (page.Charset == "" || strings.EqualFold(page.Charset, "utf-8")) {
		return page.Body, nil
	}
	contentType := page.ContentType
	if page.Charset != "" {
		contentType += "; charset=" + page.Charset
	}
	reader, err := charset.NewReader(bytes.NewReader(page.Body), contentType)
	if err != nil {
		return page.Body, nil // unknown charset: read it as it is
	}
	transcoded, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("decoding the page's text: %w", err)
	}
	return transcoded, nil
}

// markdownFromHTML picks out the main content with readability and
// converts it to markdown; when readability finds too little, the
// whole document is converted instead. Either way the links are first
// made absolute against the page's own address and stripped of their
// titles (resolveLinksAgainstPageURLAndDropTitles), so the converter
// is given no base of its own: its WithDomain option resolves against
// scheme and host only, which sends `#note-1` and `../b` to the
// site's root.
func markdownFromHTML(body []byte, pageURL *url.URL) (title, markdown string, extractedMainContent bool, err error) {
	article, readErr := readability.FromReader(bytes.NewReader(body), pageURL)
	if readErr == nil && article.Node != nil {
		resolveLinksAgainstPageURLAndDropTitles(article.Node, pageURL)
		converted, convertErr := htmltomarkdown.ConvertNode(article.Node)
		if convertErr == nil && utf8.RuneCount(bytes.TrimSpace(converted)) >= readableContentMinimumCharacters {
			return strings.TrimSpace(article.Title()), strings.TrimSpace(string(converted)), true, nil
		}
		title = strings.TrimSpace(article.Title())
	}

	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", "", false, fmt.Errorf("parsing the page: %w", err)
	}
	resolveLinksAgainstPageURLAndDropTitles(document, pageURL)
	converted, err := htmltomarkdown.ConvertNode(document)
	if err != nil {
		return "", "", false, fmt.Errorf("converting the page to markdown: %w", err)
	}
	return title, strings.TrimSpace(string(converted)), false, nil
}

// resolveLinksAgainstPageURLAndDropTitles walks the tree in place.
// Every `a[href]` and `img[src]` is resolved against the page's full
// address (path and query included, so a fragment-only link keeps its
// page), and every link's and image's `title` is removed: on most
// sites it repeats the link text, and the caller pays for it in
// characters. A `javascript:` link loses its href, so it converts to
// its bare text; an address that does not parse is left as written.
// Absolute addresses, mailto: and tel: among them, resolve to
// themselves.
func resolveLinksAgainstPageURLAndDropTitles(node *html.Node, pageURL *url.URL) {
	if node.Type == html.ElementNode && (node.Data == "a" || node.Data == "img") {
		addressAttribute := "href"
		if node.Data == "img" {
			addressAttribute = "src"
		}
		kept := node.Attr[:0]
		for _, attribute := range node.Attr {
			if attribute.Namespace == "" && attribute.Key == "title" {
				continue
			}
			if attribute.Namespace == "" && attribute.Key == addressAttribute {
				address := strings.TrimSpace(attribute.Val)
				if strings.HasPrefix(strings.ToLower(address), "javascript:") {
					continue
				}
				if pageURL != nil {
					if reference, parseErr := url.Parse(address); parseErr == nil {
						attribute.Val = pageURL.ResolveReference(reference).String()
					}
				}
			}
			kept = append(kept, attribute)
		}
		node.Attr = kept
	}
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		resolveLinksAgainstPageURLAndDropTitles(child, pageURL)
		if isLinkWithNoAddress(child) {
			unwrapNode(child)
		}
		child = next
	}
}

// isLinkWithNoAddress reports an `a` with no href: a heading anchor
// (`<a id="abstract"></a>`, which PubMed Central puts on every
// heading) or a javascript: link whose href was dropped above. Kept,
// it converts to an empty `[]()` that costs characters and means
// nothing.
func isLinkWithNoAddress(node *html.Node) bool {
	if node.Type != html.ElementNode || node.Data != "a" {
		return false
	}
	for _, attribute := range node.Attr {
		if attribute.Namespace == "" && attribute.Key == "href" {
			return false
		}
	}
	return true
}

// unwrapNode replaces node with its children, keeping their order.
func unwrapNode(node *html.Node) {
	parent := node.Parent
	for node.FirstChild != nil {
		child := node.FirstChild
		node.RemoveChild(child)
		parent.InsertBefore(child, node)
	}
	parent.RemoveChild(node)
}

// clampWebFetchMaxLength applies the default and the bounds.
func clampWebFetchMaxLength(maxLength int) int {
	if maxLength <= 0 {
		return WebFetchDefaultMaxLength
	}
	if maxLength < WebFetchMinMaxLength {
		return WebFetchMinMaxLength
	}
	if maxLength > WebFetchMaxMaxLength {
		return WebFetchMaxMaxLength
	}
	return maxLength
}

// renderWebPageWindow writes what web_fetch returns. The lines above
// the frame are grounding's own: addresses, renderer, and where this
// window sits in the page. Everything the page supplied, its title
// included, goes inside the frame.
func renderWebPageWindow(page *WebPageText, startIndex, maxLength int) string {
	maxLength = clampWebFetchMaxLength(maxLength)
	runes := []rune(page.Text)
	total := len(runes)
	if startIndex < 0 {
		startIndex = 0
	}

	var header strings.Builder
	fmt.Fprintf(&header, "URL: %s\n", page.FinalURL)
	if page.FinalURL != page.RequestedURL {
		fmt.Fprintf(&header, "Redirected from: %s\n", page.RequestedURL)
	}
	fmt.Fprintf(&header, "Renderer: %s", page.Renderer)
	if page.ContentType != "" {
		fmt.Fprintf(&header, " (%s)", page.ContentType)
	}
	header.WriteString("\n")
	if !page.ExtractedMainContent && strings.Contains(page.ContentType, "html") {
		header.WriteString("Note: no main content could be picked out, so this is the whole page, menus and all.\n")
	}
	for _, note := range page.NotesFromGrounding {
		header.WriteString(note + "\n")
	}

	if startIndex >= total {
		fmt.Fprintf(&header, "start_index %d is past the end: the page has %d characters.\n", startIndex, total)
		return strings.TrimRight(header.String(), "\n")
	}
	end := startIndex + maxLength
	if end > total {
		end = total
	}
	fmt.Fprintf(&header, "Characters %d–%d of %d.", startIndex, end, total)
	if end < total {
		fmt.Fprintf(&header, " More follows: call again with start_index=%d.", end)
	}
	header.WriteString("\n\n")

	content := string(runes[startIndex:end])
	if startIndex == 0 && page.Title != "" {
		content = "Title: " + page.Title + "\n\n" + content
	}
	return header.String() + FramedAsExternalContent("web_fetch "+page.FinalURL, content)
}
