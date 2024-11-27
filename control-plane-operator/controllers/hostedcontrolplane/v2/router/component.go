package router

import (
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
		RolloutOnConfigMapChange("router").
		Build()
}

// useHCPRouter returns true if a dedicated common router is created for a HCP to handle ingress for the managed endpoints.
// This is true when the API input specifies intent for the following:
// 1 - AWS endpointAccess is private somehow (i.e. publicAndPrivate or private) or is public and configured with external DNS.
// 2 - When 1 is true, we recommend (and automate via CLI) ServicePublishingStrategy to be "Route" for all endpoints but the KAS
// which needs a dedicated Service type LB external to be exposed if no external DNS is supported.
// Otherwise, the Routes use the management cluster Domain and resolve through the default ingress controller.
func useHCPRouter(cpContext component.WorkloadContext) (bool, error) {
	if sharedingress.UseSharedIngress() {
		return false, nil
	}
	return util.IsPrivateHCP(cpContext.HCP) || util.IsPublicKASWithDNS(cpContext.HCP), nil
}
