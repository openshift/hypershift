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
	"sigs.k8s.io/cluster-api/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
)

type KubeVirtJsonPatchTest struct {
	DummyInfraSetup
	ctx           context.Context
	client        crclient.Client
	hostedCluster *hyperv1.HostedCluster
}

func NewKubeVirtJsonPatchTest(ctx context.Context, cl crclient.Client, hc *hyperv1.HostedCluster) *KubeVirtJsonPatchTest {
	return &KubeVirtJsonPatchTest{
		ctx:           ctx,
		client:        cl,
		hostedCluster: hc,
	}
}

func (k KubeVirtJsonPatchTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.KubevirtPlatform {
		t.Skip("test only supported on platform KubeVirt")
	}

	t.Log("Starting test KubeVirtJsonPatchTest")
}

func (k KubeVirtJsonPatchTest) Run(t *testing.T, nodePool hyperv1.NodePool, _ []corev1.Node) {
	g := NewWithT(t)

	np := &hyperv1.NodePool{}
	e2eutil.EventuallyObject(
		t, k.ctx, fmt.Sprintf("waiting for NodePool %s/%s to have a kubevirt platform", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			err := k.client.Get(k.ctx, util.ObjectKey(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(pool *hyperv1.NodePool) (done bool, reasons string, err error) {
				if np.Spec.Platform.Kubevirt != nil && np.Spec.Platform.Type == hyperv1.KubevirtPlatform {
					return true, "", nil
				}
				return false, fmt.Sprintf("invalid platform type, wanted %s, got %s", hyperv1.KubevirtPlatform, np.Spec.Platform.Type), nil
			},
		},
	)

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

	labelSelector := labels.SelectorFromValidatedSet(labels.Set{hyperv1.NodePoolNameLabel: np.Name})
	e2eutil.EventuallyObjects(
		t, k.ctx, fmt.Sprintf("waiting for one VirtualMachineInstance with 3 CPU cores"),
		func(ctx context.Context) ([]*kubevirtv1.VirtualMachineInstance, error) {
			vmis := &kubevirtv1.VirtualMachineInstanceList{}
			err := infraClient.GetInfraClient().List(k.ctx, vmis, &crclient.ListOptions{Namespace: guestNamespace, LabelSelector: labelSelector})
			var ptrs []*kubevirtv1.VirtualMachineInstance
			for i := range vmis.Items {
				ptrs = append(ptrs, &vmis.Items[i])
			}
			return ptrs, err
		},
		[]e2eutil.Predicate[[]*kubevirtv1.VirtualMachineInstance]{
			func(items []*kubevirtv1.VirtualMachineInstance) (done bool, reasons string, err error) {
				return len(items) == 1, fmt.Sprintf("wanted one VirtualMachineInstance, got %d", len(items)), nil
			},
		},
		[]e2eutil.Predicate[*kubevirtv1.VirtualMachineInstance]{
			func(instance *kubevirtv1.VirtualMachineInstance) (done bool, reasons string, err error) {
				cores := ptr.Deref(instance.Spec.Domain.CPU, kubevirtv1.CPU{}).Cores
				return cores == uint32(3), fmt.Sprintf("wanted 3 CPU cores, got %d", cores), nil
			},
		},
	)
}

func (k KubeVirtJsonPatchTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.hostedCluster.Name + "-" + "test-kv-json-patch",
			Namespace: k.hostedCluster.Namespace,
			Annotations: map[string]string{
				hyperv1.JSONPatchAnnotation: `[{"op": "replace","path": "/spec/template/spec/domain/cpu/cores","value": 3}]`,
			},
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = ptr.To[int32](1)

	return nodePool, nil
}
