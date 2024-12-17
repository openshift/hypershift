package olm

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"

	corev1 "k8s.io/api/core/v1"
)

type OLMService struct {
	Name       string
	Manifest   *corev1.Service
	Reconciler func(*corev1.Service, config.OwnerRef) error
}

func OLMServices(hcpNamespace string) []OLMService {
	return []OLMService{
		{
			Name:       "certifiedOperatorsService",
			Manifest:   manifests.CertifiedOperatorsService(hcpNamespace),
			Reconciler: ReconcileCertifiedOperatorsService,
		},
		{
			Name:       "communityOperatorsService",
			Manifest:   manifests.CommunityOperatorsService(hcpNamespace),
			Reconciler: ReconcileCommunityOperatorsService,
		},
		{
			Name:       "marketplaceOperatorsService",
			Manifest:   manifests.RedHatMarketplaceOperatorsService(hcpNamespace),
			Reconciler: ReconcileRedHatMarketplaceOperatorsService,
		},
		{
			Name:       "redHatOperatorsService",
			Manifest:   manifests.RedHatOperatorsService(hcpNamespace),
			Reconciler: ReconcileRedHatOperatorsService,
		},
	}
}
