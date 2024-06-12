//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	kubevirtv1 "kubevirt.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
)

type KubeVirtNodeSelectorTest struct {
	DummyInfraSetup
	ctx           context.Context
	client        crclient.Client
	hostedCluster *hyperv1.HostedCluster
	nodeSelector  map[string]string
}

func NewKubeKubeVirtNodeSelectorTest(ctx context.Context, cl crclient.Client, hc *hyperv1.HostedCluster) *KubeVirtNodeSelectorTest {
	return &KubeVirtNodeSelectorTest{
		ctx:           ctx,
		client:        cl,
		hostedCluster: hc,
		nodeSelector: map[string]string{
			"nodepool-nodeselector-testlabel": utilrand.String(10),
		},
	}
}

func (k KubeVirtNodeSelectorTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.KubevirtPlatform {
		t.Skip("test only supported on platform KubeVirt")
	}

	t.Log("Starting test KubeVirtNodeSelectorTest")

	g := NewWithT(t)
	infraClient, err := k.GetInfraClient()
	g.Expect(err).ShouldNot(HaveOccurred())

	g.Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		labelSelector := labels.SelectorFromValidatedSet(labels.Set{"cpu-vendor.node.kubevirt.io/Intel": "true"})
		nodes := &corev1.NodeList{}
		err = infraClient.GetInfraClient().List(k.ctx, nodes, &crclient.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			return err
		}
		if len(nodes.Items) == 0 {
			labelSelector := labels.SelectorFromValidatedSet(labels.Set{"cpu-vendor.node.kubevirt.io/AMD": "true"})
			err = infraClient.GetInfraClient().List(k.ctx, nodes, &crclient.ListOptions{LabelSelector: labelSelector})
			if err != nil {
				return err
			}
		}
		g.Expect(len(nodes.Items)).ToNot(Equal(0))
		node := &nodes.Items[0]
		nodeLabels := node.Labels
		for key, value := range k.nodeSelector {
			nodeLabels[key] = value
		}
		node.SetLabels(nodeLabels)
		err := infraClient.GetInfraClient().Update(k.ctx, node, &crclient.UpdateOptions{})
		return err
	})).To(Succeed())
}

func (k KubeVirtNodeSelectorTest) Run(t *testing.T, nodePool hyperv1.NodePool, _ []corev1.Node) {
	g := NewWithT(t)

	localInfraNS := manifests.HostedControlPlaneNamespace(k.hostedCluster.Namespace, k.hostedCluster.Name)
	infraClient, err := k.GetInfraClient()
	g.Expect(err).ShouldNot(HaveOccurred())

	vmis := &kubevirtv1.VirtualMachineInstanceList{}
	labelSelector := labels.SelectorFromValidatedSet(labels.Set{hyperv1.NodePoolNameLabel: nodePool.Name})
	g.Eventually(func(gg Gomega) {
		gg.Expect(
			infraClient.GetInfraClient().List(k.ctx, vmis, &crclient.ListOptions{Namespace: localInfraNS, LabelSelector: labelSelector}),
		).To(Succeed())

		gg.Expect(vmis.Items).To(HaveLen(1))
		vmi := vmis.Items[0]

		gg.Expect(vmi.Spec.NodeSelector).ToNot(BeNil())
		gg.Expect(vmi.Spec.NodeSelector).To(Equal(k.nodeSelector))
	}).WithContext(k.ctx).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

func (k KubeVirtNodeSelectorTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.hostedCluster.Name + "-" + "test-kv-nodeselector",
			Namespace: k.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = ptr.To(int32(1))
	nodePool.Spec.Platform.Kubevirt.NodeSelector = k.nodeSelector

	return nodePool, nil
}

func (k KubeVirtNodeSelectorTest) GetInfraClient() (kvinfra.KubevirtInfraClient, error) {
	localInfraNS := manifests.HostedControlPlaneNamespace(k.hostedCluster.Namespace, k.hostedCluster.Name)
	cm := kvinfra.NewKubevirtInfraClientMap()
	var creds *hyperv1.KubevirtPlatformCredentials
	return cm.DiscoverKubevirtClusterClient(k.ctx, k.client, k.hostedCluster.Spec.InfraID, creds, localInfraNS, k.hostedCluster.Namespace)
}
