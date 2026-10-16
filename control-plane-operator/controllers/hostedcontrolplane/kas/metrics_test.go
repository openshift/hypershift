package kas

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestKASHealthMetricsOperations(t *testing.T) {
	t.Parallel()

	t.Run("When creating KAS health metrics, it should register both metrics", func(t *testing.T) {
		g := NewGomegaWithT(t)
		registry := prometheus.NewRegistry()
		m := NewKASHealthMetrics(registry)

		g.Expect(testutil.ToFloat64(m.Available)).To(Equal(0.0))

		mfs, err := registry.Gather()
		g.Expect(err).NotTo(HaveOccurred())
		names := make([]string, 0, len(mfs))
		for _, mf := range mfs {
			names = append(names, mf.GetName())
		}
		g.Expect(names).To(ConsistOf(KASAvailableMetricName, KASRequestDurationMetricName))
	})

	t.Run("When setting availability to 1, it should reflect the new value", func(t *testing.T) {
		g := NewGomegaWithT(t)
		registry := prometheus.NewRegistry()
		m := NewKASHealthMetrics(registry)

		m.Available.Set(1)
		g.Expect(testutil.ToFloat64(m.Available)).To(Equal(1.0))
	})

	t.Run("When observing a request duration, it should be counted", func(t *testing.T) {
		g := NewGomegaWithT(t)
		registry := prometheus.NewRegistry()
		m := NewKASHealthMetrics(registry)

		m.RequestDuration.Observe(0.5)
		g.Expect(testutil.CollectAndCount(m.RequestDuration)).To(Equal(1))
	})
}
