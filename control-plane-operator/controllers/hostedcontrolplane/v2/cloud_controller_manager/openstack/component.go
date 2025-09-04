package openstack

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "openstack-cloud-controller-manager"
)

var _ component.ComponentOptions = &openstackOptions{}

type openstackOptions struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *openstackOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *openstackOptions) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *openstackOptions) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &openstackOptions{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(predicate).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfig),
		).
		WithManifestAdapter(
			"openstack-trusted-ca.yaml",
			component.WithAdaptFunction(adaptTrustedCA),
		).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	return cpContext.HCP.Spec.Platform.Type == hyperv1.OpenStackPlatform, nil
}
