package ignitionserver

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/releaseinfo"
)

const (
	ComponentName = "ignition-server"
)

var _ component.ComponentOptions = &ignitionServer{}

type ignitionServer struct {
	releaseProvider      releaseinfo.ProviderWithOpenShiftImageRegistryOverrides
	defaultIngressDomain string
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *ignitionServer) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *ignitionServer) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *ignitionServer) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent(releaseProvider releaseinfo.ProviderWithOpenShiftImageRegistryOverrides, defaultIngressDomain string) component.ControlPlaneComponent {
	ignition := &ignitionServer{
		releaseProvider:      releaseProvider,
		defaultIngressDomain: defaultIngressDomain,
	}

	return component.NewDeploymentComponent(ComponentName, ignition).
		WithAdaptFunction(ignition.adaptDeployment).
		WithPredicate(predicate).
		WithManifestAdapter(
			"service.yaml",
			component.WithAdaptFunction(adaptService),
		).
		WithManifestAdapter(
			"podmonitor.yaml",
			component.WithAdaptFunction(adaptPodMonitor),
		).
		WithManifestAdapter(
			"route.yaml",
			component.WithAdaptFunction(ignition.adaptRoute),
			component.WithPredicate(routePredicate),
		).
		WithManifestAdapter(
			"ca-cert.yaml",
			component.WithAdaptFunction(adaptCACertSecret),
			component.DisableIfAnnotationExist(hyperv1.DisablePKIReconciliationAnnotation),
			component.ReconcileExisting(),
		).
		WithManifestAdapter(
			"serving-cert.yaml",
			component.WithAdaptFunction(adaptServingCertSecret),
			component.DisableIfAnnotationExist(hyperv1.DisablePKIReconciliationAnnotation),
			component.ReconcileExisting(),
		).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	_, disableIgnition := cpContext.HCP.Annotations[hyperv1.DisableIgnitionServerAnnotation]
	return !disableIgnition, nil
}
