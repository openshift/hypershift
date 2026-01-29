package controlplanemetrics

import (
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	controlPlaneRolloutDurationSeconds *prometheus.GaugeVec
)

func init() {
	controlPlaneRolloutDurationSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "e2e_control_plane_rollout_duration_seconds",
		Help: "Duration in seconds for control plane components to become ready",
	}, []string{"namespace", "name", "context"})
}

// SetupWithManager registers the control plane metrics with Prometheus
func SetupWithManager(mgr ctrl.Manager) error {
	return prometheus.Register(controlPlaneRolloutDurationSeconds)
}

// RecordControlPlaneRolloutDuration records a control plane rollout duration metric
func RecordControlPlaneRolloutDuration(namespace, name, context string, durationSeconds float64) {
	controlPlaneRolloutDurationSeconds.WithLabelValues(namespace, name, context).Set(durationSeconds)
}
