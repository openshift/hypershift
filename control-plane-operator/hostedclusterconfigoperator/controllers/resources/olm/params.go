package olm

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/catalogs"
	"github.com/openshift/hypershift/support/imageresolution"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
)

type OperatorLifecycleManagerParams struct {
	CertifiedOperatorsImage string
	CommunityOperatorsImage string
	RedHatMarketplaceImage  string
	RedHatOperatorsImage    string
	OLMCatalogPlacement     hyperv1.OLMCatalogPlacement
}

func NewOperatorLifecycleManagerParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, pullSecret *corev1.Secret, imageMetadataProvider util.ImageMetadataProvider) (*OperatorLifecycleManagerParams, error) {
	mirrors, err := imageresolution.ParseImageRegistryMirrorsEnvVar(hcp.Annotations[hyperv1.OLMCatalogsISRegistryOverridesAnnotation])
	if err != nil {
		return nil, fmt.Errorf("failed to parse OLM catalog registry overrides annotation: %w", err)
	}
	catalogImages, err := catalogs.GetCatalogImages(ctx, *hcp, pullSecret.Data[corev1.DockerConfigJsonKey], imageMetadataProvider, imageresolution.ResolverConfig{ImageRegistryMirrors: mirrors})
	if err != nil {
		return nil, fmt.Errorf("failed to get catalog images: %w", err)
	}

	params := &OperatorLifecycleManagerParams{
		CertifiedOperatorsImage: catalogImages["certified-operators"],
		CommunityOperatorsImage: catalogImages["community-operators"],
		RedHatMarketplaceImage:  catalogImages["redhat-marketplace"],
		RedHatOperatorsImage:    catalogImages["redhat-operators"],
		OLMCatalogPlacement:     hcp.Spec.OLMCatalogPlacement,
	}

	return params, nil
}
