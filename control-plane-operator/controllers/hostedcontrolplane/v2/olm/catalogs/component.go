package catalogs

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	redhatOperatorsCatalogComponentName    = "redhat-operators-catalog"
	redhatMarketplaceCatalogComponentName  = "redhat-marketplace-catalog"
	communityOperatorsCatalogComponentName = "community-operators-catalog"
	certifiedOperatorsCatalogComponentName = "certified-operators-catalog"
)

var _ component.ComponentOptions = &catalogOptions{}

type catalogOptions struct {
	capabilityImageStream bool
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (r *catalogOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (r *catalogOptions) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (r *catalogOptions) NeedsManagementKASAccess() bool {
	return false
}

func NewCatalogComponents(capabilityImageStream bool) []component.ControlPlaneComponent {
	catalog := &catalogOptions{
		capabilityImageStream: capabilityImageStream,
	}

	return []component.ControlPlaneComponent{
		component.NewDeploymentComponent(redhatOperatorsCatalogComponentName, catalog).
			WithAdaptFunction(catalog.adaptCatalogDeployment).
			WithPredicate(catalogsPredicate).
			WithManifestAdapter(
				"imagestream.yaml",
				component.WithAdaptFunction(adaptImageStream),
				component.WithPredicate(catalog.imageStreamPredicate),
			).
			WithDependencies(oapiv2.ComponentName).
			Build(),
		component.NewDeploymentComponent(redhatMarketplaceCatalogComponentName, catalog).
			WithAdaptFunction(catalog.adaptCatalogDeployment).
			WithPredicate(catalogsPredicate).
			WithDependencies(oapiv2.ComponentName).
			Build(),
		component.NewDeploymentComponent(communityOperatorsCatalogComponentName, catalog).
			WithAdaptFunction(catalog.adaptCatalogDeployment).
			WithPredicate(catalogsPredicate).
			WithDependencies(oapiv2.ComponentName).
			Build(),
		component.NewDeploymentComponent(certifiedOperatorsCatalogComponentName, catalog).
			WithAdaptFunction(catalog.adaptCatalogDeployment).
			WithPredicate(catalogsPredicate).
			WithDependencies(oapiv2.ComponentName).
			Build(),
	}
}

func catalogsPredicate(cpContext component.WorkloadContext) (bool, error) {
	hcp := cpContext.HCP
	if hcp.Spec.Configuration != nil && hcp.Spec.Configuration.OperatorHub != nil &&
		hcp.Spec.Configuration.OperatorHub.DisableAllDefaultSources {
		return false, nil
	}

	return hcp.Spec.OLMCatalogPlacement == hyperv1.ManagementOLMCatalogPlacement, nil
}
