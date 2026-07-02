package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidOSImageStreamCondition(t *testing.T) {
	testCases := []struct {
		name            string
		nodePool        *hyperv1.NodePool
		expectedRemoved bool
		expectedStatus  corev1.ConditionStatus
		expectedReason  string
	}{
		{
			name: "When osImageStream.Name is empty, it should remove the condition",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec:       hyperv1.NodePoolSpec{},
			},
			expectedRemoved: true,
		},
		{
			name: "When osImageStream.Name is rhel-9, it should set condition True",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-9"},
				},
			},
			expectedStatus: corev1.ConditionTrue,
			expectedReason: hyperv1.AsExpectedReason,
		},
		{
			name: "When osImageStream.Name is rhel-10, it should set condition True",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-10"},
				},
			},
			expectedStatus: corev1.ConditionTrue,
			expectedReason: hyperv1.AsExpectedReason,
		},
		{
			name: "When osImageStream.Name is invalid, it should set condition False",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec: hyperv1.NodePoolSpec{
					OSImageStream: hyperv1.OSImageStreamReference{Name: "rhel-8"},
				},
			},
			expectedStatus: corev1.ConditionFalse,
			expectedReason: hyperv1.NodePoolInvalidOSImageStreamReason,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			r := &NodePoolReconciler{}
			result, err := r.validOSImageStreamCondition(t.Context(), tc.nodePool, &hyperv1.HostedCluster{})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(BeNil())

			cond := FindStatusCondition(tc.nodePool.Status.Conditions, hyperv1.NodePoolValidOSImageStreamConditionType)
			if tc.expectedRemoved {
				g.Expect(cond).To(BeNil())
			} else {
				g.Expect(cond).ToNot(BeNil())
				g.Expect(cond.Status).To(Equal(tc.expectedStatus))
				g.Expect(cond.Reason).To(Equal(tc.expectedReason))
			}
		})
	}
}
