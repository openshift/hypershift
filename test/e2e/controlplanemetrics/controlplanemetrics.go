package controlplanemetrics

import (
	"fmt"
	"sync"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	controlPlaneRolloutDurationSeconds *prometheus.GaugeVec
	setupOnce                          sync.Once
	setupErr                           error
)

// SetupWithManager registers the control plane metrics with Prometheus.
// This function is safe to call multiple times; the metric will only be created and registered once.
func SetupWithManager(_ ctrl.Manager) error {
	setupOnce.Do(func() {
		controlPlaneRolloutDurationSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "e2e_control_plane_rollout_duration_seconds",
			Help: "Duration in seconds for control plane components to become ready",
		}, []string{"namespace", "name", "context"})
		if err := prometheus.Register(controlPlaneRolloutDurationSeconds); err != nil {
			setupErr = fmt.Errorf("failed to register e2e_control_plane_rollout_duration_seconds metric: %w", err)
		}
	})
	return setupErr
}

// RecordControlPlaneRolloutDuration records a control plane rollout duration metric.
// If the metric has not been set up via SetupWithManager, this function is a no-op.
func RecordControlPlaneRolloutDuration(namespace, name, context string, durationSeconds float64) {
	if controlPlaneRolloutDurationSeconds == nil {
		return
	}
	controlPlaneRolloutDurationSeconds.WithLabelValues(namespace, name, context).Set(durationSeconds)
}
