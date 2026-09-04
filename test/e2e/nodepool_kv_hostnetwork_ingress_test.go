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

	// Every machine of the nodepool should have a ready EndpointSlice
	// with the machine internal IP and the host network https port.
	machines := &capiv1.MachineList{}
	hcpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
	g.Expect(k.infra.MGMTClient().List(ctx, machines, crclient.InNamespace(hcpNamespace),
		crclient.MatchingLabels{capiv1.MachineDeploymentNameLabel: nodePool.Name})).To(Succeed())
	g.Expect(machines.Items).To(HaveLen(1))

	g.Eventually(func() error {
		return e2eutil.ValidateKubeVirtIngressPassthrough(ctx, infraClient, k.infra.Namespace(), hc, machines.Items, kubevirtHostNetworkIngressHTTPSPort)
	}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).WithContext(ctx).Should(Succeed())

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
