package olm

import (
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/olm"
)

type OperatorLifecycleManagerParams struct {
	CertifiedOperatorsImage string
	CommunityOperatorsImage string
	RedHatMarketplaceImage  string
	RedHatOperatorsImage    string
	OLMCatalogPlacement     hyperv1.OLMCatalogPlacement
}

func NewOperatorLifecycleManagerParams(hcp *hyperv1.HostedControlPlane) *OperatorLifecycleManagerParams {
	params := &OperatorLifecycleManagerParams{
		CertifiedOperatorsImage: olm.CatalogToImage["certified-operators"],
		CommunityOperatorsImage: olm.CatalogToImage["community-operators"],
		RedHatMarketplaceImage:  olm.CatalogToImage["redhat-marketplace"],
		RedHatOperatorsImage:    olm.CatalogToImage["redhat-operators"],
		OLMCatalogPlacement:     hcp.Spec.OLMCatalogPlacement,
	}

	return params
}
