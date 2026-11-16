package tools

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"btr.mt/grounding/internal/backends"
)

// The research ledger is the record of one research_question run: every
// source the sub-agent's tools put in front of it, with the text it was
// shown. It does two jobs that keep the sub-agent honest without trusting
// it.
//
// It gates web_fetch. A run may fetch only a URL that its own results
// produced: a search hit, a paper's URL, an open-access copy, a DOI's
// resolver address. A URL that a fetched page suggested is refused, so
// a page cannot steer the sub-agent into carrying the caller's question
// out in a query string. The price is that the sub-agent cannot follow
// links from a page it has read; following links on the same host would
// not be safe either, since the attacker's page is usually on the
// attacker's host.
//
// And it is what the backbrief is checked against: a cited source must
// be in the ledger, and its quote must be in the text the ledger holds
// for that source.
//
// The ledger is keyed by a session ID from the start, so that a later
// call can resume a run (PLAN, research agent, follow-ups).

// ResearchLedgerSource is one source a run's tools returned.
type ResearchLedgerSource struct {
	// URL is the address as a tool returned it; DOI is normalised
	// (lowercase, no resolver prefix). A source has one or both.
	URL   string `json:"url,omitempty"`
	DOI   string `json:"doi,omitempty"`
	Title string `json:"title,omitempty"`

	// RetrievedBy names the tools that returned this source, in the
	// order they did.
	RetrievedBy []string `json:"retrieved_by"`

	// TextShownToResearcher is every piece of text about this source
	// that the sub-agent was shown: a search snippet, an abstract, a
	// fetched page. Quotes are checked against it.
	TextShownToResearcher []string `json:"text_shown_to_researcher,omitempty"`

	// OpenAccessURL is a legal copy found for the DOI, when there is
	// one. With the DOI it is how the calling agent gets the document
	// itself (find_download_link); the researcher never downloads.
	OpenAccessURL string `json:"open_access_url,omitempty"`

	// RedirectedToURLs are where fetching URL ended up. A citation may
	// name the source by one of them; none becomes fetchable.
	RedirectedToURLs []string `json:"redirected_to_urls,omitempty"`
}

// ResearchLedger records one run. It is safe for concurrent use, since
// a model may call several tools at once.
type ResearchLedger struct {
	SessionID string
	StartedAt time.Time

	mutex   sync.Mutex
	sources []*ResearchLedgerSource
	// toolCalls counts the calls recorded with CountToolCall;
	// failedToolCalls holds a line for each that failed.
	toolCalls       int
	failedToolCalls []string
	// Lookups into sources, by normalised URL and by DOI.
	sourceIndexByComparableURL map[string]int
	sourceIndexByDOI           map[string]int
	// fetchableComparableURLs are the URLs web_fetch may take: every
	// source URL, open-access URL and DOI resolver address.
	fetchableComparableURLs map[string]bool
}

// NewResearchLedger starts a ledger with a fresh session ID.
func NewResearchLedger(now time.Time) *ResearchLedger {
	return &ResearchLedger{
		SessionID:                  newResearchSessionID(),
		StartedAt:                  now,
		sourceIndexByComparableURL: map[string]int{},
		sourceIndexByDOI:           map[string]int{},
		fetchableComparableURLs:    map[string]bool{},
	}
}

func newResearchSessionID() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("research-%x", time.Now().UnixNano())
	}
	return "research-" + hex.EncodeToString(buffer)
}

// ResearchLedgerEntry is what a tool reports about one source it showed
// the researcher. Empty fields are ignored.
type ResearchLedgerEntry struct {
	URL           string
	DOI           string
	Title         string
	Text          string
	OpenAccessURL string
}

// Record adds what a tool showed the researcher, merging it into an
// existing source when the URL or DOI matches one.
func (ledger *ResearchLedger) Record(toolName string, entry ResearchLedgerEntry) {
	doi := ""
	if strings.TrimSpace(entry.DOI) != "" {
		doi = backends.NormalisedDOI(entry.DOI)
	}
	comparableURL := comparableResearchURL(entry.URL)
	if doi == "" && comparableURL == "" {
		return
	}

	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()

	index, found := -1, false
	if doi != "" {
		index, found = ledger.sourceIndexByDOI[doi]
	}
	if !found && comparableURL != "" {
		index, found = ledger.sourceIndexByComparableURL[comparableURL]
	}
	if !found {
		ledger.sources = append(ledger.sources, &ResearchLedgerSource{})
		index = len(ledger.sources) - 1
	}
	source := ledger.sources[index]

	if source.URL == "" && entry.URL != "" {
		source.URL = strings.TrimSpace(entry.URL)
	}
	if source.DOI == "" {
		source.DOI = doi
	}
	if source.Title == "" {
		source.Title = strings.TrimSpace(entry.Title)
	}
	if source.OpenAccessURL == "" {
		source.OpenAccessURL = strings.TrimSpace(entry.OpenAccessURL)
	}
	if len(source.RetrievedBy) == 0 || source.RetrievedBy[len(source.RetrievedBy)-1] != toolName {
		source.RetrievedBy = append(source.RetrievedBy, toolName)
	}
	// Paging through a page with web_fetch fetches it again; keep its
	// text once.
	if text := strings.TrimSpace(entry.Text); text != "" && !containsString(source.TextShownToResearcher, text) {
		source.TextShownToResearcher = append(source.TextShownToResearcher, text)
	}

	if doi != "" {
		ledger.sourceIndexByDOI[doi] = index
		ledger.fetchableComparableURLs[comparableResearchURL("https://doi.org/"+doi)] = true
	}
	if comparableURL != "" {
		ledger.sourceIndexByComparableURL[comparableURL] = index
		ledger.fetchableComparableURLs[comparableURL] = true
	}
	if openAccess := comparableResearchURL(entry.OpenAccessURL); openAccess != "" {
		ledger.fetchableComparableURLs[openAccess] = true
	}
}

// RecordPapers records an academic tool's results.
func (ledger *ResearchLedger) RecordPapers(toolName string, papers []backends.Paper) {
	for _, paper := range papers {
		ledger.Record(toolName, ResearchLedgerEntry{
			URL: paper.URL, DOI: paper.DOI, Title: paper.Title, Text: paper.Abstract,
		})
	}
}

// RecordWebResults records a web search's results; the researcher sees
// each result's title and text, so both are what a quote may come from.
func (ledger *ResearchLedger) RecordWebResults(toolName string, results []backends.WebResult) {
	for _, result := range results {
		shown := []string{result.Text, result.Summary}
		shown = append(shown, result.Highlights...)
		ledger.Record(toolName, ResearchLedgerEntry{
			URL: result.URL, Title: result.Title, Text: strings.Join(nonEmpty(shown), "\n"),
		})
	}
}

// RecordFetchedPage records a page web_fetch read. A redirect's final
// address is recorded beside the requested one as the same source.
func (ledger *ResearchLedger) RecordFetchedPage(page *WebPageText) {
	ledger.Record("web_fetch", ResearchLedgerEntry{
		URL: page.RequestedURL, Title: page.Title, Text: page.Text,
	})
	finalURL := comparableResearchURL(page.FinalURL)
	if finalURL != "" && finalURL != comparableResearchURL(page.RequestedURL) {
		ledger.mutex.Lock()
		index, found := ledger.sourceIndexByComparableURL[comparableResearchURL(page.RequestedURL)]
		if found {
			ledger.sourceIndexByComparableURL[finalURL] = index
			source := ledger.sources[index]
			if !containsString(source.RedirectedToURLs, page.FinalURL) {
				source.RedirectedToURLs = append(source.RedirectedToURLs, page.FinalURL)
			}
		}
		ledger.mutex.Unlock()
	}
}

// ErrURLNotFromResearchResults is the refusal web_fetch gives in a
// research run for a URL its results did not produce.
var ErrURLNotFromResearchResults = errors.New("in a research run, only a URL written in the question or returned by a search or lookup in this run can be fetched, not one a page linked to")

// urlInTextPattern finds http(s) URLs in prose; trailing punctuation is
// trimmed after the match.
var urlInTextPattern = regexp.MustCompile(`https?://[^\s<>"'\x60]+`)

// URLsInText returns the http(s) URLs written in text, in order, with
// sentence punctuation and an unmatched closing bracket trimmed off.
func URLsInText(text string) []string {
	var found []string
	for _, match := range urlInTextPattern.FindAllString(text, -1) {
		match = strings.TrimRight(match, ".,;:!?")
		for _, pair := range [][2]string{{"(", ")"}, {"[", "]"}} {
			if strings.HasSuffix(match, pair[1]) && strings.Count(match, pair[0]) < strings.Count(match, pair[1]) {
				match = strings.TrimSuffix(match, pair[1])
			}
		}
		if comparableResearchURL(match) != "" && !containsString(found, match) {
			found = append(found, match)
		}
	}
	return found
}

// AllowFetching makes URLs fetchable without recording them as
// sources: the URLs the caller wrote in the question. The caller is
// trusted with its own question, so a URL in it cannot leak anything
// the caller did not already send; a URL on a page the researcher
// reads is still refused.
func (ledger *ResearchLedger) AllowFetching(urls []string) {
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	for _, rawURL := range urls {
		if comparable := comparableResearchURL(rawURL); comparable != "" {
			ledger.fetchableComparableURLs[comparable] = true
		}
	}
}

// URLMayBeFetched refuses a URL this run's results did not produce, and
// anything backends.WebURLOrRefusal refuses.
func (ledger *ResearchLedger) URLMayBeFetched(pageURL string) error {
	parsedURL, err := url.Parse(strings.TrimSpace(pageURL))
	if err != nil {
		return fmt.Errorf("not a URL: %w", err)
	}
	if err := backends.WebURLOrRefusal(parsedURL); err != nil {
		return err
	}
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	if ledger.fetchableComparableURLs[comparableResearchURL(pageURL)] {
		return nil
	}
	// DOIs are case-insensitive, so a resolver URL is judged by its DOI.
	if doi := doiFromResolverURL(pageURL); doi != "" {
		if _, found := ledger.sourceIndexByDOI[backends.NormalisedDOI(doi)]; found {
			return nil
		}
	}
	return ErrURLNotFromResearchResults
}

// SourceByURLOrDOI finds a source by DOI first, then by URL. A URL on
// a DOI resolver is looked up as its DOI.
func (ledger *ResearchLedger) SourceByURLOrDOI(sourceURL, doi string) (ResearchLedgerSource, bool) {
	if strings.TrimSpace(doi) == "" {
		doi = doiFromResolverURL(sourceURL)
	}
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	if strings.TrimSpace(doi) != "" {
		if index, found := ledger.sourceIndexByDOI[backends.NormalisedDOI(doi)]; found {
			return ledger.copyOfSource(index), true
		}
	}
	if comparableURL := comparableResearchURL(sourceURL); comparableURL != "" {
		if index, found := ledger.sourceIndexByComparableURL[comparableURL]; found {
			return ledger.copyOfSource(index), true
		}
	}
	return ResearchLedgerSource{}, false
}

// Sources returns a copy of every source, in the order first recorded.
func (ledger *ResearchLedger) Sources() []ResearchLedgerSource {
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	sources := make([]ResearchLedgerSource, len(ledger.sources))
	for index := range ledger.sources {
		sources[index] = ledger.copyOfSource(index)
	}
	return sources
}

func (ledger *ResearchLedger) copyOfSource(index int) ResearchLedgerSource {
	source := *ledger.sources[index]
	source.RetrievedBy = append([]string(nil), source.RetrievedBy...)
	source.TextShownToResearcher = append([]string(nil), source.TextShownToResearcher...)
	source.RedirectedToURLs = append([]string(nil), source.RedirectedToURLs...)
	return source
}

// CountToolCall counts one tool call into the ledger; failure, when
// not empty, says why it failed.
func (ledger *ResearchLedger) CountToolCall(toolName, failure string) {
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	ledger.toolCalls++
	if failure = strings.TrimSpace(failure); failure != "" {
		if runes := []rune(failure); len(runes) > 160 {
			failure = string(runes[:160]) + "…"
		}
		ledger.failedToolCalls = append(ledger.failedToolCalls, toolName+": "+strings.Join(strings.Fields(failure), " "))
	}
}

// FailedToolCalls says, one line each, which tool calls failed and why.
func (ledger *ResearchLedger) FailedToolCalls() []string {
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	return append([]string(nil), ledger.failedToolCalls...)
}

// ToolCalls is the number of calls counted with CountToolCall.
func (ledger *ResearchLedger) ToolCalls() int {
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	return ledger.toolCalls
}

// ResearchLedgerSnapshot is a ledger as a file: how a child
// `grounding serve --research-session` hands its run back to the
// runner that started it.
type ResearchLedgerSnapshot struct {
	SessionID       string                 `json:"session_id"`
	StartedAt       time.Time              `json:"started_at"`
	ToolCalls       int                    `json:"tool_calls"`
	FailedToolCalls []string               `json:"failed_tool_calls,omitempty"`
	Sources         []ResearchLedgerSource `json:"sources"`
}

// SnapshotJSON is the ledger as a snapshot file holds it.
func (ledger *ResearchLedger) SnapshotJSON() ([]byte, error) {
	return json.Marshal(ResearchLedgerSnapshot{
		SessionID: ledger.SessionID, StartedAt: ledger.StartedAt,
		ToolCalls: ledger.ToolCalls(), FailedToolCalls: ledger.FailedToolCalls(), Sources: ledger.Sources(),
	})
}

// WriteSnapshotFile writes the ledger to path, through a temporary file
// and a rename, so a reader never sees half a ledger.
func (ledger *ResearchLedger) WriteSnapshotFile(path string) error {
	encoded, err := ledger.SnapshotJSON()
	if err != nil {
		return err
	}
	temporary := path + ".writing"
	if err := os.WriteFile(temporary, encoded, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// ReadResearchLedgerSnapshotFile rebuilds a ledger from a snapshot file.
func ReadResearchLedgerSnapshotFile(path string) (*ResearchLedger, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snapshot ResearchLedgerSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return nil, fmt.Errorf("the research ledger file is not a ledger: %w", err)
	}
	ledger := NewResearchLedger(snapshot.StartedAt)
	ledger.SessionID = snapshot.SessionID
	ledger.toolCalls = snapshot.ToolCalls
	ledger.failedToolCalls = snapshot.FailedToolCalls
	for _, source := range snapshot.Sources {
		for index, toolName := range source.RetrievedBy {
			entry := ResearchLedgerEntry{URL: source.URL, DOI: source.DOI, Title: source.Title, OpenAccessURL: source.OpenAccessURL}
			ledger.Record(toolName, entry)
			if index == 0 {
				for _, text := range source.TextShownToResearcher {
					entry.Text = text
					ledger.Record(toolName, entry)
				}
			}
		}
		if index, found := ledger.sourceIndexByComparableURL[comparableResearchURL(source.URL)]; found {
			for _, redirectedTo := range source.RedirectedToURLs {
				ledger.sourceIndexByComparableURL[comparableResearchURL(redirectedTo)] = index
				ledger.sources[index].RedirectedToURLs = append(ledger.sources[index].RedirectedToURLs, redirectedTo)
			}
		}
	}
	return ledger, nil
}

// comparableResearchURL reduces a URL to what decides whether two
// spellings name the same page: http and https are the same, as are the
// host's case, a default port, a trailing slash and the fragment. The
// query string is kept exactly, since that is where a leak would ride.
// Anything that is not an absolute http(s) URL gives "".
func comparableResearchURL(rawURL string) string {
	parsedURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsedURL.Host == "" {
		return ""
	}
	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(parsedURL.Hostname()), ".")
	if port := parsedURL.Port(); port != "" && port != "80" && port != "443" {
		host += ":" + port
	}
	path := parsedURL.EscapedPath()
	path = strings.TrimRight(path, "/")
	comparable := host + path
	if parsedURL.RawQuery != "" {
		comparable += "?" + parsedURL.RawQuery
	}
	return comparable
}

// doiFromResolverURL returns the DOI in a doi.org or dx.doi.org URL,
// or "".
func doiFromResolverURL(rawURL string) string {
	parsedURL, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsedURL.Hostname())
	if host != "doi.org" && host != "dx.doi.org" {
		return ""
	}
	doi := strings.TrimPrefix(parsedURL.Path, "/")
	if !strings.HasPrefix(doi, "10.") {
		return ""
	}
	return doi
}

func containsString(texts []string, wanted string) bool {
	for _, text := range texts {
		if text == wanted {
			return true
		}
	}
	return false
}

func nonEmpty(texts []string) []string {
	var kept []string
	for _, text := range texts {
		if strings.TrimSpace(text) != "" {
			kept = append(kept, text)
		}
	}
	return kept
}
