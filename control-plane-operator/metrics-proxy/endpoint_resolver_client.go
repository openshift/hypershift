package metricsproxy

import (
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

// Discover queries the endpoint-resolver for pod endpoints for the given component.
// The serviceName parameter is the component name used for endpoint-resolver lookup
// (which resolves pods by the hypershift.openshift.io/control-plane-component label).
func (c *EndpointResolverClient) Discover(ctx context.Context, serviceName string, port int32) ([]ScrapeTarget, error) {
	url := fmt.Sprintf("%s/resolve/%s", c.baseURL, serviceName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s: %w", url, err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query endpoint-resolver for %s: %w", serviceName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("endpoint-resolver returned status %d for %s", resp.StatusCode, serviceName)
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
