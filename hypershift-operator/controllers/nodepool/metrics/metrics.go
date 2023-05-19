package metrics

import (
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	labelNames = []string{"namespace", "name", "platform"}

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
)

func init() {
	metrics.Registry.MustRegister(
		nodePoolSize,
		nodePoolAvailableReplicas,
		nodePoolDeletionDuration,
		nodePoolInitialRolloutDuration,
	)
}

func labelValues(nodePool *hyperv1.NodePool) []string {
	return []string{
		nodePool.Namespace,
		nodePool.Name,
		string(nodePool.Spec.Platform.Type),
	}
}

func RecordNodePoolSize(nodePool *hyperv1.NodePool, size float64) {
	nodePoolSize.WithLabelValues(labelValues(nodePool)...).Set(size)
}

func RecordNodePoolAvailableReplicas(nodePool *hyperv1.NodePool) {
	nodePoolAvailableReplicas.WithLabelValues(labelValues(nodePool)...).Set(float64(nodePool.Status.Replicas))
}

func RecordNodePoolDeletionDuration(nodePool *hyperv1.NodePool) {
	duration := time.Since(nodePool.DeletionTimestamp.Time).Seconds()
	nodePoolDeletionDuration.WithLabelValues(labelValues(nodePool)...).Set(duration)
}

func RecordNodePoolInitialRolloutDuration(nodePool *hyperv1.NodePool) {
	duration := time.Since(nodePool.CreationTimestamp.Time).Seconds()
	nodePoolInitialRolloutDuration.WithLabelValues(labelValues(nodePool)...).Set(duration)
}
