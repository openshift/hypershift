package capimanager

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPredicate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		hcpAnnotations map[string]string
		expected       bool
	}{
		{
			name:     "When HCP has no annotations, it should return true",
			expected: true,
		},
		{
			name: "When HCP has DisableMachineManagement annotation, it should return false",
			hcpAnnotations: map[string]string{
				hyperv1.DisableMachineManagement: "true",
			},
			expected: false,
		},
		{
			name: "When HCP has other annotations but not DisableMachineManagement, it should return true",
			hcpAnnotations: map[string]string{
				"some.other/annotation": "value",
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-hcp",
					Namespace:   "test-namespace",
					Annotations: tc.hcpAnnotations,
				},
			}

			cpContext := component.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			result, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestCAPIManagerOptions_IsRequestServing(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIManagerOptions{}
	g.Expect(capi.IsRequestServing()).To(BeFalse())
}

func TestCAPIManagerOptions_MultiZoneSpread(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIManagerOptions{}
	g.Expect(capi.MultiZoneSpread()).To(BeFalse())
}

func TestCAPIManagerOptions_NeedsManagementKASAccess(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	capi := &CAPIManagerOptions{}
	g.Expect(capi.NeedsManagementKASAccess()).To(BeTrue())
}
