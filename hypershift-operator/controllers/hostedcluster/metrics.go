package hostedcluster

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	clusterDeletionTime = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Time in seconds it took from initial cluster deletion to the resource all hypershift finalizers being removed",
		Name: "hypershift_cluster_deletion_duration_seconds",
	}, []string{"name"})
)

func init() {
	metrics.Registry.MustRegister(
		clusterDeletionTime,
	)
}
