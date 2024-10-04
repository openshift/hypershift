//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
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

type KubeVirtQoSClassGuaranteedTest struct {
	DummyInfraSetup
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
	e2eutil.EventuallyObject(t, k.ctx, "NodePool to have QoS class configured",
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			err := k.client.Get(ctx, util.ObjectKey(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(nodePool *hyperv1.NodePool) (done bool, reasons string, err error) {
				want, got := hyperv1.KubevirtPlatform, nodePool.Spec.Platform.Type
				return want == got, fmt.Sprintf("expected NodePool to have platform %s, got %s", want, got), nil
			},
			func(pool *hyperv1.NodePool) (done bool, reasons string, err error) {
				diff := cmp.Diff(ptr.To(hyperv1.QoSClassGuaranteed), ptr.Deref(ptr.Deref(np.Spec.Platform.Kubevirt, hyperv1.KubevirtNodePoolPlatform{}).Compute, hyperv1.KubevirtCompute{}).QosClass)
				return diff == "", fmt.Sprintf("incorrect QoS class: %v", diff), nil
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

	e2eutil.EventuallyObjects(t, k.ctx, "one VMI to exist with the correct domain resources",
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
				if vmi.Spec.Domain.Resources.Requests.Cpu() == nil ||
					vmi.Spec.Domain.Resources.Requests.Memory() == nil ||
					vmi.Spec.Domain.Resources.Limits.Cpu() == nil ||
					vmi.Spec.Domain.Resources.Limits.Memory() == nil {
					return false, fmt.Sprintf("expected non-nil domain requests and limits, got %#v", vmi.Spec.Domain.Resources), nil
				}
				if diff := cmp.Diff(vmi.Spec.Domain.Resources.Requests.Cpu(), vmi.Spec.Domain.Resources.Limits.Cpu()); diff != "" {
					return false, fmt.Sprintf("domain CPU requests don't match CPU limits: %s", diff), nil
				}
				if diff := cmp.Diff(vmi.Spec.Domain.Resources.Requests.Memory(), vmi.Spec.Domain.Resources.Limits.Memory()); diff != "" {
					return false, fmt.Sprintf("domain memory requests don't match memory limits: %s", diff), nil
				}
				return true, "domain requests and limits are non-nil and match each other", nil
			},
		},
	)

	e2eutil.EventuallyObjects(t, k.ctx, "one Pod to be running with the correct QoS class",
		func(ctx context.Context) ([]*corev1.Pod, error) {
			list := &corev1.PodList{}
			err := infraClient.GetInfraClient().List(ctx, list, &crclient.ListOptions{Namespace: guestNamespace, LabelSelector: labels.SelectorFromValidatedSet(labels.Set{hyperv1.NodePoolNameLabel: nodePool.Name})})
			pods := make([]*corev1.Pod, len(list.Items))
			for i := range list.Items {
				pods[i] = &list.Items[i]
			}
			return pods, err
		},
		[]e2eutil.Predicate[[]*corev1.Pod]{
			func(pods []*corev1.Pod) (done bool, reasons string, err error) {
				return len(pods) == 1, fmt.Sprintf("expected %d Pods, got %d", 1, len(pods)), nil
			},
		},
		[]e2eutil.Predicate[*corev1.Pod]{
			func(pod *corev1.Pod) (done bool, reasons string, err error) {
				if pod.Status.Phase != corev1.PodRunning {
					return false, fmt.Sprintf("expected pod to have status %s, got %s", corev1.PodRunning, pod.Status.Phase), nil
				}

				want, got := corev1.PodQOSGuaranteed, pod.Status.QOSClass
				return want == got, fmt.Sprintf("expected pod to have QoS class %s, got %s", want, got), nil
			},
		},
	)
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

	nodePool.Spec.Replicas = ptr.To[int32](1)

	return nodePool, nil
}
