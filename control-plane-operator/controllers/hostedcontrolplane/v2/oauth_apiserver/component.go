package oapi

import (
	kasv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	"k8s.io/utils/ptr"
)

const (
	ComponentName = "openshift-oauth-apiserver"
)

var _ component.ComponentOptions = &openshiftOAuthAPIServer{}

type openshiftOAuthAPIServer struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (k *openshiftOAuthAPIServer) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (k *openshiftOAuthAPIServer) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (k *openshiftOAuthAPIServer) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &openshiftOAuthAPIServer{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(predicate).
		WithManifestAdapter(
			"audit-config.yaml",
			component.WithPredicate(kasv2.AuditEnabledWorkloadContext),
			component.WithAdaptFunction(kasv2.AdaptAuditConfig),
		).
		WithManifestAdapter(
			"pdb.yaml",
			component.AdaptPodDisruptionBudget(),
		).
		WithDependencies(oapiv2.ComponentName).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{}).
		InjectKonnectivityContainer(component.KonnectivityContainerOptions{
			Mode: component.Socks5,
			Socks5Options: component.Socks5Options{
				ResolveFromGuestClusterDNS: ptr.To(true),
			},
		}).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	return util.HCPOAuthEnabled(cpContext.HCP), nil
}
