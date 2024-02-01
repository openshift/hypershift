//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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

type KubeVirtQoSClassGuaranteedTest struct {
	ctx           context.Context
	client        crclient.Client
	hostedCluster *hyperv1.HostedCluster
}

func NewKubeVirtQoSClassGuaranteedTest(ctx context.Context, cl crclient.Client, hc *hyperv1.HostedCluster) *KubeVirtQoSClassGuaranteedTest {
	return &KubeVirtQoSClassGuaranteedTest{
		ctx:           ctx,
		client:        cl,
		hostedCluster: hc,
	}
}

func (k KubeVirtQoSClassGuaranteedTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.KubevirtPlatform {
		t.Skip("test only supported on platform KubeVirt")
	}

	t.Log("Starting test KubeVirtQoSClassGuaranteedTest")
}

func (k KubeVirtQoSClassGuaranteedTest) Run(t *testing.T, nodePool hyperv1.NodePool, _ []corev1.Node) {
	g := NewWithT(t)

	np := &hyperv1.NodePool{}
	g.Eventually(func(gg Gomega) {
		gg.Expect(k.client.Get(k.ctx, util.ObjectKey(&nodePool), np)).Should(Succeed())
		gg.Expect(np.Spec.Platform).ToNot(BeNil())
		gg.Expect(np.Spec.Platform.Type).To(Equal(hyperv1.KubevirtPlatform))
		gg.Expect(np.Spec.Platform.Kubevirt).ToNot(BeNil())
		gg.Expect(np.Spec.Platform.Kubevirt.Compute).ToNot(BeNil())
		gg.Expect(np.Spec.Platform.Kubevirt.Compute.QosClass).To(HaveValue(Equal(hyperv1.QoSClassGuaranteed)))
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

		validateQuantity(vmi.Spec.Domain.Resources.Requests.Cpu(), vmi.Spec.Domain.Resources.Limits.Cpu(), gg)
		validateQuantity(vmi.Spec.Domain.Resources.Requests.Memory(), vmi.Spec.Domain.Resources.Limits.Memory(), gg)
	}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

	g.Eventually(func(gg Gomega) corev1.PodQOSClass {
		pods := &corev1.PodList{}
		gg.Expect(
			infraClient.GetInfraClient().List(k.ctx, pods, &crclient.ListOptions{Namespace: guestNamespace, LabelSelector: labelSelector}),
		).To(Succeed())

		gg.Expect(pods.Items).To(HaveLen(1))
		gg.Expect(pods.Items[0].Status.Phase).To(Equal(corev1.PodRunning))

		return pods.Items[0].Status.QOSClass
	}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Equal(corev1.PodQOSGuaranteed))
}

func validateQuantity(req, limit *resource.Quantity, g Gomega) {
	g.ExpectWithOffset(1, req).ToNot(BeNil())
	g.ExpectWithOffset(1, limit).ToNot(BeNil())
	g.ExpectWithOffset(1, *req).To(Equal(*limit))
}

func (k KubeVirtQoSClassGuaranteedTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.hostedCluster.Name + "-" + "test-kv-qos-guaranteed",
			Namespace: k.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	if nodePool.Spec.Platform.Kubevirt != nil &&
		nodePool.Spec.Platform.Kubevirt.Compute != nil {
		nodePool.Spec.Platform.Kubevirt.Compute.QosClass = ptr.To(hyperv1.QoSClassGuaranteed)
	}

	nodePool.Spec.Replicas = ptr.To(int32(1))

	return nodePool, nil
}
