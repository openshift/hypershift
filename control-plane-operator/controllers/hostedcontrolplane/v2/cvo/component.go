package cvo

import (
	"os"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/support/awsutil"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/rhobsmonitoring"
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

// isManagementClusterMetricsAccessEnabled determines if CVO needs access to a metrics
// endpoint on the Management Cluster. This covers two scenarios:
//   - Self-managed HyperShift: Thanos Querier in openshift-monitoring namespace
//     (controlled by enableCVOManagementClusterMetricsAccess flag)
//   - ROSA HCP: RHOBS Prometheus in openshift-observability-operator namespace
//     (enabled when RHOBS monitoring is active on ROSA HCP clusters)
func (cvo *clusterVersionOperator) isManagementClusterMetricsAccessEnabled(cpContext component.WorkloadContext) bool {
	return cvo.enableCVOManagementClusterMetricsAccess ||
		(os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1" && awsutil.IsROSAHCP(cpContext.HCP))
}
