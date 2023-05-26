package metrics

import (
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	labelNames = []string{"namespace", "name", "cluster_name", "platform"}

	NodePoolSizeMetricName = "hypershift_nodepools_size"
	nodePoolSize           = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Number of desired replicas associated with a given NodePool",
		Name: NodePoolSizeMetricName,
	}, labelNames)

	NodePoolAvailableReplicasMetricName = "hypershift_nodepools_available_replicas"
	nodePoolAvailableReplicas           = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Number of available replicas associated with a given NodePool",
		Name: NodePoolAvailableReplicasMetricName,
	}, labelNames)

	NodePoolDeletionDurationMetricName = "hypershift_nodepools_deletion_duration_seconds"
	nodePoolDeletionDuration           = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Time in seconds it took for NodePool to be deleted",
		Name: NodePoolDeletionDurationMetricName,
	}, labelNames)

	NodePoolInitialRolloutDurationMetricName = "hypershift_nodepools_initial_rollout_duration_seconds"
	nodePoolInitialRolloutDuration           = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Time in seconds it took from initial NodePool creation and rollout of initial version",
		Name: NodePoolInitialRolloutDurationMetricName,
	}, labelNames)

	NodePoolUpgradeDurationMetricName = "hypershift_nodepools_upgrade_duration_seconds"
	nodePoolUpgradeDuration           = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Help: "Time in seconds it took a NodePool to upgrade and rollout a given version",
		Name: NodePoolUpgradeDurationMetricName,
	}, append(labelNames, "previous_version", "new_version"))
)

func init() {
	metrics.Registry.MustRegister(
		nodePoolSize,
		nodePoolAvailableReplicas,
		nodePoolDeletionDuration,
		nodePoolInitialRolloutDuration,
		nodePoolUpgradeDuration,
	)
}

func labels(nodePool *hyperv1.NodePool) prometheus.Labels {
	return prometheus.Labels{
		"namespace":    nodePool.Namespace,
		"name":         nodePool.Name,
		"cluster_name": nodePool.Spec.ClusterName,
		"platform":     string(nodePool.Spec.Platform.Type),
	}
}

func RecordNodePoolSize(nodePool *hyperv1.NodePool, size float64) {
	nodePoolSize.With(labels(nodePool)).Set(size)
}

func RecordNodePoolAvailableReplicas(nodePool *hyperv1.NodePool) {
	nodePoolAvailableReplicas.With(labels(nodePool)).Set(float64(nodePool.Status.Replicas))
}

func RecordNodePoolDeletionDuration(nodePool *hyperv1.NodePool) {
	duration := time.Since(nodePool.DeletionTimestamp.Time).Seconds()
	nodePoolDeletionDuration.With(labels(nodePool)).Set(duration)
}

func RecordNodePoolInitialRolloutDuration(nodePool *hyperv1.NodePool) {
	duration := time.Since(nodePool.CreationTimestamp.Time).Seconds()
	nodePoolInitialRolloutDuration.With(labels(nodePool)).Set(duration)
}

func RecordNodePoolUpgradeDuration(nodePool *hyperv1.NodePool, prevVersion, newVersion string, duration time.Duration) {
	mlabels := labels(nodePool)
	mlabels["previous_version"] = prevVersion
	mlabels["new_version"] = newVersion

	nodePoolUpgradeDuration.With(mlabels).Set(duration.Seconds())
}
