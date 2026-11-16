package tools

import (
	"context"
	"sync"
	"time"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

// availabilityTimeBudget is how long a search waits for its
// availability providers, all of them together; what is not ready by
// then is left out rather than holding the search.
const availabilityTimeBudget = 8 * time.Second

// availabilityProviders is core's (Unpaywall) and then the add-ons'.
func availabilityProviders() []AvailabilityProvider {
	return append([]AvailabilityProvider{{
		Name: "unpaywall",
		Find: openAccessCopiesFromUnpaywall,
	}}, availabilityProvidersAddedByAddOns...)
}

// fillAvailability asks every provider the caller's tier allows, in
// parallel under one time budget, and merges what they found: for each
// field, the first provider that knows it wins.
func fillAvailability(ctx context.Context, client *backends.Client, cfg config.Config, records []backends.Paper) {
	if len(records) == 0 {
		return
	}
	tier := auth.GetTier(ctx)
	budget, cancel := context.WithTimeout(ctx, availabilityTimeBudget)
	defer cancel()

	providers := availabilityProviders()
	found := make([][]*backends.Availability, len(providers))
	var wg sync.WaitGroup
	for i, provider := range providers {
		if provider.FullTierOnly && tier != auth.TierFull {
			continue
		}
		wg.Add(1)
		go func(i int, provider AvailabilityProvider) {
			defer wg.Done()
			found[i] = provider.Find(budget, client, cfg, records)
		}(i, provider)
	}
	wg.Wait()

	callerHasFindDownloadLink := tier == auth.TierFull && (findDownloadLinkIsRegistered() || CommandLineRunHasFindDownloadLink)
	for r := range records {
		merged := &backends.Availability{}
		for _, fromProvider := range found {
			if r >= len(fromProvider) || fromProvider[r] == nil {
				continue
			}
			a := fromProvider[r]
			if merged.OpenAccessURL == "" && a.OpenAccessURL != "" {
				merged.OpenAccessURL, merged.OpenAccessURLKind, merged.OpenAccessLicense = a.OpenAccessURL, a.OpenAccessURLKind, a.OpenAccessLicense
			}
			if merged.DownloadableFileMD5 == "" {
				merged.DownloadableFileMD5 = a.DownloadableFileMD5
			}
		}
		if callerHasFindDownloadLink {
			switch {
			case merged.OpenAccessURLKind == "pdf" && records[r].DOI != "":
				merged.FindDownloadLinkArguments = map[string]string{"doi": records[r].DOI}
			case merged.DownloadableFileMD5 != "":
				merged.FindDownloadLinkArguments = map[string]string{"md5": merged.DownloadableFileMD5}
			case records[r].Kind == backends.RecordKindBook && records[r].Title != "" && DownloadProvidersAreRegistered():
				// Found at download time, not now: looking a book's file up
				// costs a search page and several rate-limited record
				// fetches (5s and more), which only a download should pay.
				merged.FindDownloadLinkArguments = findDownloadLinkArgumentsForBook(records[r])
			}
		}
		if merged.OpenAccessURL != "" || merged.DownloadableFileMD5 != "" || merged.FindDownloadLinkArguments != nil {
			records[r].Availability = merged
		}
	}
}

// findDownloadLinkArgumentsForBook names a book by its title and first
// author, the key find_download_link finds its file by.
func findDownloadLinkArgumentsForBook(book backends.Paper) map[string]string {
	arguments := map[string]string{"title": book.Title}
	if len(book.Authors) > 0 && book.Authors[0] != "" {
		arguments["author"] = book.Authors[0]
	}
	return arguments
}

// CommandLineRunHasFindDownloadLink is set by a command-line search:
// its find-download-link command exists whether or not the key is in the
// environment (it fetches the key when it needs it), unlike the MCP
// tool, which is registered only with the key.
var CommandLineRunHasFindDownloadLink bool

// findDownloadLinkIsRegistered reports whether this server offers
// find_download_link at all (it needs its key).
func findDownloadLinkIsRegistered() bool {
	_, ok := Lookup("find_download_link")
	return ok
}

// openAccessCopiesFromUnpaywall looks up each record with a DOI, in
// parallel; Unpaywall needs the contact email.
func openAccessCopiesFromUnpaywall(ctx context.Context, client *backends.Client, cfg config.Config, records []backends.Paper) []*backends.Availability {
	found := make([]*backends.Availability, len(records))
	if cfg.Email == "" {
		return found
	}
	unpaywall := backends.NewUnpaywall(client, cfg.Email)
	var wg sync.WaitGroup
	for i, record := range records {
		if record.DOI == "" {
			continue
		}
		wg.Add(1)
		go func(i int, doi string) {
			defer wg.Done()
			location, err := unpaywall.FindOpenAccess(ctx, doi)
			if err != nil || location == nil || !location.IsOA {
				return
			}
			a := &backends.Availability{OpenAccessLicense: location.License}
			switch {
			case location.PDFURL != "":
				a.OpenAccessURL, a.OpenAccessURLKind = location.PDFURL, "pdf"
			case location.URL != "":
				a.OpenAccessURL, a.OpenAccessURLKind = location.URL, "landing_page"
			default:
				return
			}
			found[i] = a
		}(i, record.DOI)
	}
	wg.Wait()
	return found
}
