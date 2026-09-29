package bad

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Both resources appear in the controller signature; diagnose the update target.
func updateHostedCluster(ctx context.Context, c client.Client, hc *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) error {
	return c.Status().Update(ctx, hc) // want `do not call Status\(\)\.Update\(\) on HostedCluster;`
}

func inlineHostedCluster(c client.Client, hc *hyperv1.HostedCluster) error {
	return c.Status().Patch(nil, hc, client.MergeFrom(hc)) // want `do not use MergeFrom\(\) on HostedCluster without an optimistic lock`
}

func variableHostedCluster(c client.Client, hc *hyperv1.HostedCluster) error {
	original := hc
	var patch = client.MergeFrom(original) // want `do not use MergeFrom\(\) on HostedCluster without an optimistic lock`
	return c.Status().Patch(nil, hc, patch)
}

func optionsHostedCluster(c client.Client, hc *hyperv1.HostedCluster) error {
	return c.Status().Patch(nil, hc, client.MergeFromWithOptions(hc)) // want `do not use MergeFromWithOptions\(\) on HostedCluster without MergeFromWithOptimisticLock`
}

func spoofedLockHostedCluster(c client.Client, hc *hyperv1.HostedCluster) error {
	return c.Status().Patch(nil, hc, client.MergeFromWithOptions(hc, MergeFromWithOptimisticLock{})) // want `do not use MergeFromWithOptions\(\) on HostedCluster without MergeFromWithOptimisticLock`
}

type ClusterAlias = hyperv1.HostedCluster
type ControlPlaneAlias = hyperv1.HostedControlPlane
type ClusterPointerAlias = *ClusterAlias

func updateAliases(c client.Client, hc ClusterPointerAlias, hcp *ControlPlaneAlias) {
	_ = c.Status().Update(nil, hc)  // want `do not call Status\(\)\.Update\(\) on HostedCluster;`
	_ = c.Status().Update(nil, hcp) // want `do not call Status\(\)\.Update\(\) on HostedControlPlane;`
}

func patchAlias(c client.Client, hc *ClusterAlias) error {
	unsafe := client.MergeFrom(hc) // want `do not use MergeFrom\(\) on HostedCluster without an optimistic lock`
	alias := unsafe
	patch := alias
	return c.Status().Patch(nil, hc, patch)
}

func reassignHostedCluster(c client.Client, hc *hyperv1.HostedCluster) error {
	patch := client.MergeFromWithOptions(hc, client.MergeFromWithOptimisticLock{})
	{
		patch = client.MergeFrom(hc) // want `do not use MergeFrom\(\) on HostedCluster without an optimistic lock`
	}
	return c.Status().Patch(nil, hc, patch)
}

func conditionalHostedCluster(c client.Client, hc *hyperv1.HostedCluster) error {
	patch := client.MergeFrom(hc) // want `do not use MergeFrom\(\) on HostedCluster without an optimistic lock`
	if hc.Status.Ready {
		patch = client.MergeFromWithOptions(hc, client.MergeFromWithOptimisticLock{})
	}
	return c.Status().Patch(nil, hc, patch)
}
