package extoidc

import (
	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/util"

	"k8s.io/utils/ptr"
)

const (
	ComponentName = "external-oidc-webhook"
)

var _ component.ComponentOptions = &externalOIDCWebhook{}

type externalOIDCWebhook struct {
}

// IsRequestServing implements [controlplanecomponent.ComponentOptions].
func (e *externalOIDCWebhook) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements [controlplanecomponent.ComponentOptions].
func (e *externalOIDCWebhook) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements [controlplanecomponent.ComponentOptions].
func (e *externalOIDCWebhook) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &externalOIDCWebhook{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(Predicate).
		WithManifestAdapter(
			"auth-config.yaml",
			component.WithAdaptFunction(adaptAuthConfig),
		).
		WithManifestAdapter(
			"pdb.yaml",
			component.AdaptPodDisruptionBudget(),
		).
		InjectAvailabilityProberContainer(podspec.AvailabilityProberOpts{}).
		InjectKonnectivityContainer(component.KonnectivityContainerOptions{
			Mode: component.Socks5,
			Socks5Options: component.Socks5Options{
				ResolveFromGuestClusterDNS: ptr.To(true),
			},
		}).
		Build()
}

func Predicate(cpContext component.WorkloadContext) (bool, error) {
	return util.HCPExternalOIDCEnabled(cpContext.HCP) &&
		featuregates.Gate().Enabled(featuregates.ExternalOIDCExternalClaimsSourcing), nil

}
