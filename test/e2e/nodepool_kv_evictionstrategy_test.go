//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"
	kubevirtv1 "kubevirt.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
)

type KubeVirtEvictionStrategyTest struct {
	DummyInfraSetup
	ctx           context.Context
	client        crclient.Client
	hostedCluster *hyperv1.HostedCluster
}

func NewKubeVirtEvictionStrategyTest(ctx context.Context, cl crclient.Client, hc *hyperv1.HostedCluster) *KubeVirtEvictionStrategyTest {
	return &KubeVirtEvictionStrategyTest{
		ctx:           ctx,
		client:        cl,
		hostedCluster: hc,
	}
}

func (k KubeVirtEvictionStrategyTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.KubevirtPlatform {
		t.Skip("test only supported on platform KubeVirt")
	}

	t.Log("Starting test KubeVirtEvictionStrategyTest")

}

func (k KubeVirtEvictionStrategyTest) Run(t *testing.T, nodePool hyperv1.NodePool, _ []corev1.Node) {
	g := NewWithT(t)

	localInfraNS := manifests.HostedControlPlaneNamespace(k.hostedCluster.Namespace, k.hostedCluster.Name)
	infraClient, err := k.GetInfraClient()
	g.Expect(err).ShouldNot(HaveOccurred())

	e2eutil.EventuallyObjects(t, k.ctx, "one VMI to exist with the correct eviction strategy",
		func(ctx context.Context) ([]*kubevirtv1.VirtualMachineInstance, error) {
			list := &kubevirtv1.VirtualMachineInstanceList{}
			err := infraClient.GetInfraClient().List(ctx, list, &crclient.ListOptions{Namespace: localInfraNS, LabelSelector: labels.SelectorFromValidatedSet(labels.Set{hyperv1.NodePoolNameLabel: nodePool.Name})})
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
				return vmi.Spec.EvictionStrategy != nil && *vmi.Spec.EvictionStrategy == kubevirtv1.EvictionStrategyExternal, fmt.Sprintf("unexpected VMI evictionStrategy: %v", vmi.Spec.EvictionStrategy), nil
			},
		},
	)
}

func (k KubeVirtEvictionStrategyTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.hostedCluster.Name + "-" + "test-kv-evictionstrategy",
			Namespace: k.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = ptr.To[int32](1)

	return nodePool, nil
}

func (k KubeVirtEvictionStrategyTest) GetInfraClient() (kvinfra.KubevirtInfraClient, error) {
	localInfraNS := manifests.HostedControlPlaneNamespace(k.hostedCluster.Namespace, k.hostedCluster.Name)
	cm := kvinfra.NewKubevirtInfraClientMap()
	var creds *hyperv1.KubevirtPlatformCredentials
	return cm.DiscoverKubevirtClusterClient(k.ctx, k.client, k.hostedCluster.Spec.InfraID, creds, localInfraNS, k.hostedCluster.Namespace)
}
