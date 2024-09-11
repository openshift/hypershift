package olm

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/olm"
)

type OperatorLifecycleManagerParams struct {
	CertifiedOperatorsImage string
	CommunityOperatorsImage string
	RedHatMarketplaceImage  string
	RedHatOperatorsImage    string
	OLMCatalogPlacement     hyperv1.OLMCatalogPlacement
}

func NewOperatorLifecycleManagerParams(hcp *hyperv1.HostedControlPlane) (*OperatorLifecycleManagerParams, error) {
	catalogImages, err := olm.GetCatalogToImagesWithVersion(hcp.Status.VersionStatus.Desired.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to get catalog images with version: %w", err)
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
