package hostedcluster

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	HostedClusterDeletionDurationMetricName = "hypershift_cluster_deletion_duration_seconds"
	hostedClusterDeletionDuration           = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Time in seconds it took from HostedCluster having a deletion timestamp to all hypershift finalizers being removed",
		Name: HostedClusterDeletionDurationMetricName,
	}, []string{"name"})

	HostedClusterGuestCloudResourcesDeletionDurationMetricName = "hypershift_cluster_guest_cloud_resources_deletion_duration_seconds"
	hostedClusterGuestCloudResourcesDeletionDuration           = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Time in seconds it took from HostedCluster having a deletion timestamp to the CloudResourcesDestroyed being true",
		Name: HostedClusterGuestCloudResourcesDeletionDurationMetricName,
	}, []string{"name"})
)

func init() {
	metrics.Registry.MustRegister(
		hostedClusterDeletionDuration,
		hostedClusterGuestCloudResourcesDeletionDuration,
	)
}
