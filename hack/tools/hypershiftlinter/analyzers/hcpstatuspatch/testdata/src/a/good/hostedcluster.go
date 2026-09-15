package good

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/statuspatching"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func sharedHelpers(c client.Client, hc *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) {
	_ = statuspatching.PatchStatus(nil, c, hc, func(current *hyperv1.HostedCluster) error {
		current.Status.Ready = true
		return nil
	})
	_ = statuspatching.PatchStatus(nil, c, hcp, func(current *hyperv1.HostedControlPlane) error {
		current.Status.Ready = true
		return nil
	})
}

func guardedHostedCluster(c client.Client, hc *hyperv1.HostedCluster) error {
	return c.Status().Patch(nil, hc, client.MergeFromWithOptions(hc, client.MergeFromWithOptimisticLock{}))
}

func reassignGuardedHostedCluster(c client.Client, hc *hyperv1.HostedCluster) error {
	patch := client.MergeFrom(hc)
	patch = client.MergeFromWithOptions(hc, client.MergeFromWithOptimisticLock{})
	alias := patch
	return c.Status().Patch(nil, hc, alias)
}

func objectPatchHostedCluster(c client.Client, hc *hyperv1.HostedCluster) error {
	return c.Patch(nil, hc, client.MergeFrom(hc))
}

type HostedCluster struct{}

func unrelatedHostedCluster(c client.Client, hc *HostedCluster) {
	_ = c.Status().Update(nil, hc)
	_ = c.Status().Patch(nil, hc, client.MergeFrom(hc))
}

func lockOptions(c client.Client, hc *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) {
	type LockAlias = client.MergeFromWithOptimisticLock
	lock := LockAlias{}
	_ = c.Status().Patch(nil, hc, client.MergeFromWithOptions(hc, lock))
	_ = c.Status().Patch(nil, hcp, client.MergeFromWithOptions(hcp, &lock))
}
