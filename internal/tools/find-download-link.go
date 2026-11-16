package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

// find_download_link finds a link to a copy of a document; on the
// command line only, it also saves the file when asked (--save-to).
// Core tries a
// legal open-access copy first (Unpaywall, by DOI); only when there is
// none does it ask the download providers an add-on registered
// (registry-of-search-sources-citation-verifiers-and-health-probes.go's
// seam, extended here), in their order, the first that takes the
// document answering. Core has no provider of its own beyond
// Unpaywall.

// DocumentToDownload is what a caller named: exactly one of a DOI, an
// MD5 (a file hash a provider holds), or a book (a title, with an
// author if known, or an ISBN).
type DocumentToDownload struct {
	DOI, MD5, Title, Author, ISBN string
}

// IsBook is whether the document is named as a book, by title or ISBN.
func (d DocumentToDownload) IsBook() bool {
	return strings.TrimSpace(d.Title) != "" || strings.TrimSpace(d.ISBN) != ""
}

// Validate refuses anything but exactly one way of naming a document.
func (d DocumentToDownload) Validate() error {
	named := 0
	for _, given := range []bool{d.DOI != "", d.MD5 != "", d.IsBook()} {
		if given {
			named++
		}
	}
	switch {
	case named == 0 && strings.TrimSpace(d.Author) != "":
		return fmt.Errorf("an author needs a title")
	case named == 0:
		return fmt.Errorf("one of doi, md5, or title (with author) is required")
	case named > 1:
		return fmt.Errorf("give only one of doi, md5, or title (with author)")
	}
	return nil
}

// DownloadQuota is a provider's daily allowance, when it has one.
type DownloadQuota struct {
	DownloadsLeft   int `json:"downloads_left"`
	DownloadsPerDay int `json:"downloads_per_day"`
	DownloadsDone   int `json:"downloads_done_today"`
}

// FoundDocumentCopy is find_download_link's answer.
type FoundDocumentCopy struct {
	// Source is "unpaywall" (a legal open-access copy) or the name of
	// the provider that had it.
	Source      string `json:"source"`
	OpenAccess  bool   `json:"open_access,omitempty"`
	License     string `json:"license,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
	// DownloadURLKind is "pdf" or "landing_page" for Unpaywall (it
	// sometimes knows a work is open access but has only the
	// publisher's page) and "file" for a provider's link.
	DownloadURLKind string         `json:"download_url_kind,omitempty"`
	FilePath        string         `json:"file_path,omitempty"`
	Title           string         `json:"title,omitempty"`
	Authors         []string       `json:"authors,omitempty"`
	Year            int            `json:"year,omitempty"`
	Extension       string         `json:"extension,omitempty"`
	QuotaInfo       *DownloadQuota `json:"quota,omitempty"`
	ResolvedMD5     string         `json:"resolved_md5,omitempty"`
	ResolvedFromDOI bool           `json:"resolved_from_doi,omitempty"`
	// FoundByTitle is set when a book's title (or ISBN) was searched
	// for; MatchConfidence is how well the file's title and author
	// matched (0 when found by ISBN alone).
	FoundByTitle    bool    `json:"found_by_title,omitempty"`
	MatchConfidence float64 `json:"match_confidence,omitempty"`
	Caution         string  `json:"caution,omitempty"`
}

// DownloadProvider is a source of files beyond the open-access copy,
// registered by an add-on.
type DownloadProvider struct {
	Name string

	// Takes is whether the provider can look for the document at all
	// (one holding MD5s takes an MD5; one finding books, a title).
	Takes func(document DocumentToDownload) bool

	// Download finds the document's link and, when directory is not
	// empty (a command-line --save-to), saves the file there. parameters are the call's own arguments the
	// provider added to the tool (AddParameterToTool), by name. It may
	// need a key: on a command-line run (IsCommandLineRun) it may fetch
	// one by running a command, never on the server.
	Download func(ctx context.Context, client *backends.Client, cfg config.Config, document DocumentToDownload, parameters map[string]any, directory string) (*FoundDocumentCopy, error)
}

var downloadProvidersAddedByAddOns []DownloadProvider

// RegisterDownloadProvider adds a provider after any registered before
// it. Call it from an init().
func RegisterDownloadProvider(provider DownloadProvider) {
	downloadProvidersAddedByAddOns = append(downloadProvidersAddedByAddOns, provider)
}

// DownloadProvidersAreRegistered is whether this build has any source
// of files beyond the open-access copy.
func DownloadProvidersAreRegistered() bool {
	return len(downloadProvidersAddedByAddOns) > 0
}

// DefaultSaveToDirectory is where the command line saves a file when
// --save-to is not given (GROUNDING_DOWNLOAD_DIRECTORY), "" for links
// only. The MCP tool never saves.
func DefaultSaveToDirectory() string {
	return strings.TrimSpace(os.Getenv("GROUNDING_DOWNLOAD_DIRECTORY"))
}

// FindDownloadLink finds the document: the legal open-access copy of a
// DOI first, then the first provider that takes it. With saveToDirectory
// set (the command line's --save-to; never the server) it saves the file
// there too.
func FindDownloadLink(ctx context.Context, document DocumentToDownload, parameters map[string]any, saveToDirectory string) (*FoundDocumentCopy, error) {
	if err := document.Validate(); err != nil {
		return nil, err
	}
	document.MD5 = strings.ToLower(strings.TrimSpace(document.MD5))
	cfg := config.LoadConfig()
	client := backends.NewClient(backends.WithEmail(cfg.Email))
	directory := saveToDirectory

	if document.DOI != "" && cfg.Email != "" {
		if openAccess, ok := downloadOpenAccess(ctx, client, cfg.Email, document.DOI, directory); ok {
			return openAccess, nil
		}
	}

	for _, provider := range downloadProvidersAddedByAddOns {
		if !provider.Takes(document) {
			continue
		}
		downloaded, err := provider.Download(ctx, client, cfg, document, parameters, directory)
		if err != nil && document.DOI != "" {
			if cfg.Email == "" {
				return nil, fmt.Errorf("no open-access lookup (Unpaywall needs GROUNDING_EMAIL) and %w", err)
			}
			return nil, fmt.Errorf("no open-access copy of %s on Unpaywall, and %w", document.DOI, err)
		}
		return downloaded, err
	}

	switch {
	case document.DOI != "" && cfg.Email == "":
		return nil, fmt.Errorf("no open-access lookup (Unpaywall needs GROUNDING_EMAIL), and this server has no other download source")
	case document.DOI != "":
		return nil, fmt.Errorf("no open-access copy of %s on Unpaywall, and this server has no other download source", document.DOI)
	default:
		return nil, fmt.Errorf("this server has no download source that takes an MD5 or a book's title: only a DOI with a legal open-access copy")
	}
}

// doiFilename turns a DOI into a filename that survives every
// filesystem: the slash and other separators become underscores.
func doiFilename(doi string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(doi)
}

// downloadOpenAccess looks the DOI up on Unpaywall and, when a legal
// open-access copy exists, returns it (fetching to directory when one
// is set). ok is false when there is no usable copy or the fetch
// failed, in which case the caller falls back to the providers.
func downloadOpenAccess(ctx context.Context, client *backends.Client, email, doi, directory string) (*FoundDocumentCopy, bool) {
	unpaywall := backends.NewUnpaywall(client, email)
	location, err := unpaywall.FindOpenAccess(ctx, doi)
	if err != nil || location == nil || !location.IsOA {
		return nil, false
	}
	fileURL, urlKind := location.PDFURL, "pdf"
	if fileURL == "" {
		fileURL, urlKind = location.URL, "landing_page"
	}
	if fileURL == "" {
		return nil, false
	}

	result := &FoundDocumentCopy{
		Source:          "unpaywall",
		OpenAccess:      true,
		License:         location.License,
		DownloadURL:     fileURL,
		DownloadURLKind: urlKind,
		Title:           location.Title,
		Year:            location.Year,
		Extension:       "pdf",
	}
	if directory == "" {
		return result, true
	}
	// A landing page can't be saved as the paper; let the caller fall
	// back to a provider for an actual file.
	if urlKind != "pdf" {
		return nil, false
	}

	destPath, err := backends.ContainedPath(directory, doiFilename(location.DOI)+".pdf")
	if err != nil {
		return nil, false
	}
	_, contentType, err := backends.FetchToFile(ctx, client.UserAgent(), fileURL, destPath)
	if err != nil {
		return nil, false
	}
	// Repositories occasionally serve a landing page where a PDF was
	// promised; don't hand that over as the paper.
	if strings.Contains(contentType, "text/html") {
		os.Remove(destPath)
		return nil, false
	}
	result.FilePath = destPath
	return result, true
}
