package kas

import (
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	KASAvailableMetricName       = "hypershift_kube_apiserver_available"
	KASRequestDurationMetricName = "hypershift_kube_apiserver_request_duration_seconds"
)

// KASHealthMetrics holds Prometheus metrics for KAS health check probes.
// Each CPO pod runs in its own HCP namespace, so metrics are naturally
// scoped per hosted cluster without needing additional labels.
type KASHealthMetrics struct {
	Available       prometheus.Gauge
	RequestDuration prometheus.Histogram
}

// NewKASHealthMetrics creates and registers KAS health metrics with the
// controller-runtime metrics registry. The existing PodMonitor for the
// control-plane-operator will automatically scrape these metrics.
func NewKASHealthMetrics() *KASHealthMetrics {
	m := &KASHealthMetrics{
		Available: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: KASAvailableMetricName,
			Help: "1 if the KAS /healthz endpoint returns HTTP 200, 0 otherwise.",
		}),
		RequestDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    KASRequestDurationMetricName,
			Help:    "Latency of the KAS /healthz health check probe in seconds.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}),
	}

	crmetrics.Registry.MustRegister(m.Available, m.RequestDuration)
	return m
}
