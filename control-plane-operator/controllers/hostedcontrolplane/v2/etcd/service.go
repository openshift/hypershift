package etcd

import (
	"fmt"

	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
)

// adaptClientService adapts the etcd client service for the default shard
func adaptClientService(cpContext component.WorkloadContext, svc *corev1.Service) error {
	hcp := cpContext.HCP
	managedEtcdSpec := hcp.Spec.Etcd.Managed

	// Use EffectiveShards to get normalized shard configuration
	shards := managedEtcdSpec.EffectiveShards(hcp)
	if len(shards) == 0 {
		return fmt.Errorf("no etcd shards configured")
	}
	defaultShard := shards[0]

	// Update service name to include shard name
	// For backward compatibility, use "etcd-client" for the default shard
	if defaultShard.Name == "default" {
		svc.Name = "etcd-client"
	} else {
		svc.Name = fmt.Sprintf("etcd-%s-client", defaultShard.Name)
	}

	// Update selector to match shard-specific pods
	if svc.Spec.Selector == nil {
		svc.Spec.Selector = make(map[string]string)
	}
	svc.Spec.Selector["app"] = "etcd"
	svc.Spec.Selector["hypershift.openshift.io/etcd-shard"] = defaultShard.Name

	return nil
}

// adaptDiscoveryService adapts the etcd discovery service for the default shard
func adaptDiscoveryService(cpContext component.WorkloadContext, svc *corev1.Service) error {
	hcp := cpContext.HCP
	managedEtcdSpec := hcp.Spec.Etcd.Managed

	// Use EffectiveShards to get normalized shard configuration
	shards := managedEtcdSpec.EffectiveShards(hcp)
	if len(shards) == 0 {
		return fmt.Errorf("no etcd shards configured")
	}
	defaultShard := shards[0]

	// Update service name to include shard name
	// For backward compatibility, use "etcd-discovery" for the default shard
	if defaultShard.Name == "default" {
		svc.Name = "etcd-discovery"
	} else {
		svc.Name = fmt.Sprintf("etcd-%s-discovery", defaultShard.Name)
	}

	// Update selector to match shard-specific pods
	if svc.Spec.Selector == nil {
		svc.Spec.Selector = make(map[string]string)
	}
	svc.Spec.Selector["app"] = "etcd"
	svc.Spec.Selector["hypershift.openshift.io/etcd-shard"] = defaultShard.Name

	return nil
}
