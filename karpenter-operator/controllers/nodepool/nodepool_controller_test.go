package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/globalps"
	"github.com/openshift/hypershift/support/api"

	"github.com/awslabs/operatorpkg/status"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	"github.com/go-logr/logr/testr"
)

func TestReconcileNodePool(t *testing.T) {
	testCases := []struct {
		name         string
		spec         hyperkarpenterv1.OpenshiftNodePoolSpec
		expectedSpec karpenterv1.NodePoolSpec
	}{
		{
			name: "When OpenshiftNodePoolSpec.spec is defined all fields should be mirrored and GlobalPullSecret label should be injected",
			spec: hyperkarpenterv1.OpenshiftNodePoolSpec{
				Template: karpenterv1.NodeClaimTemplate{
					ObjectMeta: karpenterv1.ObjectMeta{
						Labels: map[string]string{
							"test-label": "test-value",
						},
						Annotations: map[string]string{
							"test-annotation": "test-value",
						},
					},
					Spec: karpenterv1.NodeClaimTemplateSpec{
						Taints: []corev1.Taint{
							{
								Key:    "test-taint",
								Value:  "test-value",
								Effect: corev1.TaintEffectNoSchedule,
							},
						},
						Requirements: []karpenterv1.NodeSelectorRequirementWithMinValues{
							{
								NodeSelectorRequirement: corev1.NodeSelectorRequirement{
									Key:      "node.kubernetes.io/instance-type",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"m5.large", "m5.xlarge"},
								},
							},
						},
						NodeClassRef: &karpenterv1.NodeClassReference{
							Group: "karpenter.k8s.aws",
							Kind:  "EC2NodeClass",
							Name:  "default",
						},
					},
				},
				Disruption: karpenterv1.Disruption{
					ConsolidationPolicy: karpenterv1.ConsolidationPolicyWhenEmptyOrUnderutilized,
					ConsolidateAfter:    karpenterv1.MustParseNillableDuration("30s"),
				},
				Limits: karpenterv1.Limits{
					corev1.ResourceCPU:    resource.MustParse("1000"),
					corev1.ResourceMemory: resource.MustParse("1000Gi"),
				},
				Weight: ptr.To(int32(10)),
			},
			expectedSpec: karpenterv1.NodePoolSpec{
				Template: karpenterv1.NodeClaimTemplate{
					ObjectMeta: karpenterv1.ObjectMeta{
						Labels: map[string]string{
							"test-label":              "test-value",
							globalps.GlobalPSLabelKey: "true",
						},
						Annotations: map[string]string{
							"test-annotation": "test-value",
						},
					},
					Spec: karpenterv1.NodeClaimTemplateSpec{
						Taints: []corev1.Taint{
							{
								Key:    "test-taint",
								Value:  "test-value",
								Effect: corev1.TaintEffectNoSchedule,
							},
						},
						Requirements: []karpenterv1.NodeSelectorRequirementWithMinValues{
							{
								NodeSelectorRequirement: corev1.NodeSelectorRequirement{
									Key:      "node.kubernetes.io/instance-type",
									Operator: corev1.NodeSelectorOpIn,
									Values:   []string{"m5.large", "m5.xlarge"},
								},
							},
						},
						NodeClassRef: &karpenterv1.NodeClassReference{
							Group: "karpenter.k8s.aws",
							Kind:  "EC2NodeClass",
							Name:  "default",
						},
					},
				},
				Disruption: karpenterv1.Disruption{
					ConsolidationPolicy: karpenterv1.ConsolidationPolicyWhenEmptyOrUnderutilized,
					ConsolidateAfter:    karpenterv1.MustParseNillableDuration("30s"),
				},
				Limits: karpenterv1.Limits{
					corev1.ResourceCPU:    resource.MustParse("1000"),
					corev1.ResourceMemory: resource.MustParse("1000Gi"),
				},
				Weight: ptr.To(int32(10)),
			},
		},
		{
			name: "When OpenshiftNodePool has no labels it should auto-inject GlobalPullSecret label",
			spec: hyperkarpenterv1.OpenshiftNodePoolSpec{
				Template: karpenterv1.NodeClaimTemplate{
					Spec: karpenterv1.NodeClaimTemplateSpec{
						NodeClassRef: &karpenterv1.NodeClassReference{
							Group: "karpenter.k8s.aws",
							Kind:  "EC2NodeClass",
							Name:  "default",
						},
					},
				},
			},
			expectedSpec: karpenterv1.NodePoolSpec{
				Template: karpenterv1.NodeClaimTemplate{
					ObjectMeta: karpenterv1.ObjectMeta{
						Labels: map[string]string{
							globalps.GlobalPSLabelKey: "true",
						},
					},
					Spec: karpenterv1.NodeClaimTemplateSpec{
						NodeClassRef: &karpenterv1.NodeClassReference{
							Group: "karpenter.k8s.aws",
							Kind:  "EC2NodeClass",
							Name:  "default",
						},
					},
				},
			},
		},
		{
			name: "When NodeClassRef is OpenshiftEC2NodeClass it should be translated to EC2NodeClass on the underlying NodePool",
			spec: hyperkarpenterv1.OpenshiftNodePoolSpec{
				Template: karpenterv1.NodeClaimTemplate{
					Spec: karpenterv1.NodeClaimTemplateSpec{
						NodeClassRef: &karpenterv1.NodeClassReference{
							Group: "karpenter.hypershift.openshift.io",
							Kind:  "OpenshiftEC2NodeClass",
							Name:  "my-nodeclass",
						},
					},
				},
			},
			expectedSpec: karpenterv1.NodePoolSpec{
				Template: karpenterv1.NodeClaimTemplate{
					ObjectMeta: karpenterv1.ObjectMeta{
						Labels: map[string]string{
							globalps.GlobalPSLabelKey: "true",
						},
					},
					Spec: karpenterv1.NodeClaimTemplateSpec{
						NodeClassRef: &karpenterv1.NodeClassReference{
							Group: "karpenter.k8s.aws",
							Kind:  "EC2NodeClass",
							Name:  "my-nodeclass",
						},
					},
				},
			},
		},
		{
			name: "When NodeClassRef is EC2NodeClass it should pass through unchanged",
			spec: hyperkarpenterv1.OpenshiftNodePoolSpec{
				Template: karpenterv1.NodeClaimTemplate{
					Spec: karpenterv1.NodeClaimTemplateSpec{
						NodeClassRef: &karpenterv1.NodeClassReference{
							Group: "karpenter.k8s.aws",
							Kind:  "EC2NodeClass",
							Name:  "direct-nodeclass",
						},
					},
				},
			},
			expectedSpec: karpenterv1.NodePoolSpec{
				Template: karpenterv1.NodeClaimTemplate{
					ObjectMeta: karpenterv1.ObjectMeta{
						Labels: map[string]string{
							globalps.GlobalPSLabelKey: "true",
						},
					},
					Spec: karpenterv1.NodeClaimTemplateSpec{
						NodeClassRef: &karpenterv1.NodeClassReference{
							Group: "karpenter.k8s.aws",
							Kind:  "EC2NodeClass",
							Name:  "direct-nodeclass",
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			openshiftNodePool := &hyperkarpenterv1.OpenshiftNodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nodepool",
					UID:  "test-uid",
				},
				Spec: tc.spec,
			}
			nodePool := &karpenterv1.NodePool{}

			r := &NodePoolReconciler{}
			err := r.reconcileNodePool(nodePool, openshiftNodePool)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(nodePool.Spec).To(Equal(tc.expectedSpec))

			g.Expect(nodePool.OwnerReferences).To(HaveLen(1))
			g.Expect(nodePool.OwnerReferences[0].Name).To(Equal("test-nodepool"))
			g.Expect(nodePool.OwnerReferences[0].Kind).To(Equal("OpenshiftNodePool"))
			g.Expect(nodePool.OwnerReferences[0].UID).To(Equal(openshiftNodePool.UID))
		})
	}
}

func TestReconcileStatus(t *testing.T) {
	scheme := api.Scheme

	testCases := []struct {
		name           string
		objects        []client.Object
		nodePoolStatus karpenterv1.NodePoolStatus
		expectedStatus hyperkarpenterv1.OpenshiftNodePoolStatus
	}{
		{
			name: "When NodePool status has resources it should be mirrored to OpenshiftNodePool",
			nodePoolStatus: karpenterv1.NodePoolStatus{
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
			expectedStatus: hyperkarpenterv1.OpenshiftNodePoolStatus{
				Resources: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
		},
		{
			name: "When NodePool status has conditions they should be mirrored to OpenshiftNodePool",
			nodePoolStatus: karpenterv1.NodePoolStatus{
				Conditions: []status.Condition{
					{
						Type:               string(karpenterv1.ConditionTypeValidationSucceeded),
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
						Reason:             "Valid",
						Message:            "NodePool is valid",
					},
				},
			},
			expectedStatus: hyperkarpenterv1.OpenshiftNodePoolStatus{
				Conditions: []status.Condition{
					{
						Type:               string(karpenterv1.ConditionTypeValidationSucceeded),
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
						Reason:             "Valid",
						Message:            "NodePool is valid",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			openshiftNodePool := &hyperkarpenterv1.OpenshiftNodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nodepool",
				},
			}
			g := NewWithT(t)
			fakeManagementClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			fakeGuestClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(openshiftNodePool).
				WithStatusSubresource(openshiftNodePool).
				Build()

			r := &NodePoolReconciler{
				managementClient: fakeManagementClient,
				guestClient:      fakeGuestClient,
				Namespace:        "namespace",
			}

			nodePool := &karpenterv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nodepool",
				},
				Status: tc.nodePoolStatus,
			}
			ctx := log.IntoContext(t.Context(), testr.New(t))

			err := r.reconcileStatus(ctx, nodePool, openshiftNodePool)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(openshiftNodePool.Status.Resources).To(Equal(tc.expectedStatus.Resources))
			g.Expect(openshiftNodePool.Status.Conditions).To(HaveLen(len(tc.expectedStatus.Conditions)))
			if len(tc.expectedStatus.Conditions) > 0 {
				g.Expect(openshiftNodePool.Status.Conditions[0].Type).To(Equal(tc.expectedStatus.Conditions[0].Type))
				g.Expect(openshiftNodePool.Status.Conditions[0].Status).To(Equal(tc.expectedStatus.Conditions[0].Status))
			}
		})
	}
}
