package ignitionserverproxy

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	ignitionserverv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/ignitionserver"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "ignition-server-proxy"
)

var _ component.ComponentOptions = &ignitionServerProxy{}

type ignitionServerProxy struct {
	defaultIngressDomain string
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *ignitionServerProxy) IsRequestServing() bool {
	return true
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *ignitionServerProxy) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *ignitionServerProxy) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent(defaultIngressDomain string) component.ControlPlaneComponent {
	ignition := &ignitionServerProxy{
		defaultIngressDomain: defaultIngressDomain,
	}

	return component.NewDeploymentComponent(ComponentName, ignition).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(predicate).
		WithManifestAdapter(
			"service.yaml",
			component.WithAdaptFunction(adaptService),
		).
		WithManifestAdapter(
			"route.yaml",
			component.WithAdaptFunction(ignition.adaptRoute),
			component.WithPredicate(routePredicate),
		).
		WithDependencies(ignitionserverv2.ComponentName).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	_, disableIgnition := cpContext.HCP.Annotations[hyperv1.DisableIgnitionServerAnnotation]
	return !disableIgnition && cpContext.HCP.Spec.Platform.Type != hyperv1.IBMCloudPlatform, nil
}
