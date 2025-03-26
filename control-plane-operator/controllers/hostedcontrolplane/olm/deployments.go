package olm

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"

	appsv1 "k8s.io/api/apps/v1"
)

type OLMDeployment struct {
	Name       string
	Manifest   *appsv1.Deployment
	Reconciler func(*appsv1.Deployment, config.OwnerRef, config.DeploymentConfig, string, string) error
	Image      string
}

func OLMDeployments(p *OperatorLifecycleManagerParams, hcpNamespace string) []OLMDeployment {
	return []OLMDeployment{
		{
			Name:       "certifiedOperatorsDeployment",
			Manifest:   manifests.CertifiedOperatorsDeployment(hcpNamespace),
			Reconciler: ReconcileCertifiedOperatorsDeployment,
			Image:      p.CertifiedOperatorsCatalogImageOverride,
		},
		{
			Name:       "communityOperatorsDeployment",
			Manifest:   manifests.CommunityOperatorsDeployment(hcpNamespace),
			Reconciler: ReconcileCommunityOperatorsDeployment,
			Image:      p.CommunityOperatorsCatalogImageOverride,
		},
		{
			Name:       "marketplaceOperatorsDeployment",
			Manifest:   manifests.RedHatMarketplaceOperatorsDeployment(hcpNamespace),
			Reconciler: ReconcileRedHatMarketplaceOperatorsDeployment,
			Image:      p.RedHatMarketplaceCatalogImageOverride,
		},
		{
			Name:       "redHatOperatorsDeployment",
			Manifest:   manifests.RedHatOperatorsDeployment(hcpNamespace),
			Reconciler: ReconcileRedHatOperatorsDeployment,
			Image:      p.RedHatOperatorsCatalogImageOverride,
		},
	}
}
