package good

type HostedControlPlane struct {
	Status Status
}

type Status struct {
	Ready bool
}

type OtherObject struct {
	Status Status
}

type StatusWriter interface {
	Update(ctx any, obj *HostedControlPlane) error
	Patch(ctx any, obj *HostedControlPlane, patch Patch) error
}

type OtherStatusWriter interface {
	Update(ctx any, obj *OtherObject) error
}

type Client interface {
	Status() StatusWriter
	Patch(ctx any, obj *HostedControlPlane, patch Patch) error
}

type OtherClient interface {
	Status() OtherStatusWriter
}

type Patch struct{}

func MergeFrom(obj *HostedControlPlane) Patch { return Patch{} }

func MergeFromWithOptions(obj *HostedControlPlane, opts ...any) Patch { return Patch{} }

func MergeFromWithOptimisticLock() any { return nil }

// PatchStatus stands in for statuspatching.PatchStatus.
func PatchStatus(c Client, hcp *HostedControlPlane, mutate func() error) error {
	if err := mutate(); err != nil {
		return err
	}
	return c.Patch(nil, hcp, Patch{})
}

func viaHelper(c Client, hcp *HostedControlPlane) error {
	return PatchStatus(c, hcp, func() error {
		hcp.Status.Ready = true
		return nil
	})
}

func guardedMergeFrom(c Client, hcp *HostedControlPlane) error {
	patch := MergeFromWithOptions(hcp, MergeFromWithOptimisticLock())
	return c.Status().Patch(nil, hcp, patch)
}

// Local MergeFrom used for a non-status object patch is unaffected.
func localMergeFromForObjectPatch(c Client, hcp *HostedControlPlane) error {
	return c.Patch(nil, hcp, MergeFrom(hcp))
}

// Status().Update() on a non-HostedControlPlane object is unaffected.
func updateOtherObject(c OtherClient, obj *OtherObject) error {
	return c.Status().Update(nil, obj)
}
