package hostedcluster

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	hostedClusterDeletionDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Time in seconds it took from HostedCluster having a deletion timestamp to all hypershift finalizers being removed",
		Name: "hypershift_cluster_deletion_duration_seconds",
	}, []string{"name"})
	hostedClusterGuestCloudResourcesDeletionDuration = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Time in seconds it took from HostedCluster having a deletion timestamp to the CloudResourcesDestroyed being true",
		Name: "hypershift_cluster_guest_cloud_resources_deletion_duration_seconds",
	}, []string{"name"})
)

func init() {
	metrics.Registry.MustRegister(
		hostedClusterDeletionDuration,
		hostedClusterGuestCloudResourcesDeletionDuration,
	)
}
