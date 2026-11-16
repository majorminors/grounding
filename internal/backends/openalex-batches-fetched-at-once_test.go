package backends

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A fake OpenAlex that answers a /works?filter=doi:A|B|... with one work
// per DOI, each titled by its DOI, after a pause, counting how many
// batches it holds at once.
func openAlexServingBatchesSlowly(t *testing.T, failTheBatchHolding string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var inFlight, mostAtOnce atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := inFlight.Add(1)
		defer inFlight.Add(-1)
		for most := mostAtOnce.Load(); now > most && !mostAtOnce.CompareAndSwap(most, now); most = mostAtOnce.Load() {
		}
		time.Sleep(100 * time.Millisecond)

		filter := strings.TrimPrefix(r.URL.Query().Get("filter"), "doi:")
		dois := strings.Split(filter, "|")
		if failTheBatchHolding != "" && strings.Contains(filter, failTheBatchHolding) {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var response openalexSearchResponse
		for _, doi := range dois {
			response.Results = append(response.Results, openalexWork{Title: doi, DOI: "https://doi.org/" + doi})
		}
		json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	return server, &mostAtOnce
}

func dois(n int) []string {
	list := make([]string, n)
	for i := range list {
		list[i] = fmt.Sprintf("10.1/%03d", i)
	}
	return list
}

func TestOpenAlexFetchesItsBatchesAtOnceAndKeepsTheirOrder(t *testing.T) {
	server, mostAtOnce := openAlexServingBatchesSlowly(t, "")
	SetRateLimit("openalex", 1000, 10)
	openAlex := NewOpenAlex(NewClient(), "")
	openAlex.baseURL = server.URL

	asked := dois(3*openAlexFilterBatchSize + 7) // four batches
	papers, err := openAlex.FetchWorksByDOIs(context.Background(), asked)
	if err != nil {
		t.Fatal(err)
	}
	if len(papers) != len(asked) {
		t.Fatalf("got %d works, want %d", len(papers), len(asked))
	}
	for i, p := range papers {
		if p.Title != asked[i] {
			t.Fatalf("work %d is %q, want %q: the batches came back out of order", i, p.Title, asked[i])
		}
	}
	if mostAtOnce.Load() < 2 {
		t.Errorf("the batches never overlapped (at most %d at once)", mostAtOnce.Load())
	}
}

// As the one-at-a-time loop did: one batch failing fails the whole,
// rather than returning a list with a silent hole in it.
func TestOpenAlexFailsWhenAnyBatchFails(t *testing.T) {
	asked := dois(2*openAlexFilterBatchSize + 1)
	server, _ := openAlexServingBatchesSlowly(t, asked[openAlexFilterBatchSize]) // the second batch
	SetRateLimit("openalex", 1000, 10)
	openAlex := NewOpenAlex(NewClient(), "")
	openAlex.baseURL = server.URL

	if papers, err := openAlex.FetchWorksByDOIs(context.Background(), asked); err == nil {
		t.Errorf("got %d works and no error, want the failed batch's error", len(papers))
	}
}
