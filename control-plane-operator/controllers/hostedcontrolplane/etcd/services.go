package etcd

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
)

// ReconcileClientService reconciles the etcd client service for a specific shard
// This function modifies the Service in place and should be used with createOrUpdate pattern
// Note: Name and namespace are set by the manifest constructor, not here
func ReconcileClientService(
	svc *corev1.Service,
	hcp *hyperv1.HostedControlPlane,
	shard hyperv1.ManagedEtcdShardSpec,
) error {
	// Update selector to match shard-specific pods
	if svc.Spec.Selector == nil {
		svc.Spec.Selector = make(map[string]string)
	}
	svc.Spec.Selector["app"] = "etcd"
	svc.Spec.Selector["hypershift.openshift.io/etcd-shard"] = shard.Name

	return nil
}

// ReconcileDiscoveryService reconciles the etcd discovery service for a specific shard
// This function modifies the Service in place and should be used with createOrUpdate pattern
// Note: Name and namespace are set by the manifest constructor, not here
func ReconcileDiscoveryService(
	svc *corev1.Service,
	hcp *hyperv1.HostedControlPlane,
	shard hyperv1.ManagedEtcdShardSpec,
) error {
	// Update selector to match shard-specific pods
	if svc.Spec.Selector == nil {
		svc.Spec.Selector = make(map[string]string)
	}
	svc.Spec.Selector["app"] = "etcd"
	svc.Spec.Selector["hypershift.openshift.io/etcd-shard"] = shard.Name

	return nil
}
