package nodecount

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestReconcileAutoNodeStatus_WhenKarpenterEnabledItShouldCountKarpenterNodesAndNodeClaims(t *testing.T) {
	g := NewWithT(t)

	karpenterNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "karpenter-node-1",
			Labels: map[string]string{
				karpenterv1.NodePoolLabelKey: "default",
			},
		},
	}
	regularNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "regular-node-1",
		},
	}
	nodeClaim1 := &karpenterv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "nc-1"},
	}
	nodeClaim2 := &karpenterv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "nc-2"},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		WithObjects(nodeClaim1, nodeClaim2).
		Build()

	r := &reconciler{guestClusterClient: fakeClient}
	status, err := r.reconcileAutoNodeStatus(context.Background(), []corev1.Node{*karpenterNode, *regularNode})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(status).ToNot(BeNil())
	// Only karpenter-node-1 has the karpenter.sh/nodepool label.
	g.Expect(status.NodeCount).To(Equal(ptr.To(1)))
	// Both NodeClaims should be counted.
	g.Expect(status.NodeClaimCount).To(Equal(ptr.To(2)))
}

func TestReconcileAutoNodeStatus_WhenNoKarpenterNodesItShouldReturnZeroCounts(t *testing.T) {
	g := NewWithT(t)

	regularNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "regular-node-1"},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		Build()

	r := &reconciler{guestClusterClient: fakeClient}
	status, err := r.reconcileAutoNodeStatus(context.Background(), []corev1.Node{*regularNode})

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(status).ToNot(BeNil())
	g.Expect(status.NodeCount).To(Equal(ptr.To(0)))
	g.Expect(status.NodeClaimCount).To(Equal(ptr.To(0)))
}

func TestReconcileAutoNodeStatus_WhenMultipleKarpenterNodesItShouldCountAllWithLabel(t *testing.T) {
	g := NewWithT(t)

	nodes := []corev1.Node{
		{ObjectMeta: metav1.ObjectMeta{Name: "k-node-1", Labels: map[string]string{karpenterv1.NodePoolLabelKey: "pool-a"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "k-node-2", Labels: map[string]string{karpenterv1.NodePoolLabelKey: "pool-b"}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "non-karpenter-1"}},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		Build()

	r := &reconciler{guestClusterClient: fakeClient}
	status, err := r.reconcileAutoNodeStatus(context.Background(), nodes)

	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(status.NodeCount).To(Equal(ptr.To(2)))
	g.Expect(status.NodeClaimCount).To(Equal(ptr.To(0)))
}
