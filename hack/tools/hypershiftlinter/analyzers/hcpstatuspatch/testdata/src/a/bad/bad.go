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
