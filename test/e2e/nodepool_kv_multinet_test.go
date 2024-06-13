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
	"k8s.io/utils/ptr"
	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/cluster-api/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

type KubeVirtMultinetTest struct {
	infra e2eutil.KubeVirtInfra
}

func NewKubeVirtMultinetTest(ctx context.Context, cl crclient.Client, hc *hyperv1.HostedCluster) *KubeVirtMultinetTest {
	return &KubeVirtMultinetTest{
		infra: e2eutil.NewKubeVirtInfra(ctx, cl, hc),
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
		gg.Expect(k.infra.MGMTClient().Get(k.infra.Ctx(), util.ObjectKey(&nodePool), np)).Should(Succeed())
		gg.Expect(np.Spec.Platform).ToNot(BeNil())
		gg.Expect(np.Spec.Platform.Type).To(Equal(hyperv1.KubevirtPlatform))
		gg.Expect(np.Spec.Platform.Kubevirt).ToNot(BeNil())
		gg.Expect(np.Spec.Platform.Kubevirt.AdditionalNetworks).To(Equal([]hyperv1.KubevirtNetwork{{
			Name: k.infra.Namespace() + "/" + k.infra.NADName(),
		}}))
	}).Within(5 * time.Minute).WithPolling(time.Second).Should(Succeed())

	infraClient, err := k.infra.DiscoverClient()
	g.Expect(err).ShouldNot(HaveOccurred())

	vmis := &kubevirtv1.VirtualMachineInstanceList{}
	labelSelector := labels.SelectorFromValidatedSet(labels.Set{hyperv1.NodePoolNameLabel: np.Name})
	g.Eventually(func(gg Gomega) {
		gg.Expect(
			infraClient.List(k.infra.Ctx(), vmis, &crclient.ListOptions{Namespace: k.infra.Namespace(), LabelSelector: labelSelector}),
		).To(Succeed())

		gg.Expect(vmis.Items).To(HaveLen(1))
		vmi := vmis.Items[0]
		// Use gomega HaveField so we can skip "Mac" matching
		matchingInterface := &kubevirtv1.Interface{}
		gg.Expect(vmi.Spec.Domain.Devices.Interfaces).To(ContainElement(
			HaveField("Name", "iface1_"+k.infra.Namespace()+"-"+k.infra.NADName()), matchingInterface),
		)
		gg.Expect(matchingInterface.InterfaceBindingMethod.Bridge).ToNot(BeNil())
		gg.Expect(vmi.Spec.Networks).To(ContainElement(kubevirtv1.Network{
			Name: "iface1_" + k.infra.Namespace() + "-" + k.infra.NADName(),
			NetworkSource: kubevirtv1.NetworkSource{
				Multus: &kubevirtv1.MultusNetwork{
					NetworkName: k.infra.Namespace() + "/" + k.infra.NADName(),
				},
			},
		}))
	}).WithContext(k.infra.Ctx()).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

func (k KubeVirtMultinetTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.infra.HostedCluster().Name + "-" + "test-kv-multinet",
			Namespace: k.infra.HostedCluster().Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	if nodePool.Spec.Platform.Kubevirt != nil {
		nodePool.Spec.Platform.Kubevirt.AdditionalNetworks = []hyperv1.KubevirtNetwork{{
			Name: k.infra.Namespace() + "/" + k.infra.NADName(),
		}}
	}
	nodePool.Spec.Replicas = ptr.To(int32(1))
	return nodePool, nil
}

func (k KubeVirtMultinetTest) SetupInfra(t *testing.T) error {
	return k.infra.CreateOVNKLayer2NAD(k.infra.Namespace())
}
func (k KubeVirtMultinetTest) TeardownInfra(t *testing.T) error {
	// Nothing to do here since the nad is at the hosted cluster namespace
	return nil
}
