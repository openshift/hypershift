package releaseinfo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestCincinnatiVersionResolver_WhenValidResponse_ItShouldReturnReleaseImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("channel") != "stable-4.20" {
			t.Errorf("unexpected channel query: %s", r.URL.Query().Get("channel"))
		}
		if r.URL.Query().Get("arch") != "multi" {
			t.Errorf("unexpected arch query: %s", r.URL.Query().Get("arch"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"nodes": [{"version": "4.20.0", "payload": "quay.io/openshift-release-dev/ocp-release@sha256:aaa"}, {"version": "4.20.1", "payload": "quay.io/openshift-release-dev/ocp-release@sha256:abc123"}]}`))
	}))
	defer server.Close()

	resolver := &CincinnatiVersionResolver{
		client:  server.Client(),
		baseURL: server.URL,
		cache:   make(map[string]cacheEntry),
	}

	image, err := resolver.Resolve(t.Context(), "4.20.1", "stable-4.20")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "quay.io/openshift-release-dev/ocp-release@sha256:abc123"
	if image != expected {
		t.Errorf("expected %q, got %q", expected, image)
	}
}

func TestCincinnatiVersionResolver_WhenCustomChannel_ItShouldPassChannelToAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("channel") != "candidate-4.20" {
			t.Errorf("expected channel candidate-4.20, got %s", r.URL.Query().Get("channel"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"nodes": [{"version": "4.20.1", "payload": "quay.io/openshift-release-dev/ocp-release@sha256:candidate123"}]}`))
	}))
	defer server.Close()

	resolver := &CincinnatiVersionResolver{
		client:  server.Client(),
		baseURL: server.URL,
		cache:   make(map[string]cacheEntry),
	}

	image, err := resolver.Resolve(t.Context(), "4.20.1", "candidate-4.20")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "quay.io/openshift-release-dev/ocp-release@sha256:candidate123"
	if image != expected {
		t.Errorf("expected %q, got %q", expected, image)
	}
}

func TestCincinnatiVersionResolver_WhenVersionNotInGraph_ItShouldReturnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"nodes": [{"version": "4.99.0", "payload": "quay.io/openshift-release-dev/ocp-release@sha256:aaa"}]}`))
	}))
	defer server.Close()

	resolver := &CincinnatiVersionResolver{
		client:  server.Client(),
		baseURL: server.URL,
		cache:   make(map[string]cacheEntry),
	}

	_, err := resolver.Resolve(t.Context(), "4.99.1", "stable-4.99")
	if err == nil {
		t.Fatal("expected error for version not in graph, got nil")
	}
}

func TestCincinnatiVersionResolver_WhenNon200Status_ItShouldReturnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal server error`))
	}))
	defer server.Close()

	resolver := &CincinnatiVersionResolver{
		client:  server.Client(),
		baseURL: server.URL,
		cache:   make(map[string]cacheEntry),
	}

	_, err := resolver.Resolve(t.Context(), "4.20.1", "stable-4.20")
	if err == nil {
		t.Fatal("expected error for non-200 status, got nil")
	}
}

func TestCincinnatiVersionResolver_WhenCacheIsFresh_ItShouldReturnCachedWithoutHTTPCall(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"nodes": [{"version": "4.20.1", "payload": "quay.io/openshift-release-dev/ocp-release@sha256:abc123"}]}`))
	}))
	defer server.Close()

	resolver := &CincinnatiVersionResolver{
		client:  server.Client(),
		baseURL: server.URL,
		cache:   make(map[string]cacheEntry),
	}

	ctx := t.Context()

	// First call should hit the server
	image1, err := resolver.Resolve(ctx, "4.20.1", "stable-4.20")
	if err != nil {
		t.Fatalf("unexpected error on first call: %v", err)
	}

	// Second call should return cached
	image2, err := resolver.Resolve(ctx, "4.20.1", "stable-4.20")
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}

	if image1 != image2 {
		t.Errorf("cached result differs: %q vs %q", image1, image2)
	}

	if callCount.Load() != 1 {
		t.Errorf("expected 1 HTTP call, got %d", callCount.Load())
	}
}

func TestCincinnatiVersionResolver_WhenCacheIsExpired_ItShouldReQueryAPI(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"nodes": [{"version": "4.20.1", "payload": "quay.io/openshift-release-dev/ocp-release@sha256:abc123"}]}`))
	}))
	defer server.Close()

	resolver := &CincinnatiVersionResolver{
		client:  server.Client(),
		baseURL: server.URL,
		cache:   make(map[string]cacheEntry),
	}

	ctx := context.Background()

	// First call
	_, err := resolver.Resolve(ctx, "4.20.1", "stable-4.20")
	if err != nil {
		t.Fatalf("unexpected error on first call: %v", err)
	}

	// Manually expire the cache
	resolver.mu.Lock()
	resolver.cache["stable-4.20/4.20.1"] = cacheEntry{
		releaseImage: "quay.io/openshift-release-dev/ocp-release@sha256:abc123",
		expiry:       time.Now().Add(-1 * time.Minute),
	}
	resolver.mu.Unlock()

	// Second call should re-query
	_, err = resolver.Resolve(ctx, "4.20.1", "stable-4.20")
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}

	if callCount.Load() != 2 {
		t.Errorf("expected 2 HTTP calls after cache expiry, got %d", callCount.Load())
	}
}
