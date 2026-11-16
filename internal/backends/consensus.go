package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const (
	consensusBaseURL = "https://api.consensus.app/v1"
	consensusRPS     = 1.0 // conservative; API docs don't specify limits
)

// ConsensusBackend implements SearchBackend using the Consensus.app API.
type ConsensusBackend struct {
	client *Client
	apiKey string
}

// NewConsensus creates a new Consensus backend.
func NewConsensus(client *Client, apiKey string) *ConsensusBackend {
	ensureRateLimit("consensus", consensusRPS, 1)
	return &ConsensusBackend{client: client, apiKey: apiKey}
}

// Name returns the backend identifier.
func (c *ConsensusBackend) Name() string {
	return "consensus"
}

// HealthCheck tests if the Consensus API is reachable.
func (c *ConsensusBackend) HealthCheck(ctx context.Context) HealthStatus {
	if c.apiKey == "" {
		return HealthStatus{
			Healthy: false,
			Message: "no API key configured (CONSENSUS_APP_KEY)",
		}
	}

	u := consensusBaseURL + "/quick_search?query=test"
	_, err := c.client.Do(ctx, Request{
		URL:     u,
		Backend: "consensus",
		Headers: map[string]string{
			"x-api-key": c.apiKey,
		},
	})
	if err != nil {
		return HealthStatus{
			Healthy: false,
			Message: fmt.Sprintf("Consensus API error: %v", err),
		}
	}
	return HealthStatus{
		Healthy: true,
		Message: "Consensus API reachable",
	}
}

// Search returns papers matching the query via Consensus quick_search.
func (c *ConsensusBackend) Search(ctx context.Context, query string, limit int, filters SearchFilters) ([]Paper, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("consensus: no API key configured")
	}
	if limit <= 0 {
		limit = 10
	}

	params := url.Values{}
	params.Set("query", query)
	if filters.YearMin > 0 {
		params.Set("year_min", fmt.Sprintf("%d", filters.YearMin))
	}
	if filters.YearMax > 0 {
		params.Set("year_max", fmt.Sprintf("%d", filters.YearMax))
	}

	u := consensusBaseURL + "/quick_search?" + params.Encode()

	body, err := c.client.Do(ctx, Request{
		URL:     u,
		Backend: "consensus",
		Headers: map[string]string{
			"x-api-key": c.apiKey,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("consensus search error: %w", err)
	}

	var resp consensusSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("consensus: failed to parse response: %w", err)
	}

	papers := make([]Paper, 0, len(resp.Results))
	for _, r := range resp.Results {
		if len(papers) >= limit {
			break
		}
		papers = append(papers, consensusResultToPaper(r))
	}

	return papers, nil
}

func consensusResultToPaper(r consensusResult) Paper {
	authors := make([]string, len(r.Authors))
	copy(authors, r.Authors)

	paperURL := r.URL
	if paperURL == "" && r.DOI != "" {
		paperURL = "https://doi.org/" + r.DOI
	}

	return Paper{
		Kind:     RecordKindPaper,
		Title:    r.Title,
		Authors:  authors,
		Year:     r.PublishYear,
		DOI:      r.DOI,
		Abstract: r.Abstract,
		// The takeaway is Consensus's model summarising the study, so it
		// has a field of its own rather than leading the abstract.
		SummaryWrittenByConsensus: r.Takeaway,
		URL:                       paperURL,
		Source:                    "consensus",
		Journal:                   r.JournalName,
		Citations:                 r.CitationCount,
		ContentType:               mapStudyType(r.StudyType),
	}
}

// mapStudyType maps Consensus study types to content_type values.
func mapStudyType(st string) string {
	st = strings.TrimSpace(st)
	if st == "" {
		return ""
	}
	return "study:" + st
}

// Consensus API response types

type consensusSearchResponse struct {
	Results []consensusResult `json:"results"`
}

type consensusResult struct {
	Abstract      string   `json:"abstract"`
	Authors       []string `json:"authors"`
	DOI           string   `json:"doi"`
	JournalName   string   `json:"journal_name"`
	Pages         string   `json:"pages"`
	PublishYear   int      `json:"publish_year"`
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Volume        string   `json:"volume"`
	CitationCount int      `json:"citation_count"`
	StudyType     string   `json:"study_type"`
	Takeaway      string   `json:"takeaway"`
}
