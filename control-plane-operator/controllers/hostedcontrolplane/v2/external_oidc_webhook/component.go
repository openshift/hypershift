package extoidc

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/reconcilerpolicy"
)

const (
	ComponentName = "external-oidc-webhook"
)

var _ component.ComponentOptions = &externalOIDCWebhook{}

type externalOIDCWebhook struct {
}

// IsRequestServing implements [controlplanecomponent.ComponentOptions].
func (e *externalOIDCWebhook) IsRequestServing() bool {
	return true
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
		WithPredicate(predicate).
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
				ResolveFromGuestClusterDNS: new(true),
			},
		}).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	return reconcilerpolicy.HCPExternalOIDCEnabled(cpContext.HCP) &&
		featuregates.Gate().Enabled(featuregates.ExternalOIDCAsWebhook), nil
}
