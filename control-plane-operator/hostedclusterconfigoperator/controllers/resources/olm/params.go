package olm

import (
	"context"
	"fmt"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/olm"
	"github.com/openshift/hypershift/support/releaseinfo/registryclient"
	corev1 "k8s.io/api/core/v1"
)

type OperatorLifecycleManagerParams struct {
	CertifiedOperatorsImage string
	CommunityOperatorsImage string
	RedHatMarketplaceImage  string
	RedHatOperatorsImage    string
	OLMCatalogPlacement     hyperv1.OLMCatalogPlacement
}

func NewOperatorLifecycleManagerParams(ctx context.Context, hcp *hyperv1.HostedControlPlane, pullSecret *corev1.Secret, digestLister registryclient.DigestListerFN) (*OperatorLifecycleManagerParams, error) {
	catalogImages, err := olm.GetCatalogImages(ctx, *hcp, pullSecret.Data[corev1.DockerConfigJsonKey], digestLister)
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
