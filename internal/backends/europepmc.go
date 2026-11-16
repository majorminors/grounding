package backends

// Europe PMC backend.
//
// Endpoint, parameter and field names were verified on 2026-09-17
// against the live OpenAPI definition EBI serves at
// https://www.ebi.ac.uk/europepmc/webservices/rest/v3/api-docs (rendered
// by https://www.ebi.ac.uk/europepmc/webservices/rest/swagger-ui/index.html),
// the searchable-field list at
// https://www.ebi.ac.uk/europepmc/webservices/rest/fields?format=json,
// and live JSON from https://www.ebi.ac.uk/europepmc/webservices/rest/search.
// The narrative page at https://europepmc.org/RestfulWebService is the
// same API description behind a Cloudflare challenge. The landing-page
// pattern https://europepmc.org/article/{SOURCE}/{id} was confirmed in
// a browser for both MED and PPR records.

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	europePMCBaseURL = "https://www.ebi.ac.uk/europepmc/webservices/rest"

	// Europe PMC publishes no numeric rate limit for the REST API: neither
	// the OpenAPI definition nor the help pages state a quota. 5 req/s
	// with burst 1 is a courtesy ceiling chosen to sit well under anything
	// that could read as abuse, not a documented figure.
	europePMCRPS = 5.0

	// europePMCMaxPageSize caps a single search page. The API accepts up
	// to 1000, but core results carry abstracts and full author lists, so
	// the API's own default of 25 is the ceiling here.
	europePMCMaxPageSize = 25

	// europePMCDefaultLimit applies when the caller passes limit <= 0,
	// matching the other search backends.
	europePMCDefaultLimit = 10

	// europePMCVerifyThreshold is the minimum match confidence for
	// VerifyByTitle to report a citation as verified.
	europePMCVerifyThreshold = 0.5

	// europePMCArticleURLPrefix is the human-readable landing page for a
	// record, addressed by source code and record id.
	europePMCArticleURLPrefix = "https://europepmc.org/article"
)

// EuropePMCBackend implements SearchBackend and CitationBackend using
// the Europe PMC Articles RESTful API. Europe PMC indexes PubMed/MEDLINE,
// PMC full text, preprints (source PPR), Agricola and patents, so it
// complements Crossref and OpenAlex with biomedical and life-science
// coverage and direct open-access full-text links.
type EuropePMCBackend struct {
	client  *Client
	baseURL string
}

// NewEuropePMC creates a new Europe PMC backend.
func NewEuropePMC(client *Client) *EuropePMCBackend {
	ensureRateLimit("europepmc", europePMCRPS, 1)
	return &EuropePMCBackend{
		client:  client,
		baseURL: europePMCBaseURL,
	}
}

// Name returns the backend identifier.
func (e *EuropePMCBackend) Name() string {
	return "europepmc"
}

// searchURL builds a GET /search request. resultType=core returns the
// abstract, journalInfo and fullTextUrlList; lite omits them and is
// enough for a reachability probe.
func (e *EuropePMCBackend) searchURL(query string, pageSize int, resultType string) string {
	params := url.Values{}
	params.Set("query", query)
	params.Set("resultType", resultType)
	params.Set("format", "json")
	params.Set("pageSize", strconv.Itoa(pageSize))
	return e.baseURL + "/search?" + params.Encode()
}

// HealthCheck tests if the Europe PMC API is reachable with one tiny
// lite search.
func (e *EuropePMCBackend) HealthCheck(ctx context.Context) HealthStatus {
	checkURL := e.searchURL("test", 1, "lite")
	var response europePMCSearchResponse
	if err := e.client.GetJSON(ctx, checkURL, "europepmc", &response); err != nil {
		return HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("Europe PMC API error: %v", err),
		}
	}
	return HealthStatus{
		Healthy: true,
		Message: "Europe PMC API reachable",
	}
}

// VerifyDOI checks if a DOI exists and returns its metadata. Europe PMC
// has no DOI lookup endpoint; the DOI field is searched as an exact
// phrase (case-insensitive, and a shorter DOI does not phrase-match a
// longer one sharing its prefix, checked live) and the hit is accepted
// only when its DOI equals the request, so the verdict never rests on
// the tokeniser. An empty result list is a not-verified result, not an
// error.
func (e *EuropePMCBackend) VerifyDOI(ctx context.Context, doi string) (*VerificationResult, error) {
	doi = normalizeDOI(doi)
	query := fmt.Sprintf(`DOI:"%s"`, europePMCPhrase(doi))
	lookupURL := e.searchURL(query, 1, "core")

	var response europePMCSearchResponse
	if err := e.client.GetJSON(ctx, lookupURL, "europepmc", &response); err != nil {
		return nil, fmt.Errorf("europepmc API error: %w", err)
	}

	for i := range response.ResultList.Result {
		result := &response.ResultList.Result[i]
		if normalizeDOI(result.DOI) != doi {
			continue
		}
		paper := europePMCResultToPaper(*result)
		return &VerificationResult{
			Verified:   true,
			Paper:      &paper,
			Source:     "europepmc",
			Confidence: 1.0,
		}, nil
	}

	return &VerificationResult{
		Verified: false,
		Source:   "europepmc",
	}, nil
}

// VerifyByTitle searches for a paper by title/authors/year and scores the
// match. The title is first searched as an exact TITLE phrase; if that
// returns nothing (a phrase misses on a single changed word) it falls
// back to an OR-joined TITLE term query, which relevance-ranks the
// closest titles so the confidence scorer, not the phrase parser,
// decides whether the citation holds up. Checked live: the OR form put
// the target first with one word misspelt, where AND returned nothing.
func (e *EuropePMCBackend) VerifyByTitle(ctx context.Context, title string, authors []string, year int) (*VerificationResult, error) {
	phraseQuery := fmt.Sprintf(`TITLE:"%s"`, europePMCPhrase(title))
	var response europePMCSearchResponse
	if err := e.client.GetJSON(ctx, e.searchURL(phraseQuery, 5, "core"), "europepmc", &response); err != nil {
		return nil, fmt.Errorf("europepmc search error: %w", err)
	}

	if len(response.ResultList.Result) == 0 {
		if looseQuery := europePMCLooseTitleQuery(title); looseQuery != "" {
			if err := e.client.GetJSON(ctx, e.searchURL(looseQuery, 5, "core"), "europepmc", &response); err != nil {
				return nil, fmt.Errorf("europepmc search error: %w", err)
			}
		}
	}

	if len(response.ResultList.Result) == 0 {
		return &VerificationResult{
			Verified: false,
			Source:   "europepmc",
		}, nil
	}

	// Find best match
	var bestMatch *europePMCResult
	var bestScore float64

	for i := range response.ResultList.Result {
		result := &response.ResultList.Result[i]
		score := europePMCMatchConfidence(title, authors, year, result)
		if score > bestScore {
			bestScore = score
			bestMatch = result
		}
	}

	if bestScore < europePMCVerifyThreshold {
		return &VerificationResult{
			Verified: false,
			Source:   "europepmc",
		}, nil
	}

	paper := europePMCResultToPaper(*bestMatch)
	return &VerificationResult{
		Verified:   true,
		Paper:      &paper,
		Source:     "europepmc",
		Confidence: bestScore,
	}, nil
}

// Search returns papers matching the query in Europe PMC's default
// relevance order. Year and citation floors are applied as native query
// clauses; the Domain filter has no Europe PMC equivalent and is ignored.
func (e *EuropePMCBackend) Search(ctx context.Context, query string, limit int, filters SearchFilters) ([]Paper, error) {
	if limit <= 0 {
		limit = europePMCDefaultLimit
	}
	if limit > europePMCMaxPageSize {
		limit = europePMCMaxPageSize
	}

	searchURL := e.searchURL(europePMCSearchQuery(query, filters), limit, "core")

	var response europePMCSearchResponse
	if err := e.client.GetJSON(ctx, searchURL, "europepmc", &response); err != nil {
		return nil, fmt.Errorf("europepmc search error: %w", err)
	}

	papers := make([]Paper, 0, len(response.ResultList.Result))
	for _, result := range response.ResultList.Result {
		if result.Title == "" {
			continue
		}
		papers = append(papers, europePMCResultToPaper(result))
	}
	return papers, nil
}

// europePMCSearchQuery combines the caller's query with native filter
// clauses. The query language is Lucene-style: fielded terms, ranges as
// FIELD:[low TO high] with * for an open bound, and boolean AND. PUB_YEAR
// and CITED are both searchable fields (per the fields module), so year
// and citation floors are applied server-side rather than post-filtered.
func europePMCSearchQuery(query string, filters SearchFilters) string {
	var clauses []string
	if filters.YearMin > 0 || filters.YearMax > 0 {
		lowerBound, upperBound := "*", "*"
		if filters.YearMin > 0 {
			lowerBound = strconv.Itoa(filters.YearMin)
		}
		if filters.YearMax > 0 {
			upperBound = strconv.Itoa(filters.YearMax)
		}
		clauses = append(clauses, fmt.Sprintf("PUB_YEAR:[%s TO %s]", lowerBound, upperBound))
	}
	if filters.MinCitations > 0 {
		clauses = append(clauses, fmt.Sprintf("CITED:[%d TO *]", filters.MinCitations))
	}
	if len(clauses) == 0 {
		return query
	}
	return "(" + query + ") AND " + strings.Join(clauses, " AND ")
}

// europePMCPhrase prepares a value for use inside a quoted phrase: any
// embedded double quotes would end the phrase early, so they become
// spaces, and whitespace runs collapse to one space.
func europePMCPhrase(value string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(value, `"`, " ")), " ")
}

// europePMCLooseTitleQuery builds the OR-joined TITLE term query used
// when an exact phrase finds nothing. tokenize lowercases and strips
// punctuation, which also keeps any "AND"/"OR"/"NOT"/"TO" in a title
// from being read as query operators. Returns "" for a title with no
// tokens, since TITLE:() is a syntax error upstream.
func europePMCLooseTitleQuery(title string) string {
	tokens := tokenize(title)
	if len(tokens) == 0 {
		return ""
	}
	return "TITLE:(" + strings.Join(tokens, " OR ") + ")"
}

// Europe PMC API response types

type europePMCSearchResponse struct {
	HitCount   int `json:"hitCount"`
	ResultList struct {
		Result []europePMCResult `json:"result"`
	} `json:"resultList"`
}

// europePMCResult is the subset of a core-format search result this
// backend reads. journalTitle is only populated in lite results and
// journalInfo only in core; preprints (source PPR) carry neither and
// name their server under bookOrReportDetails.publisher.
type europePMCResult struct {
	ID                  string                        `json:"id"`
	Source              string                        `json:"source"`
	PMID                string                        `json:"pmid"`
	PMCID               string                        `json:"pmcid"`
	DOI                 string                        `json:"doi"`
	Title               string                        `json:"title"`
	AuthorString        string                        `json:"authorString"`
	AuthorList          *europePMCAuthorList          `json:"authorList"`
	JournalTitle        string                        `json:"journalTitle"`
	JournalInfo         *europePMCJournalInfo         `json:"journalInfo"`
	BookOrReportDetails *europePMCBookOrReportDetails `json:"bookOrReportDetails"`
	PubYear             string                        `json:"pubYear"` // A string in the API, e.g. "2026"
	AbstractText        string                        `json:"abstractText"`
	IsOpenAccess        string                        `json:"isOpenAccess"` // "Y" or "N"
	CitedByCount        int                           `json:"citedByCount"`
	FullTextURLList     *europePMCFullTextURLList     `json:"fullTextUrlList"`
}

type europePMCAuthorList struct {
	Author []europePMCAuthor `json:"author"`
}

type europePMCAuthor struct {
	FullName       string `json:"fullName"` // "Surname Initials", e.g. "Abu Bakar YI"
	LastName       string `json:"lastName"`
	FirstName      string `json:"firstName"`
	CollectiveName string `json:"collectiveName"`
}

type europePMCJournalInfo struct {
	Journal struct {
		Title string `json:"title"`
	} `json:"journal"`
}

type europePMCBookOrReportDetails struct {
	Publisher string `json:"publisher"`
}

type europePMCFullTextURLList struct {
	FullTextURL []europePMCFullTextURL `json:"fullTextUrl"`
}

type europePMCFullTextURL struct {
	Availability     string `json:"availability"`     // "Open access", "Free", "Subscription required"
	AvailabilityCode string `json:"availabilityCode"` // "OA", "F", "S"
	DocumentStyle    string `json:"documentStyle"`    // "pdf", "html", "doi", "abs"
	Site             string `json:"site"`
	URL              string `json:"url"`
}

// europePMCResultToPaper converts a Europe PMC search result to a Paper.
func europePMCResultToPaper(result europePMCResult) Paper {
	return Paper{
		Title:     europePMCTitle(result.Title),
		Authors:   europePMCAuthors(result),
		Year:      europePMCYear(result.PubYear),
		DOI:       normalizeDOI(result.DOI),
		Abstract:  stripXMLTags(result.AbstractText),
		URL:       europePMCPaperURL(result),
		Source:    "europepmc",
		Journal:   europePMCJournal(result),
		Citations: result.CitedByCount,
	}
}

// europePMCTitle drops the trailing full stop MEDLINE appends to titles
// ("...cognitive load theory.") so titles line up with the other
// backends for display and title-keyed de-duplication. A title ending in
// an ellipsis is left alone.
func europePMCTitle(title string) string {
	title = strings.TrimSpace(title)
	if strings.HasSuffix(title, ".") && !strings.HasSuffix(title, "..") {
		title = strings.TrimSuffix(title, ".")
	}
	return title
}

// europePMCAuthors returns display names for a result's authors. The
// structured authorList is preferred; authorString ("Rizvi FA, Choudhury
// B, Hadie SNH.") is the fallback, split on commas with the terminal
// full stop removed.
func europePMCAuthors(result europePMCResult) []string {
	if result.AuthorList != nil && len(result.AuthorList.Author) > 0 {
		authors := make([]string, 0, len(result.AuthorList.Author))
		for _, author := range result.AuthorList.Author {
			name := strings.TrimSpace(author.FullName)
			if name == "" {
				name = strings.TrimSpace(author.CollectiveName)
			}
			if name != "" {
				authors = append(authors, name)
			}
		}
		return authors
	}

	authorString := strings.TrimSuffix(strings.TrimSpace(result.AuthorString), ".")
	if authorString == "" {
		return []string{}
	}
	parts := strings.Split(authorString, ",")
	authors := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name != "" {
			authors = append(authors, name)
		}
	}
	return authors
}

// europePMCFamilyNames returns the family name of each author, from
// lastName when the structured list is present and otherwise derived
// from the "Surname Initials" display form.
func europePMCFamilyNames(result europePMCResult) []string {
	if result.AuthorList != nil && len(result.AuthorList.Author) > 0 {
		families := make([]string, 0, len(result.AuthorList.Author))
		for _, author := range result.AuthorList.Author {
			family := strings.TrimSpace(author.LastName)
			if family == "" {
				family = europePMCFamilyName(author.FullName)
			}
			if family != "" {
				families = append(families, family)
			}
		}
		return families
	}

	authors := europePMCAuthors(result)
	families := make([]string, 0, len(authors))
	for _, author := range authors {
		if family := europePMCFamilyName(author); family != "" {
			families = append(families, family)
		}
	}
	return families
}

// europePMCFamilyName extracts the family name from Europe PMC's
// "Surname Initials" author form ("Abu Bakar YI" -> "Abu Bakar"). The
// shared extractFamilyName assumes "First Last" and would return the
// initials, so the trailing all-capitals token is stripped when there is
// more than one token.
func europePMCFamilyName(displayName string) string {
	fields := strings.Fields(strings.TrimSuffix(strings.TrimSpace(displayName), "."))
	if len(fields) == 0 {
		return ""
	}
	if len(fields) > 1 && europePMCLooksLikeInitials(fields[len(fields)-1]) {
		return strings.Join(fields[:len(fields)-1], " ")
	}
	return strings.Join(fields, " ")
}

// europePMCLooksLikeInitials reports whether a token is one to four
// capital ASCII letters, the shape of MEDLINE author initials.
func europePMCLooksLikeInitials(token string) bool {
	if len(token) == 0 || len(token) > 4 {
		return false
	}
	for _, r := range token {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// europePMCYear parses the API's string pubYear. Anything unparseable
// yields 0 (unknown), consistent with the other backends.
func europePMCYear(pubYear string) int {
	year, err := strconv.Atoi(strings.TrimSpace(pubYear))
	if err != nil || year <= 0 {
		return 0
	}
	return year
}

// europePMCJournal picks the container name: journalInfo.journal.title in
// core results, journalTitle in lite results, or the preprint server /
// publisher for records without a journal.
func europePMCJournal(result europePMCResult) string {
	if result.JournalInfo != nil && result.JournalInfo.Journal.Title != "" {
		return result.JournalInfo.Journal.Title
	}
	if result.JournalTitle != "" {
		return result.JournalTitle
	}
	if result.BookOrReportDetails != nil {
		return result.BookOrReportDetails.Publisher
	}
	return ""
}

// europePMCPaperURL prefers an open-access full-text link (PDF over
// HTML, mirroring the OpenAlex backend), then the Europe PMC landing
// page for the record, then the DOI resolver.
func europePMCPaperURL(result europePMCResult) string {
	if result.FullTextURLList != nil {
		var htmlURL string
		for _, fullText := range result.FullTextURLList.FullTextURL {
			if fullText.URL == "" || !europePMCIsOpenFullText(fullText) {
				continue
			}
			switch strings.ToLower(fullText.DocumentStyle) {
			case "pdf":
				return fullText.URL
			case "html":
				if htmlURL == "" {
					htmlURL = fullText.URL
				}
			}
		}
		if htmlURL != "" {
			return htmlURL
		}
	}

	if result.Source != "" && result.ID != "" {
		return fmt.Sprintf("%s/%s/%s", europePMCArticleURLPrefix, url.PathEscape(result.Source), url.PathEscape(result.ID))
	}
	if doi := normalizeDOI(result.DOI); doi != "" {
		return "https://doi.org/" + doi
	}
	return ""
}

// europePMCIsOpenFullText reports whether a full-text link is readable
// without a subscription: availabilityCode "OA" (open access) or "F"
// (free).
func europePMCIsOpenFullText(fullText europePMCFullTextURL) bool {
	switch strings.ToUpper(fullText.AvailabilityCode) {
	case "OA", "F":
		return true
	}
	return false
}

// europePMCMatchConfidence computes a weighted confidence score for a
// Europe PMC result match.
// Weights: title (60%), year (20%), authors (20%).
// Post-processing: year gap penalty and first-author gate.
func europePMCMatchConfidence(queryTitle string, queryAuthors []string, queryYear int, result *europePMCResult) float64 {
	score := 0.0

	// Title similarity (60%)
	if result.Title != "" {
		titleSim := jaccardSimilarity(queryTitle, result.Title)
		score += 0.6 * titleSim
	}

	// Year match (20%)
	workYear := europePMCYear(result.PubYear)
	if queryYear > 0 && workYear > 0 {
		if queryYear == workYear {
			score += 0.2
		} else if abs(queryYear-workYear) <= 1 {
			// Allow 1 year difference (publication delays)
			score += 0.1
		}
	}

	// Author match (20%)
	workAuthors := europePMCAuthors(*result)
	if len(queryAuthors) > 0 && len(workAuthors) > 0 {
		authorScore := europePMCAuthorSimilarity(queryAuthors, workAuthors)
		score += 0.2 * authorScore
	}

	// Year gap penalty
	score *= yearPenalty(queryYear, workYear)

	// First-author gate: if the first query author's family name doesn't
	// appear among the result's authors, cap below threshold
	if !europePMCFirstAuthorMatches(queryAuthors, europePMCFamilyNames(*result)) {
		if score > 0.4 {
			score = 0.4
		}
	}

	return score
}

// europePMCFirstAuthorMatches applies the shared first-author gate, then
// retries with the query author's family name read in Europe PMC's own
// "Surname Initials" form. The shared extractFamilyName assumes "First
// Last", so a query author copied back from Europe PMC ("Islam MA") or
// a multi-word surname ("Abu Bakar") would otherwise fail the gate on
// its initials or its last word.
func europePMCFirstAuthorMatches(queryAuthors []string, workFamilyNames []string) bool {
	if firstAuthorMatchesAny(queryAuthors, workFamilyNames) {
		return true
	}
	queryFamily := strings.ToLower(europePMCFamilyName(queryAuthors[0]))
	if queryFamily == "" {
		return false
	}
	for _, workFamily := range workFamilyNames {
		if strings.ToLower(workFamily) == queryFamily {
			return true
		}
	}
	return false
}

// europePMCAuthorSimilarity computes how well query authors match the
// result's "Surname Initials" display names: a query author counts as
// matched when any of its tokens longer than one character (so initials
// never match) appears in a result author's tokens.
func europePMCAuthorSimilarity(queryAuthors []string, workAuthors []string) float64 {
	if len(queryAuthors) == 0 || len(workAuthors) == 0 {
		return 0.0
	}

	matches := 0
	for _, queryAuthor := range queryAuthors {
		queryTokens := tokenize(queryAuthor)
		if europePMCAnyTokenShared(queryTokens, workAuthors) {
			matches++
		}
	}

	return float64(matches) / float64(len(queryAuthors))
}

// europePMCAnyTokenShared reports whether any multi-character query
// token appears among the tokens of any work author.
func europePMCAnyTokenShared(queryTokens []string, workAuthors []string) bool {
	for _, workAuthor := range workAuthors {
		workTokens := tokenize(workAuthor)
		for _, queryToken := range queryTokens {
			if len(queryToken) <= 1 {
				continue
			}
			for _, workToken := range workTokens {
				if queryToken == workToken {
					return true
				}
			}
		}
	}
	return false
}
