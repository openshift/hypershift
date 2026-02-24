package metricsproxy

import (
	"context"
	"crypto/tls"
)

// ComponentProvider provides component configurations for metrics scraping.
type ComponentProvider interface {
	GetComponent(name string) (ComponentConfig, bool)
	GetComponentNames() []string
}

// ComponentConfig describes how to scrape a single control plane component,
// including the per-component TLS configuration derived from its ServiceMonitor.
type ComponentConfig struct {
	ServiceName   string
	MetricsPort   int32
	MetricsPath   string
	MetricsScheme string
	TLSServerName string
	TLSConfig     *tls.Config
}

// TargetDiscoverer discovers scrape targets (pod endpoints) for a given service.
type TargetDiscoverer interface {
	Discover(ctx context.Context, serviceName string, port int32) ([]ScrapeTarget, error)
}

// ScrapeTarget represents a single pod endpoint to scrape.
type ScrapeTarget struct {
	PodName string
	PodIP   string
	Port    int32
}

// FileConfig is the YAML structure shared between the CPO (writer) and the
// metrics-proxy binary (reader) for the scrape-config ConfigMap.
type FileConfig struct {
	EndpointResolver EndpointResolverFileConfig     `json:"endpointResolver"`
	Components       map[string]ComponentFileConfig `json:"components"`
}

// EndpointResolverFileConfig holds the endpoint-resolver connection details.
type EndpointResolverFileConfig struct {
	URL    string `json:"url"`
	CAFile string `json:"caFile"`
}

// ComponentFileConfig holds the per-component scrape configuration.
type ComponentFileConfig struct {
	// ServiceName is the component name used for endpoint-resolver lookup.
	// For ServiceMonitor components this matches the service name by convention.
	// For PodMonitor components this is the PodMonitor/component name.
	ServiceName   string `json:"serviceName"`
	MetricsPort   int32  `json:"metricsPort"`
	MetricsPath   string `json:"metricsPath"`
	MetricsScheme string `json:"metricsScheme"`
	TLSServerName string `json:"tlsServerName"`
	CAFile        string `json:"caFile,omitempty"`
	CertFile      string `json:"certFile,omitempty"`
	KeyFile       string `json:"keyFile,omitempty"`
}
