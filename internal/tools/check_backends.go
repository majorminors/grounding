package tools

import (
	"context"
	"fmt"
	"sync"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
)

// HealthResult holds the aggregated health status of all backends.
type HealthResult struct {
	Backends map[string]backends.HealthStatus `json:"backends"`
	Healthy  bool                             `json:"healthy"`
}

// CheckBackendsOptions configures the health check behavior.
type CheckBackendsOptions struct {
	// Force runs the probes that cost something: Semantic Scholar
	// without an API key (its shared rate budget), and the billed
	// Consensus and Exa searches. Without it those report as
	// configured-but-unprobed.
	Force bool
}

// CheckBackends checks the health of all backends in parallel.
// It's the shared business logic called by both the MCP handler and CLI.
func CheckBackends(ctx context.Context, opts *CheckBackendsOptions) *HealthResult {
	if opts == nil {
		opts = &CheckBackendsOptions{}
	}

	cfg := config.LoadConfig()

	// Build HTTP client
	var clientOpts []backends.ClientOption
	if cfg.Email != "" {
		clientOpts = append(clientOpts, backends.WithEmail(cfg.Email))
	}
	client := backends.NewClient(clientOpts...)

	// Create backends
	crossref := backends.NewCrossref(client)
	semanticScholar := backends.NewSemanticScholar(client, cfg.SemanticScholarKey)
	openalex := backends.NewOpenAlex(client, cfg.OpenAlexAPIKey)
	wikipedia := backends.NewWikipedia(client)
	openlibrary := backends.NewOpenLibrary(client)
	europepmc := backends.NewEuropePMC(client)
	opencitations := backends.NewOpenCitations(client, cfg.OpenCitationsAccessToken)
	unpaywall := backends.NewUnpaywall(client, cfg.Email)
	consensus := backends.NewConsensus(client, cfg.ConsensusAppKey)
	exa := backends.NewExa(client, cfg.ExaAPIKey)

	result := &HealthResult{
		Backends: make(map[string]backends.HealthStatus),
		Healthy:  true,
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	// Check each backend in parallel
	checkBackend := func(name string, check func(context.Context) backends.HealthStatus) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status := check(ctx)
			mu.Lock()
			result.Backends[name] = status
			if !status.Healthy {
				result.Healthy = false
			}
			mu.Unlock()
		}()
	}

	checkBackend("crossref", crossref.HealthCheck)
	checkBackend("openalex", openalex.HealthCheck)
	checkBackend("wikipedia", wikipedia.HealthCheck)
	checkBackend("openlibrary", openlibrary.HealthCheck)
	checkBackend("europepmc", europepmc.HealthCheck)
	checkBackend("opencitations", opencitations.HealthCheck)

	// reportWithoutProbing records a backend whose real health check is
	// a billed query. Whether it is configured is free to answer and
	// is what a caller usually wants to know.
	reportWithoutProbing := func(name string, configured bool, variable, probeCost string) {
		status := backends.HealthStatus{Healthy: true}
		if configured {
			status.Message = fmt.Sprintf("configured (not probed — a probe costs %s; pass force: true to test)", probeCost)
		} else {
			status.Message = fmt.Sprintf("not configured (set %s)", variable)
		}
		mu.Lock()
		result.Backends[name] = status
		mu.Unlock()
	}

	tier := auth.GetTier(ctx)
	for _, probe := range healthProbesAddedByAddOns {
		if !probe.FullTierOnly || tier == auth.TierFull {
			checkBackend(probe.Name, probe.Check(client, cfg))
		}
	}
	if tier == auth.TierFull {
		checkBackend("unpaywall", unpaywall.HealthCheck)
		if opts.Force {
			checkBackend("consensus", consensus.HealthCheck)
			checkBackend("exa", exa.HealthCheck)
		} else {
			reportWithoutProbing("consensus", cfg.ConsensusAppKey != "", "CONSENSUS_APP_KEY", "~1¢")
			reportWithoutProbing("exa", cfg.ExaAPIKey != "", "EXA_API_KEY", "~$0.007")
		}
		// The web tools' backends are never probed, force or not: a
		// probe would be a billed search, scrape or model call, and
		// whether the key is set is what tells a caller they will work.
		reportKeyOnly := func(name string, configured bool, variable string) {
			status := backends.HealthStatus{Healthy: true, Message: fmt.Sprintf("not configured (set %s)", variable)}
			if configured {
				status.Message = "configured (never probed: every call costs money or usage)"
			}
			mu.Lock()
			result.Backends[name] = status
			mu.Unlock()
		}
		reportKeyOnly("brave", cfg.BraveAPIKey != "", "BRAVE_API_KEY")
		// The page renderers: crawl4ai's health endpoint is free and is
		// asked; Firecrawl is not called, but its line
		// says how much of today's credit cap is spent.
		pageReaders := webPageReadersFromConfig()
		checkBackend("crawl4ai", pageReaders.crawl4ai.HealthCheck)
		checkBackend("firecrawl", pageReaders.firecrawl.HealthCheck)
		checkBackend("pdftotext", func(context.Context) backends.HealthStatus {
			if pdfToTextAvailable() {
				return backends.HealthStatus{Healthy: true, Message: "installed: the plain fetch reads PDFs"}
			}
			return backends.HealthStatus{Healthy: true, Message: "not installed (poppler-utils): PDFs are read only by renderer \"firecrawl\""}
		})
		reportKeyOnly("web-page-reader", cfg.AnthropicAPIKey != "" || cfg.ResearchClaudeCodeOAuthToken != "", "ANTHROPIC_API_KEY or GROUNDING_RESEARCH_CLAUDE_CODE_OAUTH_TOKEN")
	}

	// Semantic Scholar: skip health check request if no API key (unless forced)
	// This preserves the precious 1 req/sec rate limit for actual searches.
	if cfg.SemanticScholarKey != "" || opts.Force {
		checkBackend("semantic-scholar", semanticScholar.HealthCheck)
	} else {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			result.Backends["semantic-scholar"] = backends.HealthStatus{
				Healthy: true,
				Message: "available (rate-limited: 1 req/sec, no API key configured)",
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Override status for backends in cooldown
	if expiry, active := backends.InCooldown("semantic-scholar"); active {
		status := result.Backends["semantic-scholar"]
		status.Healthy = false
		status.Message = fmt.Sprintf(
			"rate limited — cooldown until %s",
			expiry.Format("15:04:05"),
		)
		result.Backends["semantic-scholar"] = status
		result.Healthy = false
	}

	return result
}
