package oapi

import (
	kasv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
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
			component.WithAdaptFunction(kasv2.AdaptAuditConfig),
		).
		WithManifestAdapter(
			"pdb.yaml",
			component.AdaptPodDisruptionBudget(),
		).
		WithDependencies(oapiv2.ComponentName).
		InjectAvailabilityProberContainer(podspec.AvailabilityProberOpts{}).
		InjectKonnectivityContainer(component.KonnectivityContainerOptions{
			Mode: component.Socks5,
			Socks5Options: component.Socks5Options{
				ResolveFromGuestClusterDNS: ptr.To(true),
			},
		}).
		Build()
}

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	if util.HCPExternalOIDCEnabled(cpContext.HCP) && featuregates.Gate().Enabled(featuregates.ExternalOIDCExternalClaimsSourcing) {
		return adaptForExternalOIDC(cpContext, deployment)
	}

	return adaptForOAuth(cpContext, deployment)
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	return util.HCPOAuthEnabled(cpContext.HCP) || featuregates.Gate().Enabled(featuregates.ExternalOIDCExternalClaimsSourcing), nil
}
