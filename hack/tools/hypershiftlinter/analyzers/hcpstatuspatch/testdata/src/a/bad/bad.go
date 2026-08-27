package bad

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func directUpdate(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	return c.Status().Update(nil, hcp) // want `do not call Status\(\)\.Update\(\) on HostedControlPlane`
}

func unguardedMergeFromInline(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	return c.Status().Patch(nil, hcp, client.MergeFrom(hcp)) // want `do not use MergeFrom\(\) on HostedControlPlane without an optimistic lock`
}

func unguardedMergeFromVar(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	original := hcp
	patch := client.MergeFrom(original) // want `do not use MergeFrom\(\) on HostedControlPlane without an optimistic lock`
	return c.Status().Patch(nil, hcp, patch)
}

func unguardedMergeFromWithOptions(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	return c.Status().Patch(nil, hcp, client.MergeFromWithOptions(hcp)) // want `do not use MergeFromWithOptions\(\) on HostedControlPlane without MergeFromWithOptimisticLock`
}

// A same-name local type must not count as the controller-runtime optimistic lock.
type MergeFromWithOptimisticLock struct{}

func spoofedOptimisticLock(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	return c.Status().Patch(nil, hcp, client.MergeFromWithOptions(hcp, MergeFromWithOptimisticLock{})) // want `do not use MergeFromWithOptions\(\) on HostedControlPlane without MergeFromWithOptimisticLock`
}

func reassignedStillUnguarded(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	patch := client.MergeFromWithOptions(hcp, client.MergeFromWithOptimisticLock{})
	patch = client.MergeFrom(hcp) // want `do not use MergeFrom\(\) on HostedControlPlane without an optimistic lock`
	return c.Status().Patch(nil, hcp, patch)
}

func nestedUnguardedStatusPatch(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	return func() error {
		patch := client.MergeFrom(hcp) // want `do not use MergeFrom\(\) on HostedControlPlane without an optimistic lock`
		return c.Status().Patch(nil, hcp, patch)
	}()
}

func nestedBlockStillUnguarded(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	patch := client.MergeFromWithOptions(hcp, client.MergeFromWithOptimisticLock{})
	{
		patch = client.MergeFrom(hcp) // want `do not use MergeFrom\(\) on HostedControlPlane without an optimistic lock`
	}
	return c.Status().Patch(nil, hcp, patch)
}

func multiValueAssignmentUnguarded(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	other, patch := client.MergeFromWithOptions(hcp, client.MergeFromWithOptimisticLock{}), client.MergeFrom(hcp) // want `do not use MergeFrom\(\) on HostedControlPlane without an optimistic lock`
	_ = other
	return c.Status().Patch(nil, hcp, patch)
}

func ifReassignmentStillReaches(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	patch := client.MergeFrom(hcp) // want `do not use MergeFrom\(\) on HostedControlPlane without an optimistic lock`
	if hcp.Status.Ready {
		patch = client.MergeFromWithOptions(hcp, client.MergeFromWithOptimisticLock{})
	}
	return c.Status().Patch(nil, hcp, patch)
}
