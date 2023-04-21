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
	hostedClusterCreated = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Timestamp of the Hosted Cluster creation time. This metric allows to identify Hosted Clusters that have not gone available in a given time frame and count them as failures",
		Name: "hypershift_cluster_created_timestamp_seconds",
	}, []string{"name", "desiredImage", "completedImage", "available"})
)

func init() {
	metrics.Registry.MustRegister(
		hostedClusterDeletionDuration,
		hostedClusterGuestCloudResourcesDeletionDuration,
	)
}
