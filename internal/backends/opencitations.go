// OpenCitations backend: citation-graph edges keyed by DOI.
//
// Verified live on 2026-09-17 against the official documentation:
//
//   - API reference: https://api.opencitations.net/index/v2
//   - Access token:  https://opencitations.net/accesstoken
//
// Operations used (all under the base URL, {id} is "doi:<doi>"):
//
//   - references/{id}      outgoing edges (what this paper cites)
//   - citations/{id}       incoming edges (what cites this paper)
//   - citation-count/{id}  number of incoming edges
//
// Each edge is a JSON object with fields oci, citing, cited, creation,
// timespan, journal_sc and author_sc. The citing/cited values are
// space-separated identifier lists, e.g.
// "omid:br/06120344846 doi:10.1038/nature12373 openalex:W2159974629
// pmid:23903748", from which the doi: token is extracted. Counts come
// back as [{"count":"1806"}] with the count as a JSON *string*.
//
// There is no server-side limit parameter (a ?limit= query string is
// silently ignored), so capping happens client-side after the whole
// edge list has been parsed. Unknown DOIs are not 404s: citations and
// references return [] and citation-count returns [{"count":"0"}].
//
// The access token is optional but, when sent, it is validated: an
// unrecognised value yields HTTP 403 "Invalid token", so a misconfigured
// token breaks the backend rather than being ignored.

package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	openCitationsBaseURL = "https://api.opencitations.net/index/v2"

	// The documented limit is 180 requests/minute per IP address, which
	// is 3 req/s. Burst of 1 keeps us strictly under the ceiling rather
	// than front-loading a minute's allowance.
	openCitationsRPS = 3.0

	// openCitationsAccessTokenHeader is the header the API reads the
	// access token from. The documented Python example sends the bare
	// token as the header value with no "Bearer" or other scheme prefix.
	openCitationsAccessTokenHeader = "authorization"

	// openCitationsHealthCheckDOI is a paper with a large, stable
	// incoming-citation count (1806 on 2026-09-17), so a zero count
	// indicates a broken index rather than an obscure paper.
	openCitationsHealthCheckDOI = "10.1038/nature12373"
)

// OpenCitationsBackend fetches citation-graph edges from the
// OpenCitations Index. It carries no paper metadata (titles, authors,
// years) --- only DOI-to-DOI links and counts --- so it serves as the
// last-resort fallback for the citation_graph tool when Semantic
// Scholar and OpenAlex are both unavailable, with metadata resolved
// separately via Crossref.
type OpenCitationsBackend struct {
	client      *Client
	baseURL     string // Overridable by tests
	accessToken string // Optional; sent only when non-empty
}

// NewOpenCitations creates a new OpenCitations backend. The access
// token is optional (see https://opencitations.net/accesstoken); pass
// an empty string to make anonymous requests.
func NewOpenCitations(client *Client, accessToken string) *OpenCitationsBackend {
	ensureRateLimit("opencitations", openCitationsRPS, 1)
	return &OpenCitationsBackend{
		client:      client,
		baseURL:     openCitationsBaseURL,
		accessToken: strings.TrimSpace(accessToken),
	}
}

// Name returns the backend identifier.
func (o *OpenCitationsBackend) Name() string {
	return "opencitations"
}

// HealthCheck confirms the index is reachable and populated by asking
// for the citation count of a well-known paper and requiring it to be
// non-zero. An invalid access token surfaces here as an HTTP 403.
func (o *OpenCitationsBackend) HealthCheck(ctx context.Context) HealthStatus {
	count, err := o.CitationCount(ctx, openCitationsHealthCheckDOI)
	if err != nil {
		return HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("OpenCitations API error: %v", err),
		}
	}
	if count == 0 {
		return HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("OpenCitations returned zero citations for %s; index may be degraded", openCitationsHealthCheckDOI),
		}
	}
	return HealthStatus{
		Healthy: true,
		Message: "OpenCitations API reachable",
	}
}

// FetchReferenceDOIs returns the DOIs of the works this paper cites
// (outgoing edges), de-duplicated with first-seen order preserved. A
// limit of zero or less means no cap. Edges whose cited entity has no
// DOI (OMID- or PMID-only) are skipped.
func (o *OpenCitationsBackend) FetchReferenceDOIs(ctx context.Context, doi string, limit int) ([]string, error) {
	var edges []openCitationsEdge
	if err := o.getJSON(ctx, "references", doi, &edges); err != nil {
		return nil, fmt.Errorf("opencitations references error: %w", err)
	}
	return collectUniqueDOIs(edges, func(edge openCitationsEdge) string { return edge.Cited }, limit), nil
}

// FetchCitationDOIs returns the DOIs of the works that cite this paper
// (incoming edges), de-duplicated with first-seen order preserved. A
// limit of zero or less means no cap.
//
// The API has no server-side limit, so the full edge list is fetched
// and parsed before capping; for heavily cited papers that is a
// multi-megabyte response regardless of the limit requested. Each
// edge is roughly 365 bytes on the wire (1806 edges measured at
// 659 KB), so the shared client's response-size cap rejects papers
// with more than about 44,000 incoming citations with an error
// rather than a partial list.
func (o *OpenCitationsBackend) FetchCitationDOIs(ctx context.Context, doi string, limit int) ([]string, error) {
	var edges []openCitationsEdge
	if err := o.getJSON(ctx, "citations", doi, &edges); err != nil {
		return nil, fmt.Errorf("opencitations citations error: %w", err)
	}
	return collectUniqueDOIs(edges, func(edge openCitationsEdge) string { return edge.Citing }, limit), nil
}

// CitationCount returns the number of incoming citations recorded for
// the DOI. Unknown DOIs yield zero rather than an error.
func (o *OpenCitationsBackend) CitationCount(ctx context.Context, doi string) (int, error) {
	var rows []openCitationsCountRow
	if err := o.getJSON(ctx, "citation-count", doi, &rows); err != nil {
		return 0, fmt.Errorf("opencitations citation-count error: %w", err)
	}
	// The API always returns exactly one row, even for unknown DOIs
	// (count "0"). An empty array means the response shape has changed,
	// which is worth surfacing rather than silently reporting zero.
	if len(rows) == 0 {
		return 0, fmt.Errorf("opencitations citation-count returned no rows for %s", doi)
	}
	count, err := rows[0].Count.Int64()
	if err != nil {
		return 0, fmt.Errorf("opencitations citation-count %q is not an integer: %w", rows[0].Count, err)
	}
	return int(count), nil
}

// getJSON issues a rate-limited GET for {operation}/doi:{doi} and
// decodes the JSON response. The access token header is attached only
// when a token is configured.
func (o *OpenCitationsBackend) getJSON(ctx context.Context, operation string, doi string, result any) error {
	req := Request{
		Method:  http.MethodGet,
		URL:     o.operationURL(operation, doi),
		Backend: "opencitations",
	}
	if o.accessToken != "" {
		req.Headers = map[string]string{openCitationsAccessTokenHeader: o.accessToken}
	}
	body, err := o.client.Do(ctx, req)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("decoding %s response: %w", operation, err)
	}
	return nil
}

// operationURL builds {baseURL}/{operation}/doi:{doi} with the DOI
// normalised (scheme prefixes stripped, lower-cased) and escaped.
//
// The documented form keeps the DOI's slash raw
// (.../references/doi:10.1038/nature12373); the API also accepts %2F,
// but the raw form is what the examples show, so escaping is applied
// to everything except the slash. Escaping matters because DOIs may
// legitimately contain characters that would otherwise be parsed as
// URL syntax: SICI-style DOIs such as
// 10.1002/(sici)1097-0258(19980430)17:8<857::aid-sim777>3.0.co;2-e
// carry angle brackets and semicolons, and others contain ? or #.
// Verified live that the raw-slash + escaped form resolves that DOI.
func (o *OpenCitationsBackend) operationURL(operation string, doi string) string {
	escapedDOI := strings.ReplaceAll(url.PathEscape(normalizeDOI(doi)), "%2F", "/")
	return fmt.Sprintf("%s/%s/doi:%s", o.baseURL, operation, escapedDOI)
}

// collectUniqueDOIs walks the edges in order, extracts a DOI from each
// via selectIdentifiers, and returns the distinct DOIs in first-seen
// order. Edges without a DOI are skipped. A limit of zero or less
// means no cap. Always returns a non-nil slice so an empty graph is
// distinguishable from an error.
func collectUniqueDOIs(edges []openCitationsEdge, selectIdentifiers func(openCitationsEdge) string, limit int) []string {
	seen := make(map[string]bool, len(edges))
	dois := make([]string, 0, len(edges))
	for _, edge := range edges {
		doi := extractDOIFromIdentifierList(selectIdentifiers(edge))
		if doi == "" || seen[doi] {
			continue
		}
		seen[doi] = true
		dois = append(dois, doi)
		if limit > 0 && len(dois) >= limit {
			break
		}
	}
	return dois
}

// extractDOIFromIdentifierList returns the first DOI in a
// space-separated OpenCitations identifier string such as
// "omid:br/0601 doi:10.1000/xyz pmid:123", normalised (lower-cased,
// scheme stripped). Returns "" when no doi: token is present.
//
// A single entity can list more than one DOI (a published article and
// its preprint, say); the first is taken, since the entity is one
// bibliographic resource and the graph wants one node per resource.
// The documented multi-index format may suffix values with ";" so a
// trailing separator is trimmed before normalising.
func extractDOIFromIdentifierList(identifiers string) string {
	for _, token := range strings.Fields(identifiers) {
		if !strings.HasPrefix(strings.ToLower(token), "doi:") {
			continue
		}
		doi := normalizeDOI(strings.TrimRight(token, ";,"))
		if doi != "" {
			return doi
		}
	}
	return ""
}

// openCitationsEdge is one row of a references/ or citations/
// response. Only the identifier lists are used; the remaining fields
// are declared so the shape is documented and available if the
// citation_graph trend histogram ever wants creation dates.
type openCitationsEdge struct {
	OCI       string `json:"oci"`        // Open Citation Identifier, e.g. "06120344846-06802139111"
	Citing    string `json:"citing"`     // Space-separated identifiers of the citing entity
	Cited     string `json:"cited"`      // Space-separated identifiers of the cited entity
	Creation  string `json:"creation"`   // Publication date of the citing entity, YYYY-MM-DD
	Timespan  string `json:"timespan"`   // XSD duration between cited and citing publication, e.g. "P7Y8M13D"
	JournalSC string `json:"journal_sc"` // "yes" when citing and cited share a journal
	AuthorSC  string `json:"author_sc"`  // "yes" when citing and cited share an author
}

// openCitationsCountRow is the single element of a citation-count or
// reference-count response. The API encodes the count as a JSON string
// ("1806"); json.Number accepts either a quoted numeric string or a
// bare number, so this survives the API tightening its output.
type openCitationsCountRow struct {
	Count json.Number `json:"count"`
}
