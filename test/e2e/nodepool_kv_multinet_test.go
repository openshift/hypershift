//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
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
	e2eutil.EventuallyObject(t, k.infra.Ctx(), "NodePool to have additional networks configured",
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			err := k.infra.MGMTClient().Get(ctx, util.ObjectKey(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(nodePool *hyperv1.NodePool) (done bool, reasons string, err error) {
				want, got := hyperv1.KubevirtPlatform, nodePool.Spec.Platform.Type
				return want == got, fmt.Sprintf("expected NodePool to have platform %s, got %s", want, got), nil
			},
			func(pool *hyperv1.NodePool) (done bool, reasons string, err error) {
				diff := cmp.Diff([]hyperv1.KubevirtNetwork{{
					Name: k.infra.Namespace() + "/" + k.infra.NADName(),
				}}, ptr.Deref(np.Spec.Platform.Kubevirt, hyperv1.KubevirtNodePoolPlatform{}).AdditionalNetworks)
				return diff == "", fmt.Sprintf("incorrect additional networks: %v", diff), nil
			},
		},
	)

	infraClient, err := k.infra.DiscoverClient()
	g.Expect(err).ShouldNot(HaveOccurred())

	e2eutil.EventuallyObjects(t, k.infra.Ctx(), "only one VMI to exist with the correct bridge interface and multus network",
		func(ctx context.Context) ([]*kubevirtv1.VirtualMachineInstance, error) {
			list := &kubevirtv1.VirtualMachineInstanceList{}
			err := infraClient.List(ctx, list, &crclient.ListOptions{Namespace: k.infra.Namespace(), LabelSelector: labels.SelectorFromValidatedSet(labels.Set{hyperv1.NodePoolNameLabel: np.Name})})
			vmis := make([]*kubevirtv1.VirtualMachineInstance, len(list.Items))
			for i := range list.Items {
				vmis[i] = &list.Items[i]
			}
			return vmis, err
		},
		[]e2eutil.Predicate[[]*kubevirtv1.VirtualMachineInstance]{
			func(instances []*kubevirtv1.VirtualMachineInstance) (done bool, reasons string, err error) {
				return len(instances) == 1, fmt.Sprintf("expected %d VMIs, got %d", 1, len(instances)), nil
			},
		},
		[]e2eutil.Predicate[*kubevirtv1.VirtualMachineInstance]{
			func(vmi *kubevirtv1.VirtualMachineInstance) (done bool, reasons string, err error) {
				var ifaceFound bool
				ifaceName := "iface1_" + k.infra.Namespace() + "-" + k.infra.NADName()
				for _, iface := range vmi.Spec.Domain.Devices.Interfaces {
					if iface.Name == ifaceName {
						ifaceFound = iface.Bridge != nil
					}
				}
				return ifaceFound, fmt.Sprintf("expected to find device interface %s with bridge", ifaceName), nil
			},
			func(vmi *kubevirtv1.VirtualMachineInstance) (done bool, reasons string, err error) {
				expectedNetwork := kubevirtv1.Network{
					Name: "iface1_" + k.infra.Namespace() + "-" + k.infra.NADName(),
					NetworkSource: kubevirtv1.NetworkSource{
						Multus: &kubevirtv1.MultusNetwork{
							NetworkName: k.infra.Namespace() + "/" + k.infra.NADName(),
						},
					},
				}
				var networkFound bool
				for _, network := range vmi.Spec.Networks {
					networkFound = networkFound || cmp.Diff(expectedNetwork, network) == ""
				}
				return networkFound, fmt.Sprintf("expected to find multus network, had %#v", vmi.Spec.Networks), nil
			},
		},
	)
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
	nodePool.Spec.Replicas = ptr.To[int32](1)
	return nodePool, nil
}

func (k KubeVirtMultinetTest) SetupInfra(t *testing.T) error {
	return k.infra.CreateOVNKLayer2NAD(k.infra.Namespace())
}
func (k KubeVirtMultinetTest) TeardownInfra(t *testing.T) error {
	// Nothing to do here since the nad is at the hosted cluster namespace
	return nil
}
