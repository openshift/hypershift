package good

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type OtherObject struct {
	Status struct{ Ready bool }
}

type otherStatusWriter interface {
	Update(ctx any, obj *OtherObject, opts ...any) error
}

type otherClient interface {
	Status() otherStatusWriter
}

// PatchStatus stands in for statuspatching.PatchStatus.
func PatchStatus(c client.Client, hcp *hyperv1.HostedControlPlane, mutate func() error) error {
	if err := mutate(); err != nil {
		return err
	}
	return c.Patch(nil, hcp, nil)
}

func viaHelper(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	return PatchStatus(c, hcp, func() error {
		hcp.Status.Ready = true
		return nil
	})
}

func guardedMergeFrom(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	patch := client.MergeFromWithOptions(hcp, client.MergeFromWithOptimisticLock{})
	return c.Status().Patch(nil, hcp, patch)
}

// Local MergeFrom used for a non-status object patch is unaffected.
func objectPatchMergeFrom(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	return c.Patch(nil, hcp, client.MergeFrom(hcp))
}

// Status().Update() on a non-HostedControlPlane object is unaffected.
func updateOtherObject(c otherClient, obj *OtherObject) error {
	return c.Status().Update(nil, obj)
}

// A local type named HostedControlPlane is not the HyperShift API type.
type HostedControlPlane struct{}

type localStatusWriter interface {
	Update(ctx any, obj *HostedControlPlane, opts ...any) error
	Patch(ctx any, obj *HostedControlPlane, patch any, opts ...any) error
}

type localClient interface {
	Status() localStatusWriter
}

func updateLocalSameNameType(c localClient, hcp *HostedControlPlane) error {
	return c.Status().Update(nil, hcp)
}

func MergeFrom(obj any) client.Patch { return nil }

// A same-name local MergeFrom on a HyperShift HCP status patch is not the
// controller-runtime constructor, so it must not fire.
func localMergeFromOnHCPStatus(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	return c.Status().Patch(nil, hcp, MergeFrom(hcp))
}

func reassignedToOptimisticLock(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	patch := client.MergeFrom(hcp)
	patch = client.MergeFromWithOptions(hcp, client.MergeFromWithOptimisticLock{})
	return c.Status().Patch(nil, hcp, patch)
}

func nestedShadowDoesNotAffectOuter(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	patch := client.MergeFromWithOptions(hcp, client.MergeFromWithOptimisticLock{})
	_ = func() client.Patch {
		patch := client.MergeFrom(hcp)
		return patch
	}
	return c.Status().Patch(nil, hcp, patch)
}

func nestedBlockReassignment(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	patch := client.MergeFrom(hcp)
	{
		patch = client.MergeFromWithOptions(hcp, client.MergeFromWithOptimisticLock{})
	}
	return c.Status().Patch(nil, hcp, patch)
}

func nestedBlockThenReassignment(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	patch := client.MergeFromWithOptions(hcp, client.MergeFromWithOptimisticLock{})
	{
		patch = client.MergeFrom(hcp)
	}
	patch = client.MergeFromWithOptions(hcp, client.MergeFromWithOptimisticLock{})
	return c.Status().Patch(nil, hcp, patch)
}

func multiValueAssignmentGuarded(c client.Client, hcp *hyperv1.HostedControlPlane) error {
	unguarded, patch := client.MergeFrom(hcp), client.MergeFromWithOptions(hcp, client.MergeFromWithOptimisticLock{})
	_ = unguarded
	return c.Status().Patch(nil, hcp, patch)
}
