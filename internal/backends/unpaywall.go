package backends

// Unpaywall resolves a DOI to its legal open-access location, if one
// exists. It is a resolver, not a SearchBackend: one DOI in, one
// answer out.
//
// Documentation (verified live 2026-09-17; both pages are a JS
// single-page app, so plain fetches return nothing useful):
//   - REST API:    https://unpaywall.org/products/api
//   - Data format: https://unpaywall.org/data-format
//
// Endpoint: GET https://api.unpaywall.org/v2/{doi}?email={email}
//
// The email parameter is mandatory. The API answers HTTP 422 when it is
// missing ("Email address required in API call") and also 422 for
// placeholder addresses such as example.com ("Please use your own
// email address in API calls"), so the backend refuses to run without
// GROUNDING_EMAIL rather than burning a request on a guaranteed 422.
//
// The documented usage cap is a courtesy limit of 100,000 calls per day;
// past that Unpaywall recommends the database snapshot, not the API.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	unpaywallBaseURL = "https://api.unpaywall.org"

	// 100,000 calls per day is ~1.16 req/s sustained, but interactive
	// use is a handful of lookups per tool call, not a stream. 10 req/s
	// with burst 1 keeps a runaway loop well under the daily cap while
	// never making a single lookup wait.
	unpaywallRPS = 10.0

	// unpaywallHealthCheckDOI is Ioannidis (2005), "Why Most Published
	// Research Findings Are False", PLOS Medicine. PLOS is a fully gold
	// open-access publisher (CC BY), so Unpaywall should always report
	// is_oa=true for it. NB: not verified against the live API from this
	// node, because the API rejects placeholder emails and no
	// GROUNDING_EMAIL was configured here.
	unpaywallHealthCheckDOI = "10.1371/journal.pmed.0020124"
)

// ErrUnpaywallEmailRequired is returned by FindOpenAccess when the
// backend was constructed without an email address. Exported so callers
// can distinguish "not configured" from a network or API failure.
var ErrUnpaywallEmailRequired = errors.New("GROUNDING_EMAIL is required for Unpaywall")

// OpenAccessLocation is the result of resolving a DOI through Unpaywall.
//
// Three outcomes are distinguishable to the caller:
//   - FindOpenAccess returns nil: the DOI is unknown to Unpaywall.
//   - IsOA is false and the URL fields are empty: Unpaywall knows the
//     DOI but found no legal open-access copy.
//   - IsOA is true: URL fields describe Unpaywall's best_oa_location.
type OpenAccessLocation struct {
	DOI            string `json:"doi"`
	IsOA           bool   `json:"is_oa"`
	OAStatus       string `json:"oa_status,omitempty"`        // gold, hybrid, bronze, green, closed
	URL            string `json:"url,omitempty"`              // PDF URL when there is one, else landing page
	PDFURL         string `json:"pdf_url,omitempty"`          // Direct PDF, if Unpaywall found one
	LandingPageURL string `json:"landing_page_url,omitempty"` // Page describing the OA copy (often HTML full text)
	HostType       string `json:"host_type,omitempty"`        // "publisher" or "repository"
	Version        string `json:"version,omitempty"`          // submittedVersion, acceptedVersion, publishedVersion
	License        string `json:"license,omitempty"`          // e.g. "cc-by", "implied-oa"; empty when unknown
	Title          string `json:"title,omitempty"`
	Year           int    `json:"year,omitempty"`
}

// UnpaywallBackend resolves DOIs to open-access locations via the
// Unpaywall REST API.
type UnpaywallBackend struct {
	client  *Client
	email   string // Mandatory ?email= parameter; the API returns 422 without it
	baseURL string // Overridable so tests can point at an httptest server
}

// NewUnpaywall creates a new Unpaywall backend. An empty email is
// permitted at construction so the backend can still be listed and
// health-checked; FindOpenAccess will refuse to run until one is set.
func NewUnpaywall(client *Client, email string) *UnpaywallBackend {
	ensureRateLimit("unpaywall", unpaywallRPS, 1)
	return &UnpaywallBackend{
		client:  client,
		email:   strings.TrimSpace(email),
		baseURL: unpaywallBaseURL,
	}
}

// Name returns the backend identifier.
func (u *UnpaywallBackend) Name() string {
	return "unpaywall"
}

// HealthCheck tests if the Unpaywall API is reachable by resolving a
// DOI that is gold open access by publisher policy. Without an email
// the API cannot be called at all, so the backend reports itself as
// available-but-unconfigured rather than unhealthy: nothing is broken,
// it just needs GROUNDING_EMAIL.
func (u *UnpaywallBackend) HealthCheck(ctx context.Context) HealthStatus {
	if u.email == "" {
		return HealthStatus{
			Healthy: true,
			Message: "available (GROUNDING_EMAIL not set)",
		}
	}

	location, err := u.FindOpenAccess(ctx, unpaywallHealthCheckDOI)
	if err != nil {
		return HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("Unpaywall API error: %v", err),
		}
	}
	if location == nil {
		// The API answered, but claims not to know a DOI it certainly
		// indexes. Either the request shape has drifted or their data
		// has; both are worth surfacing rather than calling it healthy.
		return HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("Unpaywall API reachable but returned 404 for known DOI %s", unpaywallHealthCheckDOI),
		}
	}
	return HealthStatus{
		Healthy: true,
		Message: "Unpaywall API reachable",
	}
}

// FindOpenAccess resolves a DOI to its best legal open-access location.
//
// Returns (nil, nil) when Unpaywall does not know the DOI (HTTP 404).
// Returns a location with IsOA false and no URLs when the DOI is known
// but has no open-access copy. Returns ErrUnpaywallEmailRequired when
// the backend has no email configured.
func (u *UnpaywallBackend) FindOpenAccess(ctx context.Context, doi string) (*OpenAccessLocation, error) {
	if u.email == "" {
		return nil, ErrUnpaywallEmailRequired
	}

	doi = normalizeDOI(doi)
	if doi == "" {
		return nil, fmt.Errorf("unpaywall: empty DOI")
	}

	var doiObject unpaywallDOIObject
	if err := u.client.GetJSON(ctx, u.lookupURL(doi), "unpaywall", &doiObject); err != nil {
		if IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("unpaywall lookup error: %w", err)
	}

	location := unpaywallDOIObjectToLocation(doiObject)
	if location.DOI == "" {
		location.DOI = doi
	}
	return location, nil
}

// lookupURL builds the /v2/{doi} request URL. The DOI's slashes are
// path separators as far as the API is concerned, so each segment is
// escaped individually rather than the DOI as a whole (which would turn
// "/" into "%2F"). Characters that are legal in DOIs but not in URL
// paths (e.g. "<", ">" in SICI-style DOIs) are percent-encoded.
func (u *UnpaywallBackend) lookupURL(doi string) string {
	segments := strings.Split(doi, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	params := url.Values{}
	params.Set("email", u.email)
	return u.baseURL + "/v2/" + strings.Join(segments, "/") + "?" + params.Encode()
}

// unpaywallDOIObjectToLocation flattens a DOI object into the
// OpenAccessLocation the rest of the system consumes. Only the
// best_oa_location is surfaced; the docs describe the full oa_locations
// list as "primarily for research purposes".
func unpaywallDOIObjectToLocation(doiObject unpaywallDOIObject) *OpenAccessLocation {
	location := &OpenAccessLocation{
		DOI:      doiObject.DOI,
		IsOA:     doiObject.IsOA,
		OAStatus: doiObject.OAStatus,
		Title:    doiObject.Title,
		Year:     doiObject.Year,
	}

	// is_oa is documented as "true when best_oa_location is not null",
	// so treat it as authoritative: a false is_oa means no URLs, whatever
	// else the payload carries.
	if !doiObject.IsOA {
		return location
	}

	best := doiObject.BestOALocation
	if best == nil {
		// Should not happen given the is_oa definition above, but the
		// docs also say fields may change without notice. Fall back to
		// the location flagged is_best, then to the first listed.
		for i := range doiObject.OALocations {
			if doiObject.OALocations[i].IsBest {
				best = &doiObject.OALocations[i]
				break
			}
		}
		if best == nil && len(doiObject.OALocations) > 0 {
			best = &doiObject.OALocations[0]
		}
	}
	if best == nil {
		return location
	}

	location.PDFURL = best.URLForPDF
	location.LandingPageURL = best.URLForLandingPage
	location.HostType = best.HostType
	location.Version = best.Version
	location.License = best.License

	// The API's "url" is already "url_for_pdf if there is one, otherwise
	// the landing page"; recompute the same fallback in case it is empty.
	location.URL = best.URL
	if location.URL == "" {
		location.URL = best.URLForPDF
	}
	if location.URL == "" {
		location.URL = best.URLForLandingPage
	}

	return location
}

// unpaywallDOIObject is the subset of Unpaywall's "DOI object" this
// backend reads. The data-format guide warns that new fields may be
// added at any time; unknown fields are simply ignored on unmarshal.
// Nullable fields (title, year) unmarshal to their zero values.
type unpaywallDOIObject struct {
	DOI            string                `json:"doi"` // Always lowercase per the docs
	IsOA           bool                  `json:"is_oa"`
	OAStatus       string                `json:"oa_status"`
	Title          string                `json:"title"`
	Year           int                   `json:"year"`
	BestOALocation *unpaywallOALocation  `json:"best_oa_location"` // null when no OA copy exists
	OALocations    []unpaywallOALocation `json:"oa_locations"`
}

// unpaywallOALocation is Unpaywall's "OA Location object", one place a
// copy of the resource was found.
type unpaywallOALocation struct {
	URL               string `json:"url"` // url_for_pdf if present, else url_for_landing_page
	URLForPDF         string `json:"url_for_pdf"`
	URLForLandingPage string `json:"url_for_landing_page"`
	HostType          string `json:"host_type"` // "publisher" or "repository"
	Version           string `json:"version"`   // DRIVER Guidelines v2.0 version names
	License           string `json:"license"`   // Normalised CC abbreviation, "implied-oa", or null
	IsBest            bool   `json:"is_best"`
}
