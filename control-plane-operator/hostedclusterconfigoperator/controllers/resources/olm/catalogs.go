package olm

import (
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

func ReconcileCertifiedOperatorsCatalogSource(cs *operatorsv1alpha1.CatalogSource, p *OperatorLifecycleManagerParams) {
	reconcileCatalogSource(cs, "certified-operators:50051", p.CertifiedOperatorsImage, "Certified Operators", -200, p.OLMCatalogPlacement)
}

func ReconcileCommunityOperatorsCatalogSource(cs *operatorsv1alpha1.CatalogSource, p *OperatorLifecycleManagerParams) {
	reconcileCatalogSource(cs, "community-operators:50051", p.CommunityOperatorsImage, "Community Operators", -400, p.OLMCatalogPlacement)
}

func ReconcileRedHatMarketplaceCatalogSource(cs *operatorsv1alpha1.CatalogSource, p *OperatorLifecycleManagerParams) {
	reconcileCatalogSource(cs, "redhat-marketplace:50051", p.RedHatMarketplaceImage, "Red Hat Marketplace", -300, p.OLMCatalogPlacement)
}

func ReconcileRedHatOperatorsCatalogSource(cs *operatorsv1alpha1.CatalogSource, p *OperatorLifecycleManagerParams) {
	reconcileCatalogSource(cs, "redhat-operators:50051", p.RedHatOperatorsImage, "Red Hat Operators", -100, p.OLMCatalogPlacement)
}

func reconcileCatalogSource(cs *operatorsv1alpha1.CatalogSource, address string, image string, displayName string, priority int, placement hyperv1.OLMCatalogPlacement) {
	if cs.Annotations == nil {
		cs.Annotations = map[string]string{}
	}
	cs.Annotations["target.workload.openshift.io/management"] = `{"effect": "PreferredDuringScheduling"}`
	cs.Spec = operatorsv1alpha1.CatalogSourceSpec{
		SourceType:  operatorsv1alpha1.SourceTypeGrpc,
		DisplayName: displayName,
		GrpcPodConfig: &operatorsv1alpha1.GrpcPodConfig{
			SecurityContextConfig: operatorsv1alpha1.Restricted,
		},
		Publisher: "Red Hat",
		Priority:  priority,
		UpdateStrategy: &operatorsv1alpha1.UpdateStrategy{
			RegistryPoll: &operatorsv1alpha1.RegistryPoll{
				RawInterval: "10m",
				Interval:    &metav1.Duration{Duration: 10 * time.Minute},
			},
		},
	}
	if placement == hyperv1.ManagementOLMCatalogPlacement {
		cs.Spec.Address = address
	}
	if placement == hyperv1.GuestOLMCatalogPlacement {
		cs.Spec.Image = image
	}
}
