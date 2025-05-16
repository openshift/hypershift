package capimanager

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "cluster-api"
)

var _ component.ComponentOptions = &CAPIManagerOptions{}

type CAPIManagerOptions struct {
	imageOverride string
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *CAPIManagerOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *CAPIManagerOptions) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *CAPIManagerOptions) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent(imageOverride string) component.ControlPlaneComponent {
	capi := &CAPIManagerOptions{
		imageOverride: imageOverride,
	}

	return component.NewDeploymentComponent(ComponentName, capi).
		WithAdaptFunction(capi.adaptDeployment).
		WithPredicate(predicate).
		WithManifestAdapter(
			"role.yaml",
			component.SetHostedClusterAnnotation(),
		).
		WithManifestAdapter(
			"rolebinding.yaml",
			component.SetHostedClusterAnnotation(),
		).
		WithManifestAdapter(
			"serviceaccount.yaml",
			component.SetHostedClusterAnnotation(),
		).
		WithManifestAdapter(
			"webhook-tls-secret.yaml",
			component.WithAdaptFunction(adaptWebhookTLSSecret),
			component.ReconcileExisting(),
		).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	_, disable := cpContext.HCP.Annotations[hyperv1.DisableMachineManagement]
	return !disable, nil
}
