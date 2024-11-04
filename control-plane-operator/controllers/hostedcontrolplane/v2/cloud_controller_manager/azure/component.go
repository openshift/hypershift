package azure

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "cloud-controller-manager-azure"
)

var _ component.ComponentOptions = &azureOptions{}

type azureOptions struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *azureOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *azureOptions) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *azureOptions) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &azureOptions{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(predicate).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfig),
		).
		WithManifestAdapter(
			"config-secret.yaml",
			component.WithAdaptFunction(adaptConfigSecret),
		).
		Build()

}

func predicate(cpContext component.ControlPlaneContext) (bool, error) {
	return cpContext.HCP.Spec.Platform.Type == hyperv1.AzurePlatform, nil
}
