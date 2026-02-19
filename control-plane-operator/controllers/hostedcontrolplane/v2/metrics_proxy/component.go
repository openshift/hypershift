package metricsproxy

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	kasv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "metrics-proxy"
)

var _ component.ComponentOptions = &metricsProxy{}

type metricsProxy struct {
	defaultIngressDomain string
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *metricsProxy) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *metricsProxy) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *metricsProxy) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent(defaultIngressDomain string) component.ControlPlaneComponent {
	mp := &metricsProxy{
		defaultIngressDomain: defaultIngressDomain,
	}

	return component.NewDeploymentComponent(ComponentName, mp).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(predicate).
		WithManifestAdapter(
			"service.yaml",
		).
		WithManifestAdapter(
			"route.yaml",
			component.WithAdaptFunction(mp.adaptRoute),
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
		WithDependencies(kasv2.ComponentName).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	_, disableMonitoring := cpContext.HCP.Annotations[hyperv1.DisableMonitoringServices]
	return !disableMonitoring, nil
}
