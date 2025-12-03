package etcd

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

// ReconcileServiceMonitor reconciles the ServiceMonitor for a specific etcd shard
// This function modifies the ServiceMonitor in place and should be used with createOrUpdate pattern
// Note: Name and namespace are set by the manifest constructor, not here
func ReconcileServiceMonitor(
	sm *prometheusoperatorv1.ServiceMonitor,
	hcp *hyperv1.HostedControlPlane,
	shard hyperv1.ManagedEtcdShardSpec,
	metricsSet metrics.MetricsSet,
) error {
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{sm.Namespace},
	}

	// Update selector to match shard-specific service
	if sm.Spec.Selector.MatchLabels == nil {
		sm.Spec.Selector.MatchLabels = make(map[string]string)
	}
	sm.Spec.Selector.MatchLabels["app"] = "etcd"
	sm.Spec.Selector.MatchLabels["hypershift.openshift.io/etcd-shard"] = shard.Name

	// Apply metric relabeling configurations
	if len(sm.Spec.Endpoints) > 0 {
		sm.Spec.Endpoints[0].MetricRelabelConfigs = metrics.EtcdRelabelConfigs(metricsSet)
		util.ApplyClusterIDLabel(&sm.Spec.Endpoints[0], hcp.Spec.ClusterID)

		// Build complete relabel configs list to avoid duplicates
		priority := string(shard.Priority)
		sm.Spec.Endpoints[0].RelabelConfigs = []prometheusoperatorv1.RelabelConfig{
			{
				TargetLabel: "shard",
				Replacement: &shard.Name,
			},
			{
				TargetLabel: "priority",
				Replacement: &priority,
			},
		}
	}

	return nil
}
