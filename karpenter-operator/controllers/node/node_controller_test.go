package node

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestNodePoolNameFromNodeClaim(t *testing.T) {
	testCases := []struct {
		name                 string
		nodeClaim            *karpenterv1.NodeClaim
		expectedNodePoolName string
		expectError          bool
	}{
		{
			name: "When nodeClassRef is missing, it should fail",
			nodeClaim: &karpenterv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "claim-1"},
			},
			expectError: true,
		},
		{
			name: "When nodeClassRef name is empty, it should fail",
			nodeClaim: &karpenterv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "claim-1"},
				Spec: karpenterv1.NodeClaimSpec{
					NodeClassRef: &karpenterv1.NodeClassReference{Name: ""},
				},
			},
			expectError: true,
		},
		{
			name: "When nodeClassRef is set, it should return the synthetic NodePool name",
			nodeClaim: &karpenterv1.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "claim-1"},
				Spec: karpenterv1.NodeClaimSpec{
					NodeClassRef: &karpenterv1.NodeClassReference{Name: "default"},
				},
			},
			expectedNodePoolName: "default-karpenter",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			got, err := nodePoolNameFromNodeClaim(tc.nodeClaim)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(got).To(Equal(tc.expectedNodePoolName))
		})
	}
}

func TestReconcileSetsNodePoolLabel(t *testing.T) {
	g := NewWithT(t)

	node := initializedKarpenterNode("worker-1")
	nodeClaim := nodeClaimForNode("worker-1", "default")

	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(node, nodeClaim).Build()
	r := &Reconciler{GuestClient: c}

	_, err := r.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(node)})
	g.Expect(err).NotTo(HaveOccurred())

	updated := &corev1.Node{}
	g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(node), updated)).To(Succeed())
	g.Expect(updated.Labels).To(HaveKeyWithValue(hyperv1.NodePoolLabel, "default-karpenter"))
}

func TestReconcileCorrectsTamperedNodePoolLabel(t *testing.T) {
	g := NewWithT(t)

	node := initializedKarpenterNode("worker-1")
	node.Labels[hyperv1.NodePoolLabel] = "wrong-value"
	nodeClaim := nodeClaimForNode("worker-1", "default")

	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(node, nodeClaim).Build()
	r := &Reconciler{GuestClient: c}

	_, err := r.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(node)})
	g.Expect(err).NotTo(HaveOccurred())

	updated := &corev1.Node{}
	g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(node), updated)).To(Succeed())
	g.Expect(updated.Labels).To(HaveKeyWithValue(hyperv1.NodePoolLabel, "default-karpenter"))
}

func TestReconcileNoOpWhenNodePoolLabelCorrect(t *testing.T) {
	g := NewWithT(t)

	node := initializedKarpenterNode("worker-1")
	node.Labels[hyperv1.NodePoolLabel] = "default-karpenter"
	nodeClaim := nodeClaimForNode("worker-1", "default")

	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(node, nodeClaim).Build()
	r := &Reconciler{GuestClient: c}

	_, err := r.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(node)})
	g.Expect(err).NotTo(HaveOccurred())

	updated := &corev1.Node{}
	g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(node), updated)).To(Succeed())
	g.Expect(updated.Labels).To(HaveKeyWithValue(hyperv1.NodePoolLabel, "default-karpenter"))
}

func TestReconcileSkipsWhenNodeClaimMissing(t *testing.T) {
	g := NewWithT(t)

	node := initializedKarpenterNode("worker-1")

	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(node).Build()
	r := &Reconciler{GuestClient: c}

	_, err := r.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(node)})
	g.Expect(err).NotTo(HaveOccurred())

	updated := &corev1.Node{}
	g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(node), updated)).To(Succeed())
	g.Expect(updated.Labels).NotTo(HaveKey(hyperv1.NodePoolLabel))
}

func initializedKarpenterNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				karpenterv1.NodeInitializedLabelKey: "true",
			},
		},
	}
}

func nodeClaimForNode(nodeName, nodeClassName string) *karpenterv1.NodeClaim {
	return &karpenterv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-1"},
		Spec: karpenterv1.NodeClaimSpec{
			NodeClassRef: &karpenterv1.NodeClassReference{
				Group: "karpenter.k8s.aws",
				Kind:  "EC2NodeClass",
				Name:  nodeClassName,
			},
		},
		Status: karpenterv1.NodeClaimStatus{
			NodeName: nodeName,
		},
	}
}
