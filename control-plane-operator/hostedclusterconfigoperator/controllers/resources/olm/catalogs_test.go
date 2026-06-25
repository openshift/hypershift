package olm

import (
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileCertifiedOperatorsCatalogSource(t *testing.T) {
	testsCases := []struct {
		name      string
		placement hyperv1.OLMCatalogPlacement
		params    *OperatorLifecycleManagerParams
	}{
		{
			name:      "when placement is Management it should set Address",
			placement: hyperv1.ManagementOLMCatalogPlacement,
			params: &OperatorLifecycleManagerParams{
				CertifiedOperatorsImage: "registry.example.com/certified:latest",
				OLMCatalogPlacement:     hyperv1.ManagementOLMCatalogPlacement,
			},
		},
		{
			name:      "when placement is Guest it should set Image",
			placement: hyperv1.GuestOLMCatalogPlacement,
			params: &OperatorLifecycleManagerParams{
				CertifiedOperatorsImage: "registry.example.com/certified:latest",
				OLMCatalogPlacement:     hyperv1.GuestOLMCatalogPlacement,
			},
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cs := &operatorsv1alpha1.CatalogSource{}

			ReconcileCertifiedOperatorsCatalogSource(cs, tc.params)

			// Verify common fields
			g.Expect(cs.Spec.SourceType).To(Equal(operatorsv1alpha1.SourceTypeGrpc))
			g.Expect(cs.Spec.DisplayName).To(Equal("Certified Operators"))
			g.Expect(cs.Spec.Publisher).To(Equal("Red Hat"))
			g.Expect(cs.Spec.Priority).To(Equal(-200))
			g.Expect(cs.Spec.GrpcPodConfig.SecurityContextConfig).To(Equal(operatorsv1alpha1.Restricted))
			g.Expect(cs.Annotations).To(HaveKeyWithValue("target.workload.openshift.io/management", `{"effect": "PreferredDuringScheduling"}`))

			// Verify RegistryPoll interval is 240 minutes
			g.Expect(cs.Spec.UpdateStrategy).ToNot(BeNil())
			g.Expect(cs.Spec.UpdateStrategy.RegistryPoll).ToNot(BeNil())
			g.Expect(cs.Spec.UpdateStrategy.RegistryPoll.Interval).ToNot(BeNil())
			g.Expect(cs.Spec.UpdateStrategy.RegistryPoll.RawInterval).To(Equal("240m"))
			g.Expect(cs.Spec.UpdateStrategy.RegistryPoll.Interval.Duration).To(Equal(240 * time.Minute))

			// Verify placement-specific fields
			if tc.placement == hyperv1.ManagementOLMCatalogPlacement {
				g.Expect(cs.Spec.Address).To(Equal("certified-operators:50051"))
				g.Expect(cs.Spec.Image).To(BeEmpty())
			} else {
				g.Expect(cs.Spec.Image).To(Equal("registry.example.com/certified:latest"))
				g.Expect(cs.Spec.Address).To(BeEmpty())
			}
		})
	}
}

func TestReconcileCommunityOperatorsCatalogSource(t *testing.T) {
	g := NewWithT(t)

	params := &OperatorLifecycleManagerParams{
		CommunityOperatorsImage: "registry.example.com/community:latest",
		OLMCatalogPlacement:     hyperv1.GuestOLMCatalogPlacement,
	}

	cs := &operatorsv1alpha1.CatalogSource{}
	ReconcileCommunityOperatorsCatalogSource(cs, params)

	// Verify RegistryPoll interval is 240 minutes
	g.Expect(cs.Spec.UpdateStrategy).ToNot(BeNil())
	g.Expect(cs.Spec.UpdateStrategy.RegistryPoll).ToNot(BeNil())
	g.Expect(cs.Spec.UpdateStrategy.RegistryPoll.Interval).ToNot(BeNil())
	g.Expect(cs.Spec.UpdateStrategy.RegistryPoll.RawInterval).To(Equal("240m"))
	g.Expect(cs.Spec.UpdateStrategy.RegistryPoll.Interval.Duration).To(Equal(240 * time.Minute))

	g.Expect(cs.Spec.DisplayName).To(Equal("Community Operators"))
	g.Expect(cs.Spec.Priority).To(Equal(-400))
	g.Expect(cs.Spec.Image).To(Equal("registry.example.com/community:latest"))
}

func TestReconcileRedHatMarketplaceCatalogSource(t *testing.T) {
	g := NewWithT(t)

	params := &OperatorLifecycleManagerParams{
		RedHatMarketplaceImage: "registry.example.com/marketplace:latest",
		OLMCatalogPlacement:    hyperv1.ManagementOLMCatalogPlacement,
	}

	cs := &operatorsv1alpha1.CatalogSource{}
	ReconcileRedHatMarketplaceCatalogSource(cs, params)

	// Verify RegistryPoll interval is 240 minutes
	g.Expect(cs.Spec.UpdateStrategy).ToNot(BeNil())
	g.Expect(cs.Spec.UpdateStrategy.RegistryPoll).ToNot(BeNil())
	g.Expect(cs.Spec.UpdateStrategy.RegistryPoll.Interval).ToNot(BeNil())
	g.Expect(cs.Spec.UpdateStrategy.RegistryPoll.RawInterval).To(Equal("240m"))
	g.Expect(cs.Spec.UpdateStrategy.RegistryPoll.Interval.Duration).To(Equal(240 * time.Minute))

	g.Expect(cs.Spec.DisplayName).To(Equal("Red Hat Marketplace"))
	g.Expect(cs.Spec.Priority).To(Equal(-300))
	g.Expect(cs.Spec.Address).To(Equal("redhat-marketplace:50051"))
}

func TestReconcileRedHatOperatorsCatalogSource(t *testing.T) {
	g := NewWithT(t)

	params := &OperatorLifecycleManagerParams{
		RedHatOperatorsImage: "registry.example.com/redhat-operators:latest",
		OLMCatalogPlacement:  hyperv1.GuestOLMCatalogPlacement,
	}

	cs := &operatorsv1alpha1.CatalogSource{}
	ReconcileRedHatOperatorsCatalogSource(cs, params)

	// Verify RegistryPoll interval is 240 minutes
	g.Expect(cs.Spec.UpdateStrategy).ToNot(BeNil())
	g.Expect(cs.Spec.UpdateStrategy.RegistryPoll).ToNot(BeNil())
	g.Expect(cs.Spec.UpdateStrategy.RegistryPoll.Interval).ToNot(BeNil())
	g.Expect(cs.Spec.UpdateStrategy.RegistryPoll.RawInterval).To(Equal("240m"))
	g.Expect(cs.Spec.UpdateStrategy.RegistryPoll.Interval.Duration).To(Equal(240 * time.Minute))

	g.Expect(cs.Spec.DisplayName).To(Equal("Red Hat Operators"))
	g.Expect(cs.Spec.Priority).To(Equal(-100))
	g.Expect(cs.Spec.Image).To(Equal("registry.example.com/redhat-operators:latest"))
}

func TestReconcileCatalogSourcePreservesExistingAnnotations(t *testing.T) {
	g := NewWithT(t)

	params := &OperatorLifecycleManagerParams{
		RedHatOperatorsImage: "registry.example.com/redhat-operators:latest",
		OLMCatalogPlacement:  hyperv1.GuestOLMCatalogPlacement,
	}

	cs := &operatorsv1alpha1.CatalogSource{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"existing-annotation": "existing-value",
			},
		},
	}

	ReconcileRedHatOperatorsCatalogSource(cs, params)

	// Verify existing annotation is preserved and management annotation is added
	g.Expect(cs.Annotations).To(HaveKeyWithValue("existing-annotation", "existing-value"))
	g.Expect(cs.Annotations).To(HaveKeyWithValue("target.workload.openshift.io/management", `{"effect": "PreferredDuringScheduling"}`))
}
