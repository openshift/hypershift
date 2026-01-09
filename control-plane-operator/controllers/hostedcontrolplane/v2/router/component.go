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
		WithPredicate(func(cpContext component.WorkloadContext) (bool, error) {
			return UseHCPRouter(cpContext.HCP, cpContext.DefaultIngressDomain), nil
		}).
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

// UseHCPRouter returns true if a dedicated HCP router is needed to handle ingress for managed endpoints.
// This is true when:
// 1 - Shared ingress is not enabled, AND
// 2 - AWS endpointAccess is private (i.e. publicAndPrivate or private), OR
// 3 - The HCP is public and has services configured with Route hostnames external to the
//
//	management cluster's default ingress domain.
//
// When hostnames are subdomains of the apps domain, they are served by the management cluster's
// default router via wildcard DNS, so no dedicated HCP router is needed.
func UseHCPRouter(hcp *hyperv1.HostedControlPlane, defaultIngressDomain string) bool {
	if sharedingress.UseSharedIngress() {
		return false
	}
	return util.IsPrivateHCP(hcp) || util.IsPublicWithExternalDNS(hcp, defaultIngressDomain)
}
