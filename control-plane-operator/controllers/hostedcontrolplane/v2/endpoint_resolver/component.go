package endpointresolver

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "endpoint-resolver"
)

var _ component.ComponentOptions = &endpointResolver{}

type endpointResolver struct{}

func (r *endpointResolver) IsRequestServing() bool {
	return false
}

func (r *endpointResolver) MultiZoneSpread() bool {
	return false
}

func (r *endpointResolver) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &endpointResolver{}).
		WithPredicate(Predicate).
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

// Predicate returns true when metrics forwarding components should be deployed.
func Predicate(cpContext component.WorkloadContext) (bool, error) {
	_, disableMonitoring := cpContext.HCP.Annotations[hyperv1.DisableMonitoringServices]
	return !disableMonitoring && cpContext.HCP.Spec.Monitoring.MetricsForwarding.Mode == hyperv1.MetricsForwardingModeForward, nil
}
