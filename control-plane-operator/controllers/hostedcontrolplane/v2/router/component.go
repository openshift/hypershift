package router

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/hypershift-operator/controllers/sharedingress"
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
		WithPredicate(useHCPRouter).
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
// HCP router, as determined by util.LabelHCPRoutes. This occurs when:
//  1. The cluster has no public internet access (Private endpoint access), OR
//  2. The cluster has public internet access (Public or PublicAndPrivate endpoint access)
//     but uses a dedicated Route for KAS DNS (rather than a LoadBalancer)
//
// Excludes shared ingress configurations and IBM Cloud platform.
func UseHCPRouter(hcp *hyperv1.HostedControlPlane) bool {
	if sharedingress.UseSharedIngress() {
		return false
	}
	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform {
		return false
	}
	// Router infrastructure is needed when:
	// 1. Cluster has private access (Private or PublicAndPrivate) - for internal routes, OR
	// 2. External routes are labeled for HCP router (Public with KAS DNS)
	return util.IsPrivateHCP(hcp) || util.LabelHCPRoutes(hcp)
}

// useHCPRouter is an adapter for the component predicate interface.
func useHCPRouter(cpContext component.WorkloadContext) (bool, error) {
	return UseHCPRouter(cpContext.HCP), nil
}
