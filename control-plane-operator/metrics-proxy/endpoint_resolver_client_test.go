package metricsproxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	endpointresolver "github.com/openshift/hypershift/control-plane-operator/endpoint-resolver"
)

func TestEndpointResolverClientDiscover(t *testing.T) {
	t.Parallel()

	t.Run("When endpoint-resolver returns pods, it should convert them to ScrapeTargets", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/resolve/kube-apiserver" {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			resp := endpointresolver.ResolveResponse{
				Pods: []endpointresolver.PodEndpoint{
					{Name: "kube-apiserver-0", IP: "10.0.1.5"},
					{Name: "kube-apiserver-1", IP: "10.0.1.6"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		client := &EndpointResolverClient{
			baseURL:    server.URL,
			httpClient: server.Client(),
		}

		targets, err := client.Discover(context.Background(), "kube-apiserver", 6443)
		if err != nil {
			t.Fatalf("Discover() returned error: %v", err)
		}

		if len(targets) != 2 {
			t.Fatalf("expected 2 targets, got %d", len(targets))
		}

		if targets[0].PodName != "kube-apiserver-0" {
			t.Errorf("expected pod name kube-apiserver-0, got %s", targets[0].PodName)
		}
		if targets[0].PodIP != "10.0.1.5" {
			t.Errorf("expected pod IP 10.0.1.5, got %s", targets[0].PodIP)
		}
		if targets[0].Port != 6443 {
			t.Errorf("expected port 6443, got %d", targets[0].Port)
		}
	})

	t.Run("When endpoint-resolver returns 404, it should return nil targets", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "no endpoints found", http.StatusNotFound)
		}))
		defer server.Close()

		client := &EndpointResolverClient{
			baseURL:    server.URL,
			httpClient: server.Client(),
		}

		targets, err := client.Discover(context.Background(), "nonexistent", 8443)
		if err != nil {
			t.Fatalf("Discover() returned error: %v", err)
		}
		if targets != nil {
			t.Errorf("expected nil targets for 404, got %v", targets)
		}
	})

	t.Run("When endpoint-resolver returns server error, it should return an error", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}))
		defer server.Close()

		client := &EndpointResolverClient{
			baseURL:    server.URL,
			httpClient: server.Client(),
		}

		_, err := client.Discover(context.Background(), "kube-apiserver", 6443)
		if err == nil {
			t.Fatal("expected error for 500 response, got nil")
		}
	})
}
