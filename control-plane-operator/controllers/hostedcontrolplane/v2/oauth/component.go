package oauth

import (
	"fmt"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"

	"k8s.io/utils/ptr"
)

const (
	ComponentName = "oauth-openshift"

	httpKonnectivityProxyPort = 8092
)

var _ component.ComponentOptions = &oauthServer{}

type oauthServer struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (k *oauthServer) IsRequestServing() bool {
	return true
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (k *oauthServer) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (k *oauthServer) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return newComponent(nil)
}

func RenderDeployment(cpContext component.ControlPlaneContext, existingDeployment *appsv1.Deployment) (*appsv1.Deployment, error) {
	deploymentComponent := newComponent(component.NewWrappedDeploymentProvider(existingDeployment))
	deploymentComponentRenderer, ok := deploymentComponent.(component.ControlPlaneComponentRenderer[*appsv1.Deployment])
	if !ok {
		return nil, fmt.Errorf("%T is not a ControlPlaneComponentRenderer for Deployment", deploymentComponent)
	}
	return deploymentComponentRenderer.Render(cpContext)
}

func newComponent(customWorkloadProviderWrapper component.WorkloadProviderWrapper[*appsv1.Deployment]) component.ControlPlaneComponent {
	deploymentComponent := component.NewDeploymentComponent(ComponentName, &oauthServer{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(isOAuthEnabled).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfigMap),
		).
		WithManifestAdapter(
			"audit-config.yaml",
			component.WithAdaptFunction(adaptAuditConfig),
		).
		WithManifestAdapter(
			"pdb.yaml",
			component.AdaptPodDisruptionBudget(),
		).
		WithManifestAdapter(
			"service-session-secret.yaml",
			component.WithAdaptFunction(adaptSessionSecret),
		).
		WithManifestAdapter(
			"default-login-template-secret.yaml",
			component.WithAdaptFunction(adaptLoginTemplateSecret),
		).
		WithManifestAdapter(
			"default-provider-selection-template-secret.yaml",
			component.WithAdaptFunction(adaptProviderSelectionTemplateSecret),
		).
		WithManifestAdapter(
			"default-error-template-secret.yaml",
			component.WithAdaptFunction(adaptErrorTemplateSecret),
		).
		WithDependencies(oapiv2.ComponentName).
		InjectKonnectivityContainer(component.KonnectivityContainerOptions{
			Mode: component.Dual,
			Socks5Options: component.Socks5Options{
				ResolveFromGuestClusterDNS:      ptr.To(true),
				ResolveFromManagementClusterDNS: ptr.To(true),
			},
			HTTPSOptions: component.HTTPSOptions{
				ServingPort:                httpKonnectivityProxyPort,
				ConnectDirectlyToCloudAPIs: ptr.To(true),
			},
		}).
		InjectAvailabilityProberContainer(util.AvailabilityProberOpts{})
	if customWorkloadProviderWrapper != nil {
		deploymentComponent.WithWrappedWorkloadProvider(customWorkloadProviderWrapper)
	}
	return deploymentComponent.Build()
}

func isOAuthEnabled(cpContext component.WorkloadContext) (bool, error) {
	return util.HCPOAuthEnabled(cpContext.HCP), nil
}
