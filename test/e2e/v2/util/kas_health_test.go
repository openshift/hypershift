//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckKASHealth_StatusOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	healthy, detail := checkKASHealth(context.Background(), server.Client(), server.URL+"/healthz")
	if !healthy {
		t.Fatalf("expected healthy, got unhealthy: %s", detail)
	}
	if detail != "status 200" {
		t.Fatalf("expected detail %q, got %q", "status 200", detail)
	}
}

func TestCheckKASHealth_NonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	healthy, detail := checkKASHealth(context.Background(), server.Client(), server.URL+"/healthz")
	if healthy {
		t.Fatalf("expected unhealthy, got healthy: %s", detail)
	}
	if detail != "status 503" {
		t.Fatalf("expected detail %q, got %q", "status 503", detail)
	}
}

func TestCheckKASHealth_RequestFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	unreachableURL := server.URL + "/healthz"
	server.Close() // closing immediately makes the address unreachable

	healthy, detail := checkKASHealth(context.Background(), http.DefaultClient, unreachableURL)
	if healthy {
		t.Fatalf("expected unhealthy, got healthy: %s", detail)
	}
	if !strings.HasPrefix(detail, "request failed:") {
		t.Fatalf("expected detail to start with %q, got %q", "request failed:", detail)
	}
}

func TestCheckKASHealth_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	healthy, detail := checkKASHealth(ctx, server.Client(), server.URL+"/healthz")
	if healthy {
		t.Fatalf("expected unhealthy, got healthy: %s", detail)
	}
	if !strings.HasPrefix(detail, "request failed:") {
		t.Fatalf("expected detail to start with %q, got %q", "request failed:", detail)
	}
}

func TestCheckKASHealth_RequestCreationFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	healthy, detail := checkKASHealth(ctx, http.DefaultClient, "://invalid-url")
	if healthy {
		t.Fatalf("expected unhealthy, got healthy: %s", detail)
	}
	if !strings.HasPrefix(detail, "failed to create request:") {
		t.Fatalf("expected detail to start with %q, got %q", "failed to create request:", detail)
	}
}
