//go:build e2ev2

package util

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/onsi/gomega"
)

func TestWaitForReadyNodesByLabels(t *testing.T) {
	// The wait only fails after a 45 minute timeout, so only the success path is covered here.
	t.Run("When exactly the wanted nodes match the labels and are ready, it should return only those nodes", func(t *testing.T) {
		matchingLabels := map[string]string{"karpenter.sh/nodepool": "test"}
		newNode := func(name string, nodeLabels map[string]string, ready corev1.ConditionStatus) *corev1.Node {
			return &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: name, Labels: nodeLabels},
				Status: corev1.NodeStatus{
					Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}},
				},
			}
		}
		client := fake.NewClientBuilder().WithObjects(
			newNode("matching-ready", matchingLabels, corev1.ConditionTrue),
			newNode("other-not-ready", map[string]string{"karpenter.sh/nodepool": "other"}, corev1.ConditionFalse),
		).Build()

		nodes := WaitForReadyNodesByLabels(gomega.NewWithT(t), t.Context(), client, hyperv1.AWSPlatform, 1, matchingLabels)

		if len(nodes) != 1 || nodes[0].Name != "matching-ready" {
			t.Fatalf("expected only node matching-ready, got %v", nodes)
		}
	})
}
