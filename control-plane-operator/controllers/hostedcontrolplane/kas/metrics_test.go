package kas

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNewKASHealthMetrics(t *testing.T) {
	t.Run("When creating KAS health metrics, it should register both metrics", func(t *testing.T) {
		registry := prometheus.NewRegistry()

		m := &KASHealthMetrics{
			Available: prometheus.NewGauge(prometheus.GaugeOpts{
				Name: KASAvailableMetricName,
				Help: "1 if the KAS /healthz endpoint returns HTTP 200, 0 otherwise.",
			}),
			RequestDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
				Name:    KASRequestDurationMetricName,
				Help:    "Latency of the KAS /healthz health check probe in seconds.",
				Buckets: KASRequestDurationBuckets,
			}),
		}

		registry.MustRegister(m.Available, m.RequestDuration)

		// The gauge should start at 0
		if val := testutil.ToFloat64(m.Available); val != 0 {
			t.Errorf("expected initial gauge value 0, got %f", val)
		}

		// Setting available to 1 should work
		m.Available.Set(1)
		if val := testutil.ToFloat64(m.Available); val != 1 {
			t.Errorf("expected gauge value 1, got %f", val)
		}

		// Observing a duration should be counted
		m.RequestDuration.Observe(0.5)
		count := testutil.CollectAndCount(m.RequestDuration)
		if count != 1 {
			t.Errorf("expected 1 metric series from histogram, got %d", count)
		}
	})
}
