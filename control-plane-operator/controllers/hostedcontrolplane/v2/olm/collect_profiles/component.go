package collectprofiles

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "olm-collect-profiles"
)

var _ component.ComponentOptions = &olmCollectProfiles{}

type olmCollectProfiles struct{}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *olmCollectProfiles) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *olmCollectProfiles) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *olmCollectProfiles) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewCronJobComponent(ComponentName, &olmCollectProfiles{}).
		WithAdaptFunction(adaptCronJob).
		WithPredicate(predicate).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	return cpContext.HCP.Spec.Platform.Type != hyperv1.IBMCloudPlatform, nil
}
