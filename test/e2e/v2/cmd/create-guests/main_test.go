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

package main

import (
	"net/http"
	"net/url"
	"testing"

	"k8s.io/client-go/transport"
)

func TestResolveProxyFunc_ExplicitProxyPreserved(t *testing.T) {
	explicitURL := &url.URL{Scheme: "http", Host: "explicit-proxy.example.com:8080"}
	explicitProxy := func(*http.Request) (*url.URL, error) {
		return explicitURL, nil
	}

	proxyFunc := resolveProxyFunc(&transport.Config{Proxy: explicitProxy})

	req, err := http.NewRequest(http.MethodGet, "https://api.example.com", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	got, err := proxyFunc(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != explicitURL {
		t.Fatalf("expected explicit proxy to be preserved, got %v", got)
	}
}

func redactedURL(u *url.URL) string {
	if u == nil {
		return "<nil>"
	}
	return u.Redacted()
}

func TestResolveProxyFunc_FallsBackToEnvironment(t *testing.T) {
	proxyFunc := resolveProxyFunc(&transport.Config{})

	req, err := http.NewRequest(http.MethodGet, "https://api.example.com", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	wantURL, wantErr := http.ProxyFromEnvironment(req)
	gotURL, gotErr := proxyFunc(req)
	if gotErr != wantErr {
		t.Fatalf("expected error %v, got %v", wantErr, gotErr)
	}
	if (gotURL == nil) != (wantURL == nil) || (gotURL != nil && gotURL.String() != wantURL.String()) {
		// Redacted() masks userinfo; http.ProxyFromEnvironment can return a
		// credentialed URL sourced from HTTPS_PROXY/HTTP_PROXY.
		t.Fatalf("expected http.ProxyFromEnvironment fallback (%s), got %s", redactedURL(wantURL), redactedURL(gotURL))
	}
}
