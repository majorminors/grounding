package tools

import (
	"context"
	"fmt"
	"sync"

	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

// VerifyCitation verifies a citation across multiple backends.
// DOI verification cascades (first match wins---it's an exact ID).
// Title-based verification queries all backends and returns the
// highest-confidence match, preventing a low-quality early result
// from shadowing a better match in a later backend.
func verifyCitationUnrecorded(ctx context.Context, doi, title string, authors []string, year int) (*backends.VerificationResult, error) {
	cfg := config.LoadConfig()

	// Build HTTP client with polite pool email
	var opts []backends.ClientOption
	if cfg.Email != "" {
		opts = append(opts, backends.WithEmail(cfg.Email))
	}
	client := backends.NewClient(opts...)

	// Initialize backends
	citationBackends := []backends.CitationBackend{
		backends.NewCrossref(client),
		backends.NewSemanticScholar(client, cfg.SemanticScholarKey),
		backends.NewOpenAlex(client, cfg.OpenAlexAPIKey),
		backends.NewEuropePMC(client),
		backends.NewOpenLibrary(client),
	}

	for _, verifier := range citationVerifiersAddedByAddOns {
		if backend, ok := verifier.New(ctx, client, cfg); ok {
			citationBackends = append(citationBackends, backend)
		}
	}

	var lastError error

	// DOI verification: cascade (exact lookup, first match wins)
	if doi != "" {
		for _, backend := range citationBackends {
			result, err := backend.VerifyDOI(ctx, doi)
			if err != nil {
				lastError = err
				continue
			}
			if result.Verified {
				return result, nil
			}
		}
		errorMsg := "DOI not found in any database"
		if lastError != nil {
			errorMsg = fmt.Sprintf("DOI not found (last error: %v)", lastError)
		}
		return &backends.VerificationResult{
			Verified:   false,
			Error:      errorMsg,
			Suggestion: "DOI not found. Check that the DOI is correct — you may be misremembering it. Use web search to confirm.",
		}, nil
	}

	// Title-based verification: query all backends in parallel and
	// return the best match. Backends are independent services, so
	// running them serially only adds their latencies together.
	type titleVerification struct {
		result *backends.VerificationResult
		err    error
	}
	verifications := make([]titleVerification, len(citationBackends))
	var wg sync.WaitGroup
	for i, backend := range citationBackends {
		wg.Add(1)
		go func(index int, b backends.CitationBackend) {
			defer wg.Done()
			result, err := b.VerifyByTitle(ctx, title, authors, year)
			verifications[index] = titleVerification{result: result, err: err}
		}(i, backend)
	}
	wg.Wait()

	// Ties keep the earlier backend (Crossref first), which is the
	// authoritative registry for DOIs.
	var bestResult *backends.VerificationResult
	for _, v := range verifications {
		if v.err != nil {
			lastError = v.err
			continue
		}
		if v.result != nil && v.result.Verified {
			if bestResult == nil || v.result.Confidence > bestResult.Confidence {
				bestResult = v.result
			}
		}
	}

	if bestResult != nil {
		return bestResult, nil
	}

	errorMsg := "citation not found in any database"
	if lastError != nil {
		errorMsg = fmt.Sprintf("citation not found (last error: %v)", lastError)
	}
	return &backends.VerificationResult{
		Verified:   false,
		Error:      errorMsg,
		Suggestion: "Not found. You may have the wrong title, author, or year — or this work may not exist. Verify via web search before citing.",
	}, nil
}
