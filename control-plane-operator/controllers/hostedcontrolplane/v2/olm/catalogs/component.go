package catalogs

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	oapiv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oapi"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	redhatOperatorsCatalogComponentName    = "redhat-operators-catalog"
	communityOperatorsCatalogComponentName = "community-operators-catalog"
	certifiedOperatorsCatalogComponentName = "certified-operators-catalog"

	// extractContentContainerName is the init container that copies the catalog
	// and its pre-built cache out of the catalog image. copy-content reads the
	// cache from --cache.from=/tmp/cache, so it must not get the writable /tmp
	// mount, which would hide the image-provided cache. It only writes into the
	// catalog-content emptyDir, so it does not need a writable /tmp.
	extractContentContainerName = "extract-content"
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
			WithoutTmpDirMount(extractContentContainerName).
			Build(),
		component.NewDeploymentComponent(communityOperatorsCatalogComponentName, catalog).
			WithAdaptFunction(catalog.adaptCatalogDeployment).
			WithPredicate(catalogsPredicate).
			WithDependencies(oapiv2.ComponentName).
			WithoutTmpDirMount(extractContentContainerName).
			Build(),
		component.NewDeploymentComponent(certifiedOperatorsCatalogComponentName, catalog).
			WithAdaptFunction(catalog.adaptCatalogDeployment).
			WithPredicate(catalogsPredicate).
			WithDependencies(oapiv2.ComponentName).
			WithoutTmpDirMount(extractContentContainerName).
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
