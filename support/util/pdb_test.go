package util

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func TestReconcilePodDisruptionBudget(t *testing.T) {
	tests := []struct {
		name               string
		availability       hyperv1.AvailabilityPolicy
		wantMinAvailable   *intstr.IntOrString
		wantMaxUnavailable *intstr.IntOrString
	}{
		{
			name:               "When SingleReplica it should set minAvailable to 1",
			availability:       hyperv1.SingleReplica,
			wantMinAvailable:   ptr.To(intstr.FromInt32(1)),
			wantMaxUnavailable: nil,
		},
		{
			name:               "When HighlyAvailable it should set maxUnavailable to 1",
			availability:       hyperv1.HighlyAvailable,
			wantMinAvailable:   nil,
			wantMaxUnavailable: ptr.To(intstr.FromInt32(1)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			pdb := &policyv1.PodDisruptionBudget{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pdb",
					Namespace: "test-ns",
				},
			}

			ReconcilePodDisruptionBudget(pdb, tt.availability)

			g.Expect(pdb.Spec.MinAvailable).To(Equal(tt.wantMinAvailable))
			g.Expect(pdb.Spec.MaxUnavailable).To(Equal(tt.wantMaxUnavailable))
			g.Expect(pdb.Spec.UnhealthyPodEvictionPolicy).ToNot(BeNil())
			g.Expect(*pdb.Spec.UnhealthyPodEvictionPolicy).To(Equal(policyv1.AlwaysAllow))
		})
	}
}
