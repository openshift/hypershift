package metricsproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	endpointresolver "github.com/openshift/hypershift/control-plane-operator/endpoint-resolver"
)

// EndpointResolverClient discovers scrape targets by querying the endpoint-resolver
// service instead of directly accessing the Kubernetes API.
type EndpointResolverClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewEndpointResolverClient creates a new client for the endpoint-resolver service.
// If the CA file cannot be read (e.g. volume not yet mounted), the client is created
// without CA verification. Requests will fail with TLS errors until the CA is available,
// but the process won't crash — the config reader will reload periodically.
func NewEndpointResolverClient(baseURL, caFile string) (*EndpointResolverClient, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}

	if caPool := loadCertPool(caFile); caPool != nil {
		tlsConfig.RootCAs = caPool
	}

	return &EndpointResolverClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: tlsConfig,
			},
		},
	}, nil
}

// Discover queries the endpoint-resolver for pod endpoints matching the given
// label selector. The endpoint-resolver expects a POST to /resolve with a JSON
// body containing the selector map.
func (c *EndpointResolverClient) Discover(ctx context.Context, selector map[string]string, port int32) ([]ScrapeTarget, error) {
	reqBody := endpointresolver.ResolveRequest{Selector: selector}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal resolve request: %w", err)
	}

	url := fmt.Sprintf("%s/resolve", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query endpoint-resolver: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("endpoint-resolver returned status %d for selector %v", resp.StatusCode, selector)
	}

	var resolveResp endpointresolver.ResolveResponse
	if err := json.NewDecoder(resp.Body).Decode(&resolveResp); err != nil {
		return nil, fmt.Errorf("failed to decode endpoint-resolver response: %w", err)
	}

	targets := make([]ScrapeTarget, 0, len(resolveResp.Pods))
	for _, pod := range resolveResp.Pods {
		targets = append(targets, ScrapeTarget{
			PodName: pod.Name,
			PodIP:   pod.IP,
			Port:    port,
		})
	}

	return targets, nil
}
