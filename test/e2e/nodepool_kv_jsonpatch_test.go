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
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
)

type KubeVirtJsonPatchTest struct {
	ctx           context.Context
	client        crclient.Client
	hostedCluster *hyperv1.HostedCluster
}

func NewKubeKubeVirtJsonPatchTest(ctx context.Context, cl crclient.Client, hc *hyperv1.HostedCluster) *KubeVirtJsonPatchTest {
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

	t.Log("Starting test KubeKubeVirtJsonPatchTest")
}

func (k KubeVirtJsonPatchTest) Run(t *testing.T, nodePool hyperv1.NodePool, _ []corev1.Node) {
	g := NewWithT(t)

	np := &hyperv1.NodePool{}
	g.Eventually(func(gg Gomega) {
		gg.Expect(k.client.Get(k.ctx, util.ObjectKey(&nodePool), np)).Should(Succeed())
		gg.Expect(np.Spec.Platform).ToNot(BeNil())
		gg.Expect(np.Spec.Platform.Type).To(Equal(hyperv1.KubevirtPlatform))
		gg.Expect(np.Spec.Platform.Kubevirt).ToNot(BeNil())
	}).Within(5 * time.Minute).WithPolling(time.Second).Should(Succeed())

	localInfraNS := manifests.HostedControlPlaneNamespace(k.hostedCluster.Namespace, k.hostedCluster.Name).Name
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

		gg.Expect(vmi.Spec.Domain.CPU).ToNot(BeNil())
		gg.Expect(vmi.Spec.Domain.CPU.Cores).To(Equal(uint32(3)))
	}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
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

	nodePool.Spec.Replicas = ptr.To(int32(1))

	return nodePool, nil
}
