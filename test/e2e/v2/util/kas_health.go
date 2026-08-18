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
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
)

const (
	kasHealthPollInterval = 10 * time.Second
	kasHealthTimeout      = 5 * time.Second
)

// StartKASHealthLogger starts a background goroutine that periodically
// polls the management KAS /healthz endpoint and logs state transitions
// (healthy to unhealthy and back). This provides visibility into
// transient KAS connectivity issues during e2e test execution.
//
// The goroutine exits when ctx is cancelled. The logger parameter is
// used for all output; pass a *log.Logger configured with an
// appropriate prefix. If logger is nil, the default log package is used.
func StartKASHealthLogger(ctx context.Context, restConfig *rest.Config, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}

	httpClient, healthzURL, err := buildKASHealthClient(restConfig)
	if err != nil {
		logger.Printf("kas-health: failed to build health check client: %v", err)
		return
	}

	go func() {
		healthy := true // assume healthy at start
		logger.Printf("kas-health: starting health monitor for %s", healthzURL)

		ticker := time.NewTicker(kasHealthPollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.Printf("kas-health: monitor stopped")
				return
			case <-ticker.C:
				nowHealthy, detail := checkKASHealth(ctx, httpClient, healthzURL)
				if nowHealthy && !healthy {
					logger.Printf("kas-health: KAS recovered (healthy): %s", detail)
				} else if !nowHealthy && healthy {
					logger.Printf("kas-health: KAS unreachable (unhealthy): %s", detail)
				}
				healthy = nowHealthy
			}
		}
	}()
}

// buildKASHealthClient creates an HTTP client configured with the
// management cluster's TLS credentials for polling /healthz.
func buildKASHealthClient(restConfig *rest.Config) (*http.Client, string, error) {
	transportConfig, err := restConfig.TransportConfig()
	if err != nil {
		return nil, "", fmt.Errorf("building transport config: %w", err)
	}

	tlsConfig, err := transport.TLSConfigFor(transportConfig)
	if err != nil {
		return nil, "", fmt.Errorf("building TLS config: %w", err)
	}

	httpTransport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	// Apply auth wrappers (bearer token, exec credential plugin, etc.) so
	// the health check hits the same auth path as the real management client.
	wrappedTransport, err := transport.HTTPWrappersForConfig(transportConfig, httpTransport)
	if err != nil {
		return nil, "", fmt.Errorf("wrapping transport with auth: %w", err)
	}

	client := &http.Client{
		Transport: wrappedTransport,
		Timeout:   kasHealthTimeout,
	}

	healthzURL := strings.TrimSuffix(restConfig.Host, "/") + "/healthz"
	return client, healthzURL, nil
}

// checkKASHealth performs a single health check against the KAS /healthz
// endpoint. Returns whether the server is healthy and a detail string.
func checkKASHealth(ctx context.Context, client *http.Client, healthzURL string) (bool, string) {
	reqCtx, cancel := context.WithTimeout(ctx, kasHealthTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, healthzURL, nil)
	if err != nil {
		return false, fmt.Sprintf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return true, "status 200"
	}
	return false, fmt.Sprintf("status %d", resp.StatusCode)
}
