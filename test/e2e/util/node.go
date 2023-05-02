package util

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
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

		// Mulham: konnectivity-agent pod is not available immediately after switching from private to public.
		// This simply adds retries to solve that.
		err = wait.PollImmediateWithContext(ctx, 10*time.Second, 5*time.Minute, func(ctx context.Context) (done bool, err error) {
			podList, err := guestClient.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{LabelSelector: "app=konnectivity-agent"})
			if err != nil || len(podList.Items) == 0 {
				return false, nil
			}

			_, err = guestClient.CoreV1().Pods("kube-system").GetLogs(podList.Items[0].Name, &corev1.PodLogOptions{Container: "konnectivity-agent"}).DoRaw(ctx)
			if err != nil {
				return false, nil
			}

			return true, nil
		})
		g.Expect(err).NotTo(HaveOccurred())
	})
}
