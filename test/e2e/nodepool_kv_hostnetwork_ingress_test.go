//go:build e2e

package e2e

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	operatorv1 "github.com/openshift/api/operator/v1"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// kubevirtHostNetworkIngressHTTPSPort is the host network https port
	// configured at the default ingress controller for the HostNetwork
	// passthrough e2e test. A non default value is used so the test can tell
	// it apart from the passthrough service port.
	kubevirtHostNetworkIngressHTTPSPort int32 = 8443

	// kubevirtIngressPassthroughServicePort is the port exposed by the
	// default ingress passthrough service at the infra cluster. It is always
	// 443 regardless of the endpoint publishing strategy; only the targetPort
	// changes.
	kubevirtIngressPassthroughServicePort int32 = 443

	// endpointSliceManagedByValue is the value used by the HCCO machine
	// controller to label the EndpointSlices it manages.
	endpointSliceManagedByValue = "control-plane-operator.hypershift.openshift.io"
)

// kubevirtHostNetworkIngressBeforeApply returns a BeforeApply hook that
// configures the HostedCluster default ingress controller to use the
// HostNetwork endpoint publishing strategy.
func kubevirtHostNetworkIngressBeforeApply(original func(crclient.Object)) func(crclient.Object) {
	return func(o crclient.Object) {
		if original != nil {
			original(o)
		}
		hc, ok := o.(*hyperv1.HostedCluster)
		if !ok {
			return
		}
		if hc.Spec.OperatorConfiguration == nil {
			hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{}
		}
		if hc.Spec.OperatorConfiguration.IngressOperator == nil {
			hc.Spec.OperatorConfiguration.IngressOperator = &hyperv1.IngressOperatorSpec{}
		}
		hc.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
			Type: operatorv1.HostNetworkStrategyType,
			HostNetwork: &operatorv1.HostNetworkStrategy{
				Protocol:  operatorv1.TCPProtocol,
				HTTPPort:  80,
				HTTPSPort: kubevirtHostNetworkIngressHTTPSPort,
				StatsPort: 1936,
			},
		}
	}
}

// KubeVirtHostNetworkIngressPassthroughTest validates that the default
// ingress passthrough (Service, EndpointSlices and Route at the infra cluster)
// is properly reconciled when the guest default ingress controller uses the
// HostNetwork endpoint publishing strategy.
type KubeVirtHostNetworkIngressPassthroughTest struct {
	DummyInfraSetup
	infra        e2eutil.KubeVirtInfra
	guestClient  crclient.Client
	nodePoolName string
}

func NewKubeVirtHostNetworkIngressPassthroughTest(ctx context.Context, mgmtClient crclient.Client, hc *hyperv1.HostedCluster, guestClient crclient.Client) NodePoolTest {
	return &KubeVirtHostNetworkIngressPassthroughTest{
		infra:        e2eutil.NewKubeVirtInfra(ctx, mgmtClient, hc),
		guestClient:  guestClient,
		nodePoolName: hc.Name + "-" + "test-kv-hostnetwork-ingress",
	}
}

func (k KubeVirtHostNetworkIngressPassthroughTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.KubevirtPlatform {
		t.Skip("test only supported on KubeVirt platform")
	}
	hc := k.infra.HostedCluster()
	if hc.Spec.Platform.Kubevirt == nil || !ptr.Deref(hc.Spec.Platform.Kubevirt.BaseDomainPassthrough, false) {
		t.Skip("test only supported with kubevirt base domain passthrough")
	}
	t.Log("Starting test KubeVirtHostNetworkIngressPassthroughTest")
}

func (k KubeVirtHostNetworkIngressPassthroughTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.nodePoolName,
			Namespace: k.infra.HostedCluster().Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)
	nodePool.Spec.Replicas = ptr.To[int32](1)
	return nodePool, nil
}

func (k KubeVirtHostNetworkIngressPassthroughTest) Run(t *testing.T, nodePool hyperv1.NodePool, _ []corev1.Node) {
	g := NewWithT(t)
	ctx := k.infra.Ctx()
	hc := k.infra.HostedCluster()

	// The guest default ingress controller should report the HostNetwork strategy.
	ingressController := hcpmanifests.IngressDefaultIngressController()
	e2eutil.EventuallyObject(t, ctx, "default ingress controller to use HostNetwork strategy",
		func(ctx context.Context) (*operatorv1.IngressController, error) {
			err := k.guestClient.Get(ctx, crclient.ObjectKeyFromObject(ingressController), ingressController)
			return ingressController, err
		},
		[]e2eutil.Predicate[*operatorv1.IngressController]{
			func(ic *operatorv1.IngressController) (bool, string, error) {
				strategy := ic.Status.EndpointPublishingStrategy
				if strategy == nil {
					return false, "status.endpointPublishingStrategy not set yet", nil
				}
				if strategy.Type != operatorv1.HostNetworkStrategyType {
					return false, fmt.Sprintf("status.endpointPublishingStrategy.type is %q", strategy.Type), nil
				}
				return true, "status.endpointPublishingStrategy.type is HostNetwork", nil
			},
		},
	)

	infraClient, err := k.infra.DiscoverClient()
	g.Expect(err).ShouldNot(HaveOccurred())

	// The passthrough service should target the host network https port.
	passthroughService := hcpmanifests.IngressDefaultIngressPassthroughService(k.infra.Namespace())
	passthroughService.Name = fmt.Sprintf("%s-%s",
		hcpmanifests.IngressDefaultIngressPassthroughServiceName,
		hc.Spec.Platform.Kubevirt.GenerateID)
	e2eutil.EventuallyObject(t, ctx, "default ingress passthrough service to target the HostNetwork https port",
		func(ctx context.Context) (*corev1.Service, error) {
			err := infraClient.Get(ctx, crclient.ObjectKeyFromObject(passthroughService), passthroughService)
			return passthroughService, err
		},
		[]e2eutil.Predicate[*corev1.Service]{
			func(svc *corev1.Service) (bool, string, error) {
				if len(svc.Spec.Selector) != 0 {
					return false, "service has a selector", nil
				}
				if len(svc.Spec.Ports) != 1 {
					return false, fmt.Sprintf("service has %d ports, expected 1", len(svc.Spec.Ports)), nil
				}
				port := svc.Spec.Ports[0]
				if port.Port != kubevirtIngressPassthroughServicePort {
					return false, fmt.Sprintf("service port is %d, expected %d", port.Port, kubevirtIngressPassthroughServicePort), nil
				}
				if int32(port.TargetPort.IntValue()) != kubevirtHostNetworkIngressHTTPSPort {
					return false, fmt.Sprintf("service targetPort is %d, expected %d", port.TargetPort.IntValue(), kubevirtHostNetworkIngressHTTPSPort), nil
				}
				return true, "service targets the HostNetwork https port", nil
			},
		},
	)

	// Every machine of the nodepool should have a ready EndpointSlice
	// with the machine internal IP and the host network https port.
	machines := &capiv1.MachineList{}
	hcpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
	g.Expect(k.infra.MGMTClient().List(ctx, machines, crclient.InNamespace(hcpNamespace),
		crclient.MatchingLabels{capiv1.MachineDeploymentNameLabel: nodePool.Name})).To(Succeed())
	g.Expect(machines.Items).To(HaveLen(1))

	for _, machine := range machines.Items {
		internalIPs := []string{}
		for _, address := range machine.Status.Addresses {
			if address.Type == capiv1.MachineInternalIP {
				internalIPs = append(internalIPs, address.Address)
			}
		}
		g.Expect(internalIPs).ToNot(BeEmpty(), "machine %s should report internal IPs", machine.Name)

		endpointSlice := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: passthroughService.Namespace,
				Name:      passthroughService.Name + "-" + machine.Name + "-ipv4",
			},
		}
		e2eutil.EventuallyObject(t, ctx, fmt.Sprintf("default ingress passthrough endpointslice for machine %s to be ready", machine.Name),
			func(ctx context.Context) (*discoveryv1.EndpointSlice, error) {
				err := infraClient.Get(ctx, crclient.ObjectKeyFromObject(endpointSlice), endpointSlice)
				return endpointSlice, err
			},
			[]e2eutil.Predicate[*discoveryv1.EndpointSlice]{
				func(eps *discoveryv1.EndpointSlice) (bool, string, error) {
					if eps.Labels[discoveryv1.LabelServiceName] != passthroughService.Name {
						return false, "endpointslice is not labeled with the passthrough service name", nil
					}
					if eps.Labels[discoveryv1.LabelManagedBy] != endpointSliceManagedByValue {
						return false, "endpointslice is not managed by the control plane operator", nil
					}
					if len(eps.Ports) != 1 || ptr.Deref(eps.Ports[0].Port, 0) != kubevirtHostNetworkIngressHTTPSPort {
						return false, fmt.Sprintf("endpointslice ports %v do not match the HostNetwork https port", eps.Ports), nil
					}
					if len(eps.Endpoints) != 1 {
						return false, fmt.Sprintf("endpointslice has %d endpoints, expected 1", len(eps.Endpoints)), nil
					}
					endpoint := eps.Endpoints[0]
					if !ptr.Deref(endpoint.Conditions.Ready, false) || !ptr.Deref(endpoint.Conditions.Serving, false) {
						return false, "endpoint is not ready/serving", nil
					}
					for _, address := range endpoint.Addresses {
						for _, internalIP := range internalIPs {
							if address == internalIP {
								return true, fmt.Sprintf("endpoint address %s matches machine internal IP", address), nil
							}
						}
					}
					return false, fmt.Sprintf("endpoint addresses %v do not match machine internal IPs %v", endpoint.Addresses, internalIPs), nil
				},
			},
		)
	}

	// Finally verify traffic flows end to end through the infra router,
	// the passthrough Route/Service and the guest router bound on the host
	// network by completing a TLS handshake against the guest console.
	consoleHost := fmt.Sprintf("console-openshift-console.apps.%s.%s", hc.Name, hc.Spec.DNS.BaseDomain)
	t.Logf("Checking TLS handshake with %s through the default ingress passthrough", consoleHost)
	g.Eventually(func(ctx context.Context) error {
		dialer := &tls.Dialer{
			NetDialer: &net.Dialer{Timeout: 10 * time.Second},
			Config: &tls.Config{
				ServerName:         consoleHost,
				InsecureSkipVerify: true, //nolint:gosec // we only care about reaching the guest router
			},
		}
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(consoleHost, "443"))
		if err != nil {
			return err
		}
		return conn.Close()
	}).WithTimeout(10*time.Minute).WithPolling(15*time.Second).WithContext(ctx).Should(Succeed(),
		"should complete a TLS handshake with the guest console through the default ingress passthrough")
}
