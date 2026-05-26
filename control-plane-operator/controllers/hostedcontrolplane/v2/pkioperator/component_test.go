package pkioperator

import (
	"testing"
	"time"

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
			name:           "When DisablePKIReconciliationAnnotation is not present, it should return true",
			hcpAnnotations: map[string]string{},
			expected:       true,
		},
		{
			name:           "When annotations are nil, it should return true",
			hcpAnnotations: nil,
			expected:       true,
		},
		{
			name: "When DisablePKIReconciliationAnnotation is present, it should return false",
			hcpAnnotations: map[string]string{
				hyperv1.DisablePKIReconciliationAnnotation: "true",
			},
			expected: false,
		},
		{
			name: "When DisablePKIReconciliationAnnotation is present with any value, it should return false",
			hcpAnnotations: map[string]string{
				hyperv1.DisablePKIReconciliationAnnotation: "false",
			},
			expected: false,
		},
		{
			name: "When DisablePKIReconciliationAnnotation is present with empty value, it should return false",
			hcpAnnotations: map[string]string{
				hyperv1.DisablePKIReconciliationAnnotation: "",
			},
			expected: false,
		},
		{
			name: "When other annotations are present, it should return true",
			hcpAnnotations: map[string]string{
				"some.other.annotation": "value",
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
				Spec: hyperv1.HostedControlPlaneSpec{},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			result, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestPKIOperatorOptions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name              string
		certRotationScale time.Duration
		validate          func(*WithT, *pkiOperator)
	}{
		{
			name:              "When created with 1 hour rotation scale, it should store that value",
			certRotationScale: time.Hour,
			validate: func(g *WithT, op *pkiOperator) {
				g.Expect(op.certRotationScale).To(Equal(time.Hour))
			},
		},
		{
			name:              "When created with 24 hour rotation scale, it should store that value",
			certRotationScale: 24 * time.Hour,
			validate: func(g *WithT, op *pkiOperator) {
				g.Expect(op.certRotationScale).To(Equal(24 * time.Hour))
			},
		},
		{
			name:              "When created with 30 minute rotation scale, it should store that value",
			certRotationScale: 30 * time.Minute,
			validate: func(g *WithT, op *pkiOperator) {
				g.Expect(op.certRotationScale).To(Equal(30 * time.Minute))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			op := &pkiOperator{
				certRotationScale: tc.certRotationScale,
			}

			tc.validate(g, op)
		})
	}

	t.Run("When IsRequestServing is called, it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		op := &pkiOperator{}
		g.Expect(op.IsRequestServing()).To(BeFalse())
	})

	t.Run("When MultiZoneSpread is called, it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		op := &pkiOperator{}
		g.Expect(op.MultiZoneSpread()).To(BeFalse())
	})

	t.Run("When NeedsManagementKASAccess is called, it should return true", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		op := &pkiOperator{}
		g.Expect(op.NeedsManagementKASAccess()).To(BeTrue())
	})
}
