package kubevirt

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "cloud-controller-manager-kubevirt"
)

var _ component.ComponentOptions = &kubevirtOptions{}

type kubevirtOptions struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *kubevirtOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *kubevirtOptions) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *kubevirtOptions) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &kubevirtOptions{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(predicate).
		WithManifestAdapter(
			"kubevirt-cloud-config.yaml",
			component.WithAdaptFunction(adaptConfig),
		).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	return cpContext.HCP.Spec.Platform.Type == hyperv1.KubevirtPlatform, nil
}
