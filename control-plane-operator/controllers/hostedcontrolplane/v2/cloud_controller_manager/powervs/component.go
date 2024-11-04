package powervs

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "cloud-controller-manager-powervs"
)

var _ component.ComponentOptions = &powervsOptions{}

type powervsOptions struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *powervsOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *powervsOptions) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *powervsOptions) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &powervsOptions{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(predicate).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfig),
		).
		Build()
}

func predicate(cpContext component.ControlPlaneContext) (bool, error) {
	return cpContext.HCP.Spec.Platform.Type == hyperv1.PowerVSPlatform, nil
}
