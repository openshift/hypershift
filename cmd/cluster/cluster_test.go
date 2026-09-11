package cluster

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	hyperapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNewCreateCommands(t *testing.T) {
	g := NewWithT(t)
	pullSecretFile := filepath.Join(t.TempDir(), "pull-secret.json")
	g.Expect(os.WriteFile(pullSecretFile, []byte(`{"auths":{}}`), 0600)).To(Succeed())

	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-0"},
		Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{{
			Type:    corev1.NodeExternalIP,
			Address: "198.51.100.10",
		}}},
	}).Build()
	clientCalls := 0
	provider := &core.ClientProvider{
		ControllerRuntimeClient: func(_ string) (crclient.Client, error) {
			clientCalls++
			return client, nil
		},
	}
	cmd := NewCreateCommands(provider)
	outputFile := filepath.Join(t.TempDir(), "manifests.yaml")
	cmd.SetArgs([]string{
		"none",
		"--name=injected-client",
		"--pull-secret=" + pullSecretFile,
		"--render-into=" + outputFile,
	})

	g.Expect(cmd.Execute()).To(Succeed())
	g.Expect(clientCalls).To(Equal(1))
	_, err := os.Stat(outputFile)
	g.Expect(err).NotTo(HaveOccurred())
}

func TestNewDestroyCommands(t *testing.T) {
	g := NewWithT(t)
	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "injected-client",
			Namespace: "clusters",
		},
		Spec: hyperv1.HostedClusterSpec{InfraID: "injected-infra"},
	}
	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hostedCluster).Build()
	clientCalls := 0
	provider := &core.ClientProvider{
		ControllerRuntimeClient: func(_ string) (crclient.Client, error) {
			clientCalls++
			return client, nil
		},
	}
	cmd := NewDestroyCommands(provider)
	cmd.SetArgs([]string{
		"none",
		"--name=injected-client",
		"--namespace=clusters",
	})

	g.Expect(cmd.Execute()).To(Succeed())
	g.Expect(clientCalls).To(Equal(1))
	err := client.Get(t.Context(), crclient.ObjectKeyFromObject(hostedCluster), &hyperv1.HostedCluster{})
	g.Expect(err).To(HaveOccurred())
}
