package tools

import (
	"context"
	"testing"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

// withAvailabilityProvidersForThisTest replaces the add-ons' providers
// and, when downloadDocument is true, registers a find_download_link
// tool, restoring both when the test ends. Core's Unpaywall finds
// nothing without GROUNDING_EMAIL, which the test clears.
func withAvailabilityProvidersForThisTest(t *testing.T, downloadDocument bool, providers ...AvailabilityProvider) {
	t.Helper()
	t.Setenv("GROUNDING_EMAIL", "")
	savedProviders, savedTools := availabilityProvidersAddedByAddOns, registry
	t.Cleanup(func() { availabilityProvidersAddedByAddOns, registry = savedProviders, savedTools })
	availabilityProvidersAddedByAddOns = providers
	registry = nil
	if downloadDocument {
		Register(ToolDef{Name: "find_download_link"})
	}
}

func providerFinding(name string, fullTierOnly bool, availability func(backends.Paper) *backends.Availability) AvailabilityProvider {
	return AvailabilityProvider{
		Name:         name,
		FullTierOnly: fullTierOnly,
		Find: func(ctx context.Context, client *backends.Client, cfg config.Config, records []backends.Paper) []*backends.Availability {
			found := make([]*backends.Availability, len(records))
			for i, record := range records {
				found[i] = availability(record)
			}
			return found
		},
	}
}

func TestAvailabilityMergesProvidersAndOffersDownloadDocumentOnlyToCallersWhoHaveIt(t *testing.T) {
	openAccess := providerFinding("open", false, func(p backends.Paper) *backends.Availability {
		if p.DOI == "" {
			return nil
		}
		return &backends.Availability{OpenAccessURL: "https://repository.example/" + p.DOI + ".pdf", OpenAccessURLKind: "pdf", OpenAccessLicense: "cc-by"}
	})
	bookFiles := providerFinding("books", true, func(p backends.Paper) *backends.Availability {
		if p.Kind != backends.RecordKindBook {
			return nil
		}
		return &backends.Availability{DownloadableFileMD5: "0123456789abcdef0123456789abcdef"}
	})
	records := func() []backends.Paper {
		return []backends.Paper{
			{Title: "An open paper", DOI: "10.1/open", Kind: backends.RecordKindPaper},
			{Title: "A book", ISBN: "9780000000000", Kind: backends.RecordKindBook},
			{Title: "A closed paper", Kind: backends.RecordKindPaper},
		}
	}

	t.Run("full tier with find_download_link", func(t *testing.T) {
		withAvailabilityProvidersForThisTest(t, true, openAccess, bookFiles)
		got := records()
		fillAvailability(auth.WithTier(context.Background(), auth.TierFull), backends.NewClient(), config.Config{}, got)
		if a := got[0].Availability; a == nil || a.OpenAccessURLKind != "pdf" || a.FindDownloadLinkArguments["doi"] != "10.1/open" {
			t.Errorf("open paper availability = %+v", a)
		}
		if a := got[1].Availability; a == nil || a.FindDownloadLinkArguments["md5"] != "0123456789abcdef0123456789abcdef" {
			t.Errorf("book availability = %+v", a)
		}
		if got[2].Availability != nil {
			t.Errorf("closed paper availability = %+v, want none", got[2].Availability)
		}
	})

	t.Run("public tier", func(t *testing.T) {
		withAvailabilityProvidersForThisTest(t, true, openAccess, bookFiles)
		got := records()
		fillAvailability(auth.WithTier(context.Background(), auth.TierPublic), backends.NewClient(), config.Config{}, got)
		if a := got[0].Availability; a == nil || a.OpenAccessURL == "" || a.FindDownloadLinkArguments != nil {
			t.Errorf("open paper availability = %+v, want the open-access link and no find_download_link arguments", a)
		}
		if got[1].Availability != nil {
			t.Errorf("a full-tier-only provider answered a public caller: %+v", got[1].Availability)
		}
	})

	t.Run("full tier on a server without find_download_link", func(t *testing.T) {
		withAvailabilityProvidersForThisTest(t, false, openAccess, bookFiles)
		got := records()
		fillAvailability(auth.WithTier(context.Background(), auth.TierFull), backends.NewClient(), config.Config{}, got)
		if a := got[1].Availability; a == nil || a.DownloadableFileMD5 == "" || a.FindDownloadLinkArguments != nil {
			t.Errorf("book availability = %+v, want the md5 and no arguments for a tool this server lacks", a)
		}
	})
}

func TestAvailabilityWaitsNoLongerThanItsBudgetForASlowProvider(t *testing.T) {
	slow := AvailabilityProvider{
		Name: "slow",
		Find: func(ctx context.Context, client *backends.Client, cfg config.Config, records []backends.Paper) []*backends.Availability {
			<-ctx.Done()
			return nil
		},
	}
	withAvailabilityProvidersForThisTest(t, false, slow)
	ctx, cancel := context.WithCancel(auth.WithTier(context.Background(), auth.TierFull))
	cancel() // a search whose caller has gone: the budget is already spent
	records := []backends.Paper{{Title: "x"}}
	fillAvailability(ctx, backends.NewClient(), config.Config{}, records)
	if records[0].Availability != nil {
		t.Errorf("availability = %+v", records[0].Availability)
	}
}

// A book no provider has a file for is found at download time, by its
// title and first author; only a caller with find_download_link is told.
func TestABookIsOfferedForDownloadByTitleAndAuthor(t *testing.T) {
	records := func() []backends.Paper {
		return []backends.Paper{
			{Title: "The New Monogamy", Authors: []string{"Tammy Nelson", "Second Author"}, Kind: backends.RecordKindBook},
			{Title: "An anonymous book", Kind: backends.RecordKindBook},
			{Title: "A closed paper", Kind: backends.RecordKindPaper},
		}
	}

	withAvailabilityProvidersForThisTest(t, true)
	withDownloadProvidersForThisTest(t)
	coreAlone := records()
	fillAvailability(auth.WithTier(context.Background(), auth.TierFull), backends.NewClient(), config.Config{}, coreAlone)
	if coreAlone[0].Availability != nil {
		t.Errorf("with no download source a book was offered for download: %+v", coreAlone[0].Availability)
	}

	withDownloadProvidersForThisTest(t, DownloadProvider{Name: "books", Takes: func(d DocumentToDownload) bool { return d.IsBook() }})
	got := records()
	fillAvailability(auth.WithTier(context.Background(), auth.TierFull), backends.NewClient(), config.Config{}, got)
	if a := got[0].Availability; a == nil || a.FindDownloadLinkArguments["title"] != "The New Monogamy" || a.FindDownloadLinkArguments["author"] != "Tammy Nelson" {
		t.Errorf("book availability = %+v", a)
	}
	if a := got[1].Availability; a == nil || len(a.FindDownloadLinkArguments) != 1 || a.FindDownloadLinkArguments["title"] == "" {
		t.Errorf("anonymous book availability = %+v, want its title alone", a)
	}
	if got[2].Availability != nil {
		t.Errorf("closed paper availability = %+v, want none", got[2].Availability)
	}

	publicRecords := records()
	fillAvailability(auth.WithTier(context.Background(), auth.TierPublic), backends.NewClient(), config.Config{}, publicRecords)
	if publicRecords[0].Availability != nil {
		t.Errorf("a public caller was offered find_download_link: %+v", publicRecords[0].Availability)
	}
}
