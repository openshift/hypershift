package util

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func EnsureNodeCommunication(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureNodeCommunication", func(t *testing.T) {
		g := NewWithT(t)

		guestKubeConfigSecretData, err := WaitForGuestKubeConfig(t, ctx, client, hostedCluster)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't get kubeconfig")

		guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")
		guestClient := kubeclient.NewForConfigOrDie(guestConfig)

		podList, err := guestClient.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{LabelSelector: "app=konnectivity-agent"})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(podList.Items).ToNot(BeEmpty())
		_, err = guestClient.CoreV1().Pods("kube-system").GetLogs(podList.Items[0].Name, &corev1.PodLogOptions{Container: "konnectivity-agent"}).DoRaw(ctx)
		g.Expect(err).NotTo(HaveOccurred())
	})
}

func EnsureNodesLabelsAndTaints(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)

	var taintTransformer = func(taint corev1.Taint) hyperv1.Taint {
		return hyperv1.Taint{
			Key:    taint.Key,
			Value:  taint.Value,
			Effect: taint.Effect,
		}
	}

	for _, node := range nodes {
		for key, value := range nodePool.Spec.NodeLabels {
			g.Expect(node.Labels).To(HaveKeyWithValue(key, value))
		}

		for _, taint := range nodePool.Spec.Taints {
			g.Expect(node.Spec.Taints).To(ContainElement(WithTransform(taintTransformer, Equal(taint))))
		}
	}
}
