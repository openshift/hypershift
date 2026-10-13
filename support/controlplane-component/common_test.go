package controlplanecomponent

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func TestAdaptPodDisruptionBudget(t *testing.T) {
	tests := []struct {
		name               string
		availabilityPolicy hyperv1.AvailabilityPolicy
		wantMinAvailable   *intstr.IntOrString
		wantMaxUnavailable *intstr.IntOrString
	}{
		{
			name:               "When SingleReplica it should set minAvailable to 1",
			availabilityPolicy: hyperv1.SingleReplica,
			wantMinAvailable:   ptr.To(intstr.FromInt32(1)),
			wantMaxUnavailable: nil,
		},
		{
			name:               "When HighlyAvailable it should set maxUnavailable to 1",
			availabilityPolicy: hyperv1.HighlyAvailable,
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

			opt := AdaptPodDisruptionBudget()
			ga := &genericAdapter{}
			opt(ga)

			cpContext := WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						ControllerAvailabilityPolicy: tt.availabilityPolicy,
					},
				},
			}

			err := ga.adapt(cpContext, pdb)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(pdb.Spec.MinAvailable).To(Equal(tt.wantMinAvailable))
			g.Expect(pdb.Spec.MaxUnavailable).To(Equal(tt.wantMaxUnavailable))
			g.Expect(pdb.Spec.UnhealthyPodEvictionPolicy).ToNot(BeNil())
			g.Expect(*pdb.Spec.UnhealthyPodEvictionPolicy).To(Equal(policyv1.AlwaysAllow))
		})
	}

	t.Run("When PDB already has unhealthyPodEvictionPolicy set to IfHealthyBudget it should overwrite to AlwaysAllow", func(t *testing.T) {
		g := NewGomegaWithT(t)

		pdb := &policyv1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pdb",
				Namespace: "test-ns",
			},
			Spec: policyv1.PodDisruptionBudgetSpec{
				UnhealthyPodEvictionPolicy: ptr.To(policyv1.IfHealthyBudget),
			},
		}

		opt := AdaptPodDisruptionBudget()
		ga := &genericAdapter{}
		opt(ga)

		cpContext := WorkloadContext{
			HCP: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					ControllerAvailabilityPolicy: hyperv1.HighlyAvailable,
				},
			},
		}

		err := ga.adapt(cpContext, pdb)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(pdb.Spec.UnhealthyPodEvictionPolicy).ToNot(BeNil())
		g.Expect(*pdb.Spec.UnhealthyPodEvictionPolicy).To(Equal(policyv1.AlwaysAllow))
	})
}
