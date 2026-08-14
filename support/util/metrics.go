package util

import (
	"k8s.io/utils/ptr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// clusterIDLabel (_id) is the common label used to identify clusters in telemeter.
// For hypershift, it will identify metrics produced by the both the control plane
// components and guest cluster monitoring stack.
// ref: https://github.com/openshift/enhancements/pull/981
const clusterIDLabel = "_id"

func ApplyClusterIDLabel(ep *prometheusoperatorv1.Endpoint, clusterID string) {
	ep.RelabelConfigs = append(ep.RelabelConfigs, clusterIDRelabelConfig(clusterID))
	ep.MetricRelabelConfigs = append(ep.MetricRelabelConfigs, clusterIDRelabelConfig(clusterID))
}

func ApplyClusterIDLabelToPodMonitor(ep *prometheusoperatorv1.PodMetricsEndpoint, clusterID string) {
	ep.RelabelConfigs = append(ep.RelabelConfigs, clusterIDRelabelConfig(clusterID))
	ep.MetricRelabelConfigs = append(ep.MetricRelabelConfigs, clusterIDRelabelConfig(clusterID))
}

func ApplyClusterIDLabelToRecordingRule(rule *prometheusoperatorv1.Rule, clusterID string) {
	if rule.Labels == nil {
		rule.Labels = map[string]string{}
	}
	rule.Labels[clusterIDLabel] = clusterID
}

func clusterIDRelabelConfig(clusterID string) prometheusoperatorv1.RelabelConfig {
	return prometheusoperatorv1.RelabelConfig{
		TargetLabel: clusterIDLabel,
		Action:      "replace",
		Replacement: ptr.To(clusterID),
	}
}
