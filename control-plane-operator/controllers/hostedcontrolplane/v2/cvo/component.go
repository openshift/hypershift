package cvo

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"
)

const (
	ComponentName = "cluster-version-operator"
)

var _ component.ComponentOptions = &clusterVersionOperator{}

type clusterVersionOperator struct {
	enableCVOManagementClusterMetricsAccess bool
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (k *clusterVersionOperator) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (k *clusterVersionOperator) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (k *clusterVersionOperator) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent(enableCVOManagementClusterMetricsAccess bool) component.ControlPlaneComponent {
	cvo := &clusterVersionOperator{
		enableCVOManagementClusterMetricsAccess: enableCVOManagementClusterMetricsAccess,
	}

	return component.NewDeploymentComponent(ComponentName, cvo).
		WithAdaptFunction(cvo.adaptDeployment).
		WithManifestAdapter(
			"service.yaml",
			component.DisableIfAnnotationExist(hyperv1.DisableMonitoringServices),
		).
		WithManifestAdapter(
			"servicemonitor.yaml",
			component.WithAdaptFunction(adaptServiceMonitor),
			component.DisableIfAnnotationExist(hyperv1.DisableMonitoringServices),
		).
		WithManifestAdapter(
			"serviceaccount.yaml",
			component.WithPredicate(cvo.isManagementClusterMetricsAccessEnabled),
		).
		WithManifestAdapter(
			"role.yaml",
			component.WithPredicate(cvo.isManagementClusterMetricsAccessEnabled),
		).
		WithManifestAdapter(
			"rolebinding.yaml",
			component.WithPredicate(cvo.isManagementClusterMetricsAccessEnabled),
		).
		WithDependencies(oapiv2.ComponentName).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{
			KubeconfigVolumeName: "kubeconfig",
		}).
		Build()
}

func (cvo *clusterVersionOperator) isManagementClusterMetricsAccessEnabled(cpContext component.ControlPlaneContext) bool {
	return cvo.enableCVOManagementClusterMetricsAccess
}
