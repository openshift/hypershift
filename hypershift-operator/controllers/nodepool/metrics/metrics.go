package metrics

import (
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
)

func init() {
	metrics.Registry.MustRegister(
		nodePoolSize,
		nodePoolAvailableReplicas,
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
