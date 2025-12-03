package etcd

import (
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func adaptServiceMonitor(cpContext component.WorkloadContext, sm *prometheusoperatorv1.ServiceMonitor) error {
	hcp := cpContext.HCP
	managedEtcdSpec := hcp.Spec.Etcd.Managed

	// Use EffectiveShards to get normalized shard configuration
	shards := managedEtcdSpec.EffectiveShards(hcp)
	var defaultShard string
	if len(shards) > 0 {
		defaultShard = shards[0].Name
	} else {
		defaultShard = "default"
	}

	// Update ServiceMonitor name to include shard name
	// For backward compatibility, use "etcd" for the default shard
	if defaultShard == "default" {
		sm.Name = "etcd"
	} else {
		sm.Name = "etcd-" + defaultShard
	}

	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{sm.Namespace},
	}

	// Update selector to match shard-specific service
	if sm.Spec.Selector.MatchLabels == nil {
		sm.Spec.Selector.MatchLabels = make(map[string]string)
	}
	sm.Spec.Selector.MatchLabels["app"] = "etcd"
	sm.Spec.Selector.MatchLabels["hypershift.openshift.io/etcd-shard"] = defaultShard

	sm.Spec.Endpoints[0].MetricRelabelConfigs = metrics.EtcdRelabelConfigs(cpContext.MetricsSet)
	util.ApplyClusterIDLabel(&sm.Spec.Endpoints[0], cpContext.HCP.Spec.ClusterID)

	// Add shard label to metrics
	if len(sm.Spec.Endpoints) > 0 {
		sm.Spec.Endpoints[0].RelabelConfigs = append(sm.Spec.Endpoints[0].RelabelConfigs,
			prometheusoperatorv1.RelabelConfig{
				TargetLabel: "shard",
				Replacement: &defaultShard,
			},
		)
	}

	return nil
}
