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
	"net/http"
	"net/url"
	"testing"

	"k8s.io/client-go/transport"
)

func TestNewKASTransport(t *testing.T) {
	t.Run("When an explicit proxy is configured, it should preserve it", func(t *testing.T) {
		expectedURL := &url.URL{Scheme: "http", Host: "explicit-proxy.example.com:8080"}
		proxy := func(*http.Request) (*url.URL, error) {
			return expectedURL, nil
		}

		configuredTransport, err := NewKASTransport(&transport.Config{Proxy: proxy})
		if err != nil {
			t.Fatalf("failed to create transport: %v", err)
		}

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.example.com", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		gotURL, err := configuredTransport.Proxy(req)
		if err != nil {
			t.Fatalf("proxy returned an error: %v", err)
		}
		if gotURL != expectedURL {
			t.Fatalf("expected explicit proxy %q, got %q", redactURL(expectedURL), redactURL(gotURL))
		}
	})

	t.Run("When no proxy is configured, it should use the environment proxy", func(t *testing.T) {
		t.Setenv("HTTP_PROXY", "http://environment-proxy.example.com:8080")
		t.Setenv("http_proxy", "http://environment-proxy.example.com:8080")
		t.Setenv("HTTPS_PROXY", "http://environment-proxy.example.com:8080")
		t.Setenv("https_proxy", "http://environment-proxy.example.com:8080")
		t.Setenv("ALL_PROXY", "http://environment-proxy.example.com:8080")
		t.Setenv("all_proxy", "http://environment-proxy.example.com:8080")
		t.Setenv("NO_PROXY", "")
		t.Setenv("no_proxy", "")

		configuredTransport, err := NewKASTransport(&transport.Config{})
		if err != nil {
			t.Fatalf("failed to create transport: %v", err)
		}

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://api.example.com", nil)
		if err != nil {
			t.Fatalf("failed to create request: %v", err)
		}
		gotURL, err := configuredTransport.Proxy(req)
		if err != nil {
			t.Fatalf("proxy returned an error: %v", err)
		}
		wantURL, err := http.ProxyFromEnvironment(req)
		if err != nil {
			t.Fatalf("environment proxy returned an error: %v", err)
		}
		if (gotURL == nil) != (wantURL == nil) || (gotURL != nil && gotURL.String() != wantURL.String()) {
			t.Fatalf("expected environment proxy %q, got %q", redactURL(wantURL), redactURL(gotURL))
		}
	})
}

func redactURL(u *url.URL) string {
	if u == nil {
		return "<nil>"
	}
	return u.Redacted()
}
