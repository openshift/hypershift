package metricsproxy

import (
	"context"
	"crypto/tls"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// ComponentProvider provides component configurations for metrics scraping.
type ComponentProvider interface {
	GetComponent(name string) (ComponentConfig, bool)
	GetComponentNames() []string
}

// ComponentConfig describes how to scrape a single control plane component,
// including the per-component TLS configuration derived from its ServiceMonitor.
type ComponentConfig struct {
	Selector      map[string]string
	MetricsPort   int32
	MetricsPath   string
	MetricsScheme string
	TLSServerName string
	TLSConfig     *tls.Config

	// Label overrides from SM/PM annotations for standalone OCP compatibility.
	MetricsJob       string
	MetricsNamespace string
	MetricsService   string
	MetricsEndpoint  string

	// sampleFilter holds the compiled metric relabel rules. A nil filter
	// forwards every sample unchanged.
	sampleFilter *sampleFilter
}

// TargetDiscoverer discovers scrape targets (pod endpoints) for a given service.
type TargetDiscoverer interface {
	Discover(ctx context.Context, selector map[string]string, port int32) ([]ScrapeTarget, error)
}

// ScrapeTarget represents a single pod endpoint to scrape.
type ScrapeTarget struct {
	PodName string
	PodIP   string
	Port    int32
}

// MetricsFilterMode selects how the metrics-proxy filters the samples of a component.
type MetricsFilterMode string

const (
	// MetricsFilterNone forwards every sample unchanged. It is used for the
	// All metrics set and for PodMonitor-based components.
	MetricsFilterNone MetricsFilterMode = "None"
	// MetricsFilterRelabel applies the component's keep and drop
	// MetricRelabelConfigs to every exposed sample. An empty rule list
	// forwards no samples.
	MetricsFilterRelabel MetricsFilterMode = "Relabel"
)

// FileConfig is the YAML structure shared between the CPO (writer) and the
// metrics-proxy binary (reader) for the scrape-config ConfigMap.
type FileConfig struct {
	EndpointResolver EndpointResolverFileConfig `json:"endpointResolver"`
	Components       []ComponentFileConfig      `json:"components"`
}

// EndpointResolverFileConfig holds the endpoint-resolver connection details.
type EndpointResolverFileConfig struct {
	URL    string `json:"url"`
	CAFile string `json:"caFile"`
}

// ComponentFileConfig holds the per-component scrape configuration.
type ComponentFileConfig struct {
	// Name is the unique identifier for this component (e.g. the ServiceMonitor
	// or PodMonitor name). Components are sorted by this field for deterministic
	// serialization.
	Name string `json:"name"`
	// Selector is the pod label selector used by the endpoint-resolver to find
	// matching pods. For ServiceMonitor components this comes from the Service's
	// Spec.Selector; for PodMonitor components from the PodMonitor's
	// Spec.Selector.MatchLabels.
	Selector      map[string]string `json:"selector,omitempty"`
	MetricsPort   int32             `json:"metricsPort"`
	MetricsPath   string            `json:"metricsPath"`
	MetricsScheme string            `json:"metricsScheme"`
	TLSServerName string            `json:"tlsServerName"`
	CAFile        string            `json:"caFile,omitempty"`
	CertFile      string            `json:"certFile,omitempty"`
	KeyFile       string            `json:"keyFile,omitempty"`

	// Label overrides from SM/PM annotations for standalone OCP compatibility.
	MetricsJob       string `json:"metricsJob,omitempty"`
	MetricsNamespace string `json:"metricsNamespace,omitempty"`
	MetricsService   string `json:"metricsService,omitempty"`
	MetricsEndpoint  string `json:"metricsEndpoint,omitempty"`

	// MetricsFilter selects how samples are filtered. An empty value is
	// treated as MetricsFilterNone.
	MetricsFilter MetricsFilterMode `json:"metricsFilter,omitempty"`
	// MetricRelabelConfigs are the rules of the effective forwarding metrics
	// set, evaluated in order against every exposed sample when MetricsFilter
	// is MetricsFilterRelabel. Only the keep and drop actions are applied;
	// other actions are ignored.
	MetricRelabelConfigs []prometheusoperatorv1.RelabelConfig `json:"metricRelabelConfigs,omitempty"`
}
