package bad

type HostedControlPlane struct {
	Status Status
}

type Status struct {
	Ready bool
}

type StatusWriter interface {
	Update(ctx any, obj *HostedControlPlane) error
	Patch(ctx any, obj *HostedControlPlane, patch Patch) error
}

type Client interface {
	Status() StatusWriter
	Patch(ctx any, obj *HostedControlPlane, patch Patch) error
}

type Patch struct{}

func MergeFrom(obj *HostedControlPlane) Patch { return Patch{} }

func MergeFromWithOptions(obj *HostedControlPlane, opts ...any) Patch { return Patch{} }

func directUpdate(c Client, hcp *HostedControlPlane) error {
	return c.Status().Update(nil, hcp) // want `do not call Status\(\)\.Update\(\) on HostedControlPlane`
}

func unguardedMergeFromInline(c Client, hcp *HostedControlPlane) error {
	return c.Status().Patch(nil, hcp, MergeFrom(hcp)) // want `do not use MergeFrom\(\) on HostedControlPlane without an optimistic lock`
}

func unguardedMergeFromVar(c Client, hcp *HostedControlPlane) error {
	original := hcp
	patch := MergeFrom(original) // want `do not use MergeFrom\(\) on HostedControlPlane without an optimistic lock`
	return c.Status().Patch(nil, hcp, patch)
}

func unguardedMergeFromWithOptions(c Client, hcp *HostedControlPlane) error {
	return c.Status().Patch(nil, hcp, MergeFromWithOptions(hcp)) // want `do not use MergeFromWithOptions\(\) on HostedControlPlane without MergeFromWithOptimisticLock`
}
