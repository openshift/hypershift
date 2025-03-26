//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	kubevirtv1 "kubevirt.io/api/core/v1"
	capkv1alpha1 "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	clusterapiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

type KubeVirtAdvancedMultinetTest struct {
	infra        e2eutil.KubeVirtInfra
	ifaceName    string
	nodePoolName string
}

func NewKubeVirtAdvancedMultinetTest(ctx context.Context, mgmtClient crclient.Client, hc *hyperv1.HostedCluster) NodePoolTest {
	return KubeVirtAdvancedMultinetTest{
		infra:        e2eutil.NewKubeVirtInfra(ctx, mgmtClient, hc),
		ifaceName:    "net1",
		nodePoolName: hc.Name + "-" + "test-kv-advance-multinet",
	}
}

func (k KubeVirtAdvancedMultinetTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.KubevirtPlatform {
		t.Skip("test only supported on KubeVirt platform")
	}
	if e2eutil.IsLessThan(e2eutil.Version415) {
		t.Skip("test only supported from version 4.15")
	}

	t.Log("Starting test KubeVirtAdvancedMultinetTest")
}

func (k KubeVirtAdvancedMultinetTest) Run(t *testing.T, nodePool hyperv1.NodePool, _ []corev1.Node) {
	g := NewWithT(t)

	np := &hyperv1.NodePool{}
	g.Expect(k.infra.MGMTClient().Get(k.infra.Ctx(), util.ObjectKey(&nodePool), np)).Should(Succeed())
	g.Expect(np.Spec.Platform).ToNot(BeNil())
	g.Expect(np.Spec.Platform.Type).To(Equal(hyperv1.KubevirtPlatform))
	g.Expect(np.Spec.Platform.Kubevirt).ToNot(BeNil())
	g.Expect(np.Spec.Platform.Kubevirt.AdditionalNetworks).To(Equal([]hyperv1.KubevirtNetwork{{
		Name: "default/" + k.infra.NADName(),
	}}))
	g.Expect(np.Spec.Platform.Kubevirt.AttachDefaultNetwork).ToNot(BeNil())
	g.Expect(*np.Spec.Platform.Kubevirt.AttachDefaultNetwork).To(BeFalse())

	infraClient, err := k.infra.DiscoverClient()
	g.Expect(err).ShouldNot(HaveOccurred())

	vmis := &kubevirtv1.VirtualMachineInstanceList{}
	labelSelector := labels.SelectorFromValidatedSet(labels.Set{hyperv1.NodePoolNameLabel: np.Name})
	g.Expect(infraClient.List(k.infra.Ctx(), vmis, &crclient.ListOptions{Namespace: k.infra.Namespace(), LabelSelector: labelSelector})).To(Succeed())

	g.Expect(vmis.Items).To(HaveLen(1))
	vmi := vmis.Items[0]
	// Use gomega HaveField so we can skip "Mac" matching
	matchingInterface := &kubevirtv1.Interface{}
	expectedNetworkName := "iface1_default-" + k.infra.NADName()
	g.Expect(vmi.Spec.Domain.Devices.Interfaces).To(ContainElement(
		HaveField("Name", expectedNetworkName), matchingInterface),
	)
	g.Expect(matchingInterface.InterfaceBindingMethod.Bridge).ToNot(BeNil())
	g.Expect(vmi.Spec.Networks).To(ContainElement(kubevirtv1.Network{
		Name: expectedNetworkName,
		NetworkSource: kubevirtv1.NetworkSource{
			Multus: &kubevirtv1.MultusNetwork{
				NetworkName: "default/" + k.infra.NADName(),
			},
		},
	}))
}

func (k KubeVirtAdvancedMultinetTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.nodePoolName,
			Namespace: k.infra.HostedCluster().Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	if nodePool.Spec.Platform.Kubevirt != nil {
		nodePool.Spec.Platform.Kubevirt.AdditionalNetworks = []hyperv1.KubevirtNetwork{{
			Name: "default/" + k.infra.NADName(),
		}}
		nodePool.Spec.Platform.Kubevirt.AttachDefaultNetwork = ptr.To(false)
	}
	nodePool.Spec.Replicas = ptr.To[int32](1)
	return nodePool, nil
}

func (k KubeVirtAdvancedMultinetTest) SetupInfra(t *testing.T) error {
	if err := k.infra.CreateOVNKLayer2NAD("default"); err != nil {
		return err
	}

	infraClient, err := k.infra.DiscoverClient()
	if err != nil {
		return err
	}

	t.Log("Creating a proxy dnsmasq pod")
	dnsmasqPod := k.composeDNSMasqPod(t)
	if err := infraClient.Create(k.infra.Ctx(), dnsmasqPod); err != nil {
		return fmt.Errorf("failed creating dnsmasq pod: %w", err)
	}

	passthroughService := hcpmanifests.IngressDefaultIngressPassthroughService(k.infra.Namespace())
	passthroughService.Name = fmt.Sprintf("%s-%s",
		hcpmanifests.IngressDefaultIngressPassthroughServiceName,
		k.infra.HostedCluster().Spec.Platform.Kubevirt.GenerateID)

	e2eutil.EventuallyObject(t, k.infra.Ctx(), "the default ingresss service to appear",
		func(ctx context.Context) (*corev1.Service, error) {
			err := infraClient.Get(ctx, client.ObjectKeyFromObject(passthroughService), passthroughService)
			return passthroughService, err
		},
		nil, // no predicate necessary, getter will stop returning not-found error when we're done
	)

	e2eutil.EventuallyObject(t, k.infra.Ctx(), "dnsmasq pod to have an address",
		func(ctx context.Context) (*corev1.Pod, error) {
			err := infraClient.Get(ctx, client.ObjectKeyFromObject(dnsmasqPod), dnsmasqPod)
			return dnsmasqPod, err
		},
		[]e2eutil.Predicate[*corev1.Pod]{
			func(pod *corev1.Pod) (done bool, reasons string, err error) {
				if pod.Status.PodIP != "" {
					return true, "pod had a PodIP", nil
				}
				return false, "pod had no PodIP", nil
			},
		},
	)

	ports := []discoveryv1.EndpointPort{}
	for _, port := range passthroughService.Spec.Ports {
		ports = append(ports, discoveryv1.EndpointPort{
			Name:     &port.Name,
			Protocol: &port.Protocol,
			Port:     ptr.To(int32(port.TargetPort.IntValue())),
		})
	}
	eps := discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: k.infra.Namespace(),
			Name:      "test-e2e-nodepool-kv-advanced-multinet-default-ingress-passthrough-dnsmasq-ipv4",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: passthroughService.Name,
			},
		},
		AddressType: discoveryv1.AddressType(corev1.IPv4Protocol),
		Ports:       ports,
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{dnsmasqPod.Status.PodIP},
			Conditions: discoveryv1.EndpointConditions{
				Ready:       ptr.To(true),
				Serving:     ptr.To(true),
				Terminating: ptr.To(false),
			},
		}},
	}
	t.Logf("Creating custom default ingress passthrough endpointslice")
	if err := infraClient.Create(k.infra.Ctx(), &eps); err != nil {
		return fmt.Errorf("failed creating custom default ingress passthrough endpointslice: %w", err)
	}

	t.Logf("Waiting for kubevirt machine to report an address...")
	machineAddress := ""
	findMachineAddressRetryConfig := wait.Backoff{
		Steps:    240,
		Duration: 2 * time.Second,
	}
	allErrors := func(error) bool { return true }
	if err := retry.OnError(findMachineAddressRetryConfig, allErrors, func() error {
		var err error
		machineAddress, err = k.firstMachineAddress()
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	if len(passthroughService.Spec.Ports) == 0 {
		return fmt.Errorf("missing passthrough service port")
	}

	output, err := k.configureDNAT(t, dnsmasqPod.Status.PodIP, machineAddress)
	if err != nil {
		return fmt.Errorf("failed configuring nat at dnmasq proxy pod: %w: %s", err, string(output))
	}
	return nil
}

// TeardownInfra delete dnsmasq and nad that are deployed at default namespace
// to be privileged
func (k KubeVirtAdvancedMultinetTest) TeardownInfra(t *testing.T) error {
	infraClient, err := k.infra.DiscoverClient()
	if err != nil {
		return nil
	}

	errs := []error{}

	nad, err := k.infra.ComposeOVNKLayer2NAD("default")
	if err != nil {
		errs = append(errs, err)
	} else if err := infraClient.Delete(k.infra.Ctx(), nad); err != nil {
		errs = append(errs, err)
	}

	if err := infraClient.Delete(k.infra.Ctx(), k.composeDNSMasqPod(t)); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func (k KubeVirtAdvancedMultinetTest) configureDNAT(t *testing.T, dnsmasqPodAddress, machineAddress string) (string, error) {
	dnsmasqPod := k.composeDNSMasqPod(t)
	command := fmt.Sprintf(`
apk update
apk add iptables
iptables -t nat -A PREROUTING -p tcp -d %[1]s -j DNAT --to-destination %[2]s
`, dnsmasqPodAddress, machineAddress)
	infraClient, err := k.infra.DiscoverClient()
	if err != nil {
		return "", err
	}
	return e2eutil.RunCommandInPod(k.infra.Ctx(), infraClient, dnsmasqPod.Name, dnsmasqPod.Namespace, []string{"/bin/sh", "-c", command}, dnsmasqPod.Spec.Containers[0].Name, 20*time.Minute)
}

func (k KubeVirtAdvancedMultinetTest) firstMachineAddress() (string, error) {
	machineList := capkv1alpha1.KubevirtMachineList{}
	namespace := manifests.HostedControlPlaneNamespace(k.infra.HostedCluster().Namespace, k.infra.HostedCluster().Name)
	if err := k.infra.MGMTClient().List(k.infra.Ctx(), &machineList, client.InNamespace(namespace),
		client.MatchingLabels{
			clusterapiv1beta1.MachineDeploymentNameLabel: k.nodePoolName,
		}); err != nil {
		return "", err
	}
	if len(machineList.Items) == 0 {
		return "", fmt.Errorf("first kubevirt machine not found")
	}

	internalAddress := ""
	for _, address := range machineList.Items[0].Status.Addresses {
		if address.Type == "InternalIP" { //TODO use constant
			internalAddress = address.Address
		}
	}
	if internalAddress == "" {
		return "", fmt.Errorf("missing internal address at kubevirt machine")
	}
	return internalAddress, nil
}

func (k KubeVirtAdvancedMultinetTest) composeDNSMasqPod(t *testing.T) *corev1.Pod {
	g := NewWithT(t)
	podName := k.infra.NADName() + "-dnsmasq"
	networksJSON, err := json.Marshal([]struct {
		Interface string `json:"interface"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	}{{
		Interface: k.ifaceName,
		Namespace: "default",
		Name:      k.infra.NADName(),
	}})
	g.Expect(err).ToNot(HaveOccurred())

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      podName,
			Annotations: map[string]string{
				"k8s.v1.cni.cncf.io/networks": string(networksJSON),
			},
			Labels: map[string]string{
				"name": podName,
				"app":  podName,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Image: "registry.access.redhat.com/ubi9/ubi:latest",
					Name:  "dnsmasq",
					Command: []string{
						"bin/sh",
						"-c",
						fmt.Sprintf(`set -xe
dnf install -y iptables dnsmasq procps-ng
ip a add 192.168.66.1/24 dev %[1]s
echo "net.ipv4.ip_forward=1" | tee -a /etc/sysctl.conf
sysctl -p
iptables -A FORWARD -i %[1]s -j ACCEPT
iptables -t nat -A POSTROUTING -j MASQUERADE
dnsmasq -d --interface=%[1]s --dhcp-option=option:router,192.168.66.1 --dhcp-range=192.168.66.3,192.168.66.200,infinite
`, k.ifaceName),
					},
					ImagePullPolicy: corev1.PullIfNotPresent,
					SecurityContext: &corev1.SecurityContext{
						Privileged: ptr.To(true),
					},
				},
			},
		},
	}
}
