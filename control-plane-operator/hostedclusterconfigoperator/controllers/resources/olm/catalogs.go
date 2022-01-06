package olm

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

func ReconcileCertifiedOperatorsCatalogSource(cs *operatorsv1alpha1.CatalogSource) {
	reconcileCatalogSource(cs, "certified-operators:50051", "Certified Operators", -200)
}

func ReconcileCommunityOperatorsCatalogSource(cs *operatorsv1alpha1.CatalogSource) {
	reconcileCatalogSource(cs, "community-operators:50051", "Community Operators", -400)
}

func ReconcileRedHatMarketplaceCatalogSource(cs *operatorsv1alpha1.CatalogSource) {
	reconcileCatalogSource(cs, "redhat-marketplace:50051", "Red Hat Marketplace", -300)
}

func ReconcileRedHatOperatorsCatalogSource(cs *operatorsv1alpha1.CatalogSource) {
	reconcileCatalogSource(cs, "redhat-operators:50051", "Red Hat Operators", -100)
}

func reconcileCatalogSource(cs *operatorsv1alpha1.CatalogSource, address, displayName string, priority int) {
	if cs.Annotations == nil {
		cs.Annotations = map[string]string{}
	}
	cs.Annotations["target.workload.openshift.io/management"] = `{"effect": "PreferredDuringScheduling"}`
	cs.Spec = operatorsv1alpha1.CatalogSourceSpec{
		SourceType:  operatorsv1alpha1.SourceTypeGrpc,
		Address:     address,
		DisplayName: displayName,
		Publisher:   "Red Hat",
		Priority:    priority,
		UpdateStrategy: &operatorsv1alpha1.UpdateStrategy{
			RegistryPoll: &operatorsv1alpha1.RegistryPoll{
				Interval: &metav1.Duration{Duration: 10 * time.Minute},
			},
		},
	}
}
