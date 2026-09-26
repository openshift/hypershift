package releaseinfo

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	mirrorResultHit         = "mirror_hit"
	mirrorResultUnavailable = "mirror_unavailable"
	mirrorResultError       = "mirror_error"
	mirrorResultFallback    = "fallback"
)

var (
	lookupDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "hypershift_release_image_lookup_duration_seconds",
		Help: "Wall-clock time in seconds for a release image mirror resolution, including lock wait and registry I/O. " +
			"Subtract hypershift_release_image_lookup_lock_wait_seconds to isolate registry I/O time.",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
	})

	lockWaitDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: "hypershift_release_image_lookup_lock_wait_seconds",
		Help: "Time in seconds spent blocked waiting to acquire the mirror resolution mutex. " +
			"High values indicate callers are serialized behind a slow lookup.",
		Buckets: []float64{0.001, 0.01, 0.1, 0.5, 1, 5, 10},
	})

	mirrorLookupTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "hypershift_release_image_mirror_lookup_total",
		Help: "Total release image mirror resolution attempts by outcome: " +
			"mirror_hit (mirror found and verified), mirror_unavailable (mirror found but repo unreachable), " +
			"mirror_error (delegate lookup failed), fallback (no mirror matched, used original image).",
	}, []string{"result"})
)

func init() {
	metrics.Registry.MustRegister(
		lookupDuration,
		lockWaitDuration,
		mirrorLookupTotal,
	)
}
