//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/cluster-api/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
)

type KubeVirtMultinetTest struct {
	ctx           context.Context
	client        crclient.Client
	hostedCluster *hyperv1.HostedCluster
}

func NewKubeVirtMultinetTest(ctx context.Context, cl crclient.Client, hc *hyperv1.HostedCluster) *KubeVirtMultinetTest {
	return &KubeVirtMultinetTest{
		ctx:           ctx,
		client:        cl,
		hostedCluster: hc,
	}
}

func (k KubeVirtMultinetTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.KubevirtPlatform {
		t.Skip("test only supported on platform KubeVirt")
	}

	t.Log("Starting test KubeVirtMultinetTest")
}

func (k KubeVirtMultinetTest) Run(t *testing.T, nodePool hyperv1.NodePool, _ []corev1.Node) {
	g := NewWithT(t)

	np := &hyperv1.NodePool{}
	g.Eventually(func(gg Gomega) {
		gg.Expect(k.client.Get(k.ctx, util.ObjectKey(&nodePool), np)).Should(Succeed())
		gg.Expect(np.Spec.Platform).ToNot(BeNil())
		gg.Expect(np.Spec.Platform.Type).To(Equal(hyperv1.KubevirtPlatform))
		gg.Expect(np.Spec.Platform.Kubevirt).ToNot(BeNil())
		gg.Expect(np.Spec.Platform.Kubevirt.AdditionalNetworks).To(Equal([]hyperv1.KubevirtNetwork{{
			Name: k.nadNamespace() + "/net1",
		}}))
	}).Within(5 * time.Minute).WithPolling(time.Second).Should(Succeed())

	localInfraNS := manifests.HostedControlPlaneNamespace(k.hostedCluster.Namespace, k.hostedCluster.Name)
	var guestNamespace string
	if np.Status.Platform != nil &&
		np.Status.Platform.KubeVirt != nil &&
		np.Status.Platform.KubeVirt.Credentials != nil &&
		len(np.Status.Platform.KubeVirt.Credentials.InfraNamespace) > 0 {

		guestNamespace = np.Status.Platform.KubeVirt.Credentials.InfraNamespace
		g.Expect(np.Status.Platform.KubeVirt.Credentials.InfraKubeConfigSecret).ToNot(BeNil())
		g.Expect(np.Status.Platform.KubeVirt.Credentials.InfraKubeConfigSecret.Key).Should(Equal("kubeconfig"))
	} else {
		guestNamespace = localInfraNS
	}

	cm := kvinfra.NewKubevirtInfraClientMap()
	var creds *hyperv1.KubevirtPlatformCredentials
	if np.Status.Platform != nil && np.Status.Platform.KubeVirt != nil {
		creds = np.Status.Platform.KubeVirt.Credentials
	}
	infraClient, err := cm.DiscoverKubevirtClusterClient(k.ctx, k.client, k.hostedCluster.Spec.InfraID, creds, localInfraNS, np.GetNamespace())
	g.Expect(err).ShouldNot(HaveOccurred())

	vmis := &kubevirtv1.VirtualMachineInstanceList{}
	labelSelector := labels.SelectorFromValidatedSet(labels.Set{hyperv1.NodePoolNameLabel: np.Name})
	g.Eventually(func(gg Gomega) {
		gg.Expect(
			infraClient.GetInfraClient().List(k.ctx, vmis, &crclient.ListOptions{Namespace: guestNamespace, LabelSelector: labelSelector}),
		).To(Succeed())

		gg.Expect(vmis.Items).To(HaveLen(1))
		vmi := vmis.Items[0]
		// Use gomega HaveField so we can skip "Mac" matching
		matchingInterface := &kubevirtv1.Interface{}
		gg.Expect(vmi.Spec.Domain.Devices.Interfaces).To(ContainElement(
			HaveField("Name", "iface1_"+k.nadNamespace()+"-net1"), matchingInterface),
		)
		gg.Expect(matchingInterface.InterfaceBindingMethod.Bridge).ToNot(BeNil())
		gg.Expect(vmi.Spec.Networks).To(ContainElement(kubevirtv1.Network{
			Name: "iface1_" + k.nadNamespace() + "-net1",
			NetworkSource: kubevirtv1.NetworkSource{
				Multus: &kubevirtv1.MultusNetwork{
					NetworkName: k.nadNamespace() + "/net1",
				},
			},
		}))
	}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

func (k KubeVirtMultinetTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {

	nadYAML := fmt.Sprintf(`
apiVersion: "k8s.cni.cncf.io/v1"
kind: NetworkAttachmentDefinition
metadata:
  namespace: %[1]s
  name: %[2]s
spec:
  config: |2
    {
            "cniVersion": "0.3.1",
            "name": "l2-network",
            "type": "ovn-k8s-cni-overlay",
            "topology":"layer2",
            "netAttachDefName": "%[1]s/%[2]s"
    }
`, k.nadNamespace(), "net1")
	nad := &unstructured.Unstructured{Object: map[string]interface{}{}}
	if err := yaml.Unmarshal([]byte(nadYAML), &nad); err != nil {
		return nil, fmt.Errorf("failed unmarshaling net-attach-def: %w", err)
	}
	if err := k.client.Create(context.Background(), nad); err != nil {
		return nil, fmt.Errorf("failed creating net-attach-def: %w", err)
	}

	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.hostedCluster.Name + "-" + "test-kv-multinet",
			Namespace: k.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	if nodePool.Spec.Platform.Kubevirt != nil {
		nodePool.Spec.Platform.Kubevirt.AdditionalNetworks = []hyperv1.KubevirtNetwork{{
			Name: k.nadNamespace() + "/net1",
		}}
	}
	nodePool.Spec.Replicas = ptr.To(int32(1))
	return nodePool, nil
}

func (k KubeVirtMultinetTest) nadNamespace() string {
	return fmt.Sprintf("%s-%s", k.hostedCluster.Namespace, k.hostedCluster.Name)
}
