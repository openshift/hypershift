package router

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/support/azureutil"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"
)

const (
	ComponentName = "router"
)

var _ component.ComponentOptions = &router{}

type router struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (k *router) IsRequestServing() bool {
	return true
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (k *router) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (k *router) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &router{}).
		WithPredicate(func(cpContext component.WorkloadContext) (bool, error) {
			return UseHCPRouter(cpContext.HCP), nil
		}).
		WithAdaptFunction(adaptDeployment).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfig),
		).
		WithManifestAdapter(
			"pdb.yaml",
			component.AdaptPodDisruptionBudget(),
		).
		WithDependencies(oapiv2.ComponentName).
		Build()
}

// UseHCPRouter returns true when the HCP routes should be served by a dedicated
// HCP router. This occurs when:
//  1. The cluster is private (e.g. AWS/GCP Private or PublicAndPrivate endpoint access,
//     or ARO with Swift enabled), OR
//  2. The cluster is public but uses a dedicated Route for KAS DNS (rather than a LoadBalancer)
//
// Excludes IBM Cloud platform.
func UseHCPRouter(hcp *hyperv1.HostedControlPlane) bool {
	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		return false
	}
	// For ARO HCP, the dedicated HCP router is only needed when the cluster is private
	// (Swift enabled). LabelHCPRoutes returns true for all ARO to support the
	// SharedIngressReconciler, but that doesn't mean a dedicated router deployment is needed.
	if azureutil.IsAroHCP() {
		return util.IsPrivateHCP(hcp)
	}
	// Router infrastructure is needed when:
	// 1. Cluster has private access (Private or PublicAndPrivate) - for internal routes, OR
	// 2. External routes are labeled for HCP router (Public with KAS DNS)
	return util.IsPrivateHCP(hcp) || util.LabelHCPRoutes(hcp)
}
