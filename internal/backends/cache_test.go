package backends

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestResponseCache_ServesRepeatsWithoutRequests(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"n":` + string(rune('0'+hits.Load())) + `}`))
	}))
	defer server.Close()

	EnableResponseCache(time.Minute)
	defer DisableResponseCache()

	client := NewClient()
	first, err := client.Do(context.Background(), Request{URL: server.URL, Backend: "test-cache"})
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	second, err := client.Do(context.Background(), Request{URL: server.URL, Backend: "test-cache"})
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("expected one upstream hit, got %d", hits.Load())
	}
	if string(first) != string(second) {
		t.Errorf("cached body differs: %s vs %s", first, second)
	}

	// A different header set is a different answer (API key tiers).
	client.Do(context.Background(), Request{URL: server.URL, Backend: "test-cache", Headers: map[string]string{"Authorization": "Bearer k"}})
	if hits.Load() != 2 {
		t.Errorf("expected a keyed request to bypass the unkeyed entry, got %d hits", hits.Load())
	}

	// POSTs are never cached.
	client.Do(context.Background(), Request{URL: server.URL, Backend: "test-cache", Method: http.MethodPost, Body: []byte(`{}`)})
	client.Do(context.Background(), Request{URL: server.URL, Backend: "test-cache", Method: http.MethodPost, Body: []byte(`{}`)})
	if hits.Load() != 4 {
		t.Errorf("expected POSTs to reach upstream every time, got %d hits", hits.Load())
	}
}

func TestResponseCache_ErrorsAreNotCached(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	EnableResponseCache(time.Minute)
	defer DisableResponseCache()

	client := NewClient()
	client.Do(context.Background(), Request{URL: server.URL, Backend: "test-cache-err"})
	client.Do(context.Background(), Request{URL: server.URL, Backend: "test-cache-err"})
	if hits.Load() != 2 {
		t.Errorf("expected errors to be re-requested, got %d hits", hits.Load())
	}
}

func TestResponseCache_Expires(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	EnableResponseCache(20 * time.Millisecond)
	defer DisableResponseCache()

	client := NewClient()
	client.Do(context.Background(), Request{URL: server.URL, Backend: "test-cache-ttl"})
	time.Sleep(40 * time.Millisecond)
	client.Do(context.Background(), Request{URL: server.URL, Backend: "test-cache-ttl"})
	if hits.Load() != 2 {
		t.Errorf("expected expiry to force a fresh request, got %d hits", hits.Load())
	}
}

func TestResponseCache_EvictsWhenFull(t *testing.T) {
	EnableResponseCache(time.Minute)
	defer DisableResponseCache()
	cache.maxEntries = 3

	for _, key := range []string{"a", "b", "c", "d"} {
		cache.put(key, []byte(key))
	}
	if len(cache.entries) > 3 {
		t.Errorf("expected at most 3 entries after eviction, got %d", len(cache.entries))
	}
	if _, ok := cache.get("d"); !ok {
		t.Error("newest entry should survive eviction")
	}
}
