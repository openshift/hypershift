package metrics

import (
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
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

	NodePoolTransitionSecondsMetricName = "hypershift_nodepools_transition_seconds"
	nodePoolTransitionSeconds           = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    NodePoolTransitionSecondsMetricName,
		Help:    "Time in seconds it took from NodePool creation until a given condition transitions to true",
		Buckets: []float64{5, 10, 20, 30, 60, 90, 120, 180, 240, 300, 360, 480, 600},
	}, []string{"condition"})
)

func init() {
	metrics.Registry.MustRegister(
		nodePoolSize,
		nodePoolAvailableReplicas,
		nodePoolDeletionDuration,
		nodePoolInitialRolloutDuration,
		nodePoolTransitionSeconds,
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

func ObserveConditionTransitionDuration(nodePool *hyperv1.NodePool, newCondition, oldCondition *hyperv1.NodePoolCondition) {
	if (oldCondition != nil && oldCondition.Status == newCondition.Status) || newCondition.Status != corev1.ConditionTrue {
		return
	}

	duration := newCondition.LastTransitionTime.Sub(nodePool.CreationTimestamp.Time).Seconds()
	nodePoolTransitionSeconds.With(prometheus.Labels{"condition": newCondition.Type}).Observe(duration)
}
