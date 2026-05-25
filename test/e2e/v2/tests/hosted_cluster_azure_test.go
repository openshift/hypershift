//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/netutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// AzurePublicClusterTest registers tests for Azure public cluster validation.
// These tests verify workload identity, KAS allowed CIDRs, and ingress operator configuration
// on Azure platform clusters.
func AzurePublicClusterTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AzureWorkloadIdentity] Azure Public Cluster", Label("Azure", "self-managed-azure-public"), func() {
		BeforeEach(func() {
			testCtx := getTestCtx()
			hc := testCtx.GetHostedCluster()
			if hc == nil || hc.Spec.Platform.Type != hyperv1.AzurePlatform {
				Skip("Azure public cluster tests are only for Azure platform")
			}
		})

		It("should mutate pods with workload identity federated credentials", func() {
			e2eutil.GinkgoAtLeast(e2eutil.Version422)
			testCtx := getTestCtx()
			hc := testCtx.GetHostedCluster()
			e2eutil.WaitForGuestKubeConfig(GinkgoTB(), testCtx.Context, testCtx.MgmtClient, hc)
			hostedClusterClient := testCtx.GetHostedClusterClient()
			Expect(hostedClusterClient).NotTo(BeNil(), "hosted cluster client is nil; HostedCluster may not have KubeConfig status set")

			e2eutil.ValidateAzureWorkloadIdentityWebhookMutation(GinkgoTB(), testCtx.Context, hostedClusterClient)
		})

		It("should have expected KAS allowed CIDRs", func() {
			testCtx := getTestCtx()
			hc := testCtx.GetHostedCluster()
			kubeconfigData := e2eutil.WaitForGuestKubeConfig(GinkgoTB(), testCtx.Context, testCtx.MgmtClient, hc)
			restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
			Expect(err).NotTo(HaveOccurred(), "failed to create hosted cluster REST config")

			e2eutil.ValidateKubeAPIServerAllowedCIDRs(GinkgoTB(), testCtx.Context, testCtx.MgmtClient, restConfig, hc)
		})

		It("should have Ingress Operator configuration applied", func() {
			e2eutil.GinkgoAtLeast(e2eutil.Version421)
			testCtx := getTestCtx()
			hc := testCtx.GetHostedCluster()
			e2eutil.WaitForGuestKubeConfig(GinkgoTB(), testCtx.Context, testCtx.MgmtClient, hc)
			hostedClusterClient := testCtx.GetHostedClusterClient()
			Expect(hostedClusterClient).NotTo(BeNil(), "hosted cluster client is nil; HostedCluster may not have KubeConfig status set")

			e2eutil.ValidateIngressOperatorConfiguration(GinkgoTB(), testCtx.Context, hostedClusterClient, hc)
		})
	})
}

// AzurePrivateTopologyTest registers tests for Azure private cluster topology validation.
// These tests verify private-router Service annotation, PrivateLinkService CRs, and DNS zone configuration
// on Azure clusters with Private topology.
func AzurePrivateTopologyTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AzurePrivateLink] Azure Private Topology", Label("Azure", "self-managed-azure-private"), Ordered, func() {
		var testCtx *internal.TestContext
		var controlPlaneNamespace string

		BeforeAll(func() {
			testCtx = getTestCtx()
			hc := testCtx.GetHostedCluster()
			if hc == nil || hc.Spec.Platform.Type != hyperv1.AzurePlatform {
				Skip("Azure private topology tests are only for Azure platform")
			}
			if hc.Spec.Platform.Azure == nil || hc.Spec.Platform.Azure.Topology != hyperv1.AzureTopologyPrivate {
				Skip("Azure private topology tests require Private topology")
			}

			controlPlaneNamespace = testCtx.ControlPlaneNamespace
			Expect(controlPlaneNamespace).NotTo(BeEmpty(), "control plane namespace must be set")
		})

		It("should have Azure internal LB annotation on private-router Service", func() {
			ctx := testCtx.Context
			e2eutil.EventuallyObject(GinkgoTB(), ctx, "private-router Service has Azure internal LB annotation",
				func(ctx context.Context) (*corev1.Service, error) {
					svc := &corev1.Service{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: controlPlaneNamespace,
							Name:      "private-router",
						},
					}
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(svc), svc)
					return svc, err
				},
				[]e2eutil.Predicate[*corev1.Service]{
					func(svc *corev1.Service) (done bool, reasons string, err error) {
						val, ok := svc.Annotations[azureutil.InternalLoadBalancerAnnotation]
						if !ok || val != azureutil.InternalLoadBalancerValue {
							return false, fmt.Sprintf("expected annotation %q to be %q, got %q (present: %v)",
								azureutil.InternalLoadBalancerAnnotation, azureutil.InternalLoadBalancerValue, val, ok), nil
						}
						return true, "private-router Service has internal LB annotation", nil
					},
				},
				e2eutil.WithTimeout(10*time.Minute),
			)
		})

		It("should create AzurePrivateLinkService CR with PLS alias", func() {
			ctx := testCtx.Context
			e2eutil.EventuallyObjects(GinkgoTB(), ctx, "AzurePrivateLinkService CR is created with PLS alias",
				func(ctx context.Context) ([]*hyperv1.AzurePrivateLinkService, error) {
					return listPLS(ctx, testCtx.MgmtClient, controlPlaneNamespace)
				},
				[]e2eutil.Predicate[[]*hyperv1.AzurePrivateLinkService]{
					func(items []*hyperv1.AzurePrivateLinkService) (done bool, reasons string, err error) {
						if len(items) == 0 {
							return false, "no AzurePrivateLinkService CRs found in HCP namespace", nil
						}
						for _, pls := range items {
							if pls.Status.PrivateLinkServiceAlias != "" {
								return true, fmt.Sprintf("PLS alias is %q", pls.Status.PrivateLinkServiceAlias), nil
							}
						}
						return false, "no AzurePrivateLinkService has a PLS alias yet", nil
					},
				},
				nil,
				e2eutil.WithTimeout(15*time.Minute),
				e2eutil.WithInterval(15*time.Second),
			)
		})

		It("should populate Private Endpoint IP in PLS status", func() {
			ctx := testCtx.Context
			e2eutil.EventuallyObjects(GinkgoTB(), ctx, "AzurePrivateLinkService has Private Endpoint IP",
				func(ctx context.Context) ([]*hyperv1.AzurePrivateLinkService, error) {
					return listPLS(ctx, testCtx.MgmtClient, controlPlaneNamespace)
				},
				[]e2eutil.Predicate[[]*hyperv1.AzurePrivateLinkService]{
					func(items []*hyperv1.AzurePrivateLinkService) (done bool, reasons string, err error) {
						if len(items) == 0 {
							return false, "no AzurePrivateLinkService CRs found", nil
						}
						for _, pls := range items {
							if pls.Status.PrivateEndpointIP != "" {
								return true, fmt.Sprintf("Private Endpoint IP is %q", pls.Status.PrivateEndpointIP), nil
							}
						}
						return false, "no AzurePrivateLinkService has a Private Endpoint IP yet", nil
					},
				},
				nil,
				e2eutil.WithTimeout(15*time.Minute),
				e2eutil.WithInterval(15*time.Second),
			)
		})

		It("should populate Private DNS Zone ID in PLS status", func() {
			ctx := testCtx.Context
			e2eutil.EventuallyObjects(GinkgoTB(), ctx, "AzurePrivateLinkService has Private DNS Zone ID",
				func(ctx context.Context) ([]*hyperv1.AzurePrivateLinkService, error) {
					return listPLS(ctx, testCtx.MgmtClient, controlPlaneNamespace)
				},
				[]e2eutil.Predicate[[]*hyperv1.AzurePrivateLinkService]{
					func(items []*hyperv1.AzurePrivateLinkService) (done bool, reasons string, err error) {
						if len(items) == 0 {
							return false, "no AzurePrivateLinkService CRs found", nil
						}
						for _, pls := range items {
							if pls.Status.PrivateDNSZoneID != "" {
								return true, fmt.Sprintf("Private DNS Zone ID is %q", pls.Status.PrivateDNSZoneID), nil
							}
						}
						return false, "no AzurePrivateLinkService has a Private DNS Zone ID yet", nil
					},
				},
				nil,
				e2eutil.WithTimeout(15*time.Minute),
				e2eutil.WithInterval(15*time.Second),
			)
		})
	})
}

// listPLS returns all AzurePrivateLinkService CRs in the given namespace.
func listPLS(ctx context.Context, client crclient.Client, namespace string) ([]*hyperv1.AzurePrivateLinkService, error) {
	plsList := &hyperv1.AzurePrivateLinkServiceList{}
	if err := client.List(ctx, plsList, crclient.InNamespace(namespace)); err != nil {
		return nil, err
	}
	items := make([]*hyperv1.AzurePrivateLinkService, len(plsList.Items))
	for i := range plsList.Items {
		items[i] = &plsList.Items[i]
	}
	return items, nil
}

// AzureOAuthLoadBalancerTest registers tests for Azure OAuth LoadBalancer publishing validation.
// These tests verify that OAuth is properly exposed via a LoadBalancer Service and that the
// OAuth token flow works through that endpoint.
func AzureOAuthLoadBalancerTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AzureOAuth] Azure OAuth LoadBalancer", Label("Azure", "self-managed-azure-oauth-lb"), func() {
		BeforeEach(func() {
			testCtx := getTestCtx()
			hc := testCtx.GetHostedCluster()
			if hc == nil || hc.Spec.Platform.Type != hyperv1.AzurePlatform {
				Skip("Azure OAuth LB tests are only for Azure platform")
			}
			strategy := netutil.ServicePublishingStrategyByTypeByHC(hc, hyperv1.OAuthServer)
			if strategy == nil || strategy.Type != hyperv1.LoadBalancer {
				Skip("Azure OAuth LB tests require OAuthServer with LoadBalancer publishing strategy")
			}
		})

		It("should create oauth-openshift Service as LoadBalancer with external IP", func() {
			testCtx := getTestCtx()
			ctx := testCtx.Context
			controlPlaneNamespace := testCtx.ControlPlaneNamespace

			e2eutil.EventuallyObject(GinkgoTB(), ctx, "oauth-openshift Service is LoadBalancer with external IP",
				func(ctx context.Context) (*corev1.Service, error) {
					svc := hcpmanifests.OauthServerService(controlPlaneNamespace)
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(svc), svc)
					return svc, err
				},
				[]e2eutil.Predicate[*corev1.Service]{
					func(svc *corev1.Service) (done bool, reasons string, err error) {
						if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
							return false, fmt.Sprintf("expected Service type LoadBalancer, got %s", svc.Spec.Type), nil
						}
						return true, "oauth-openshift Service is type LoadBalancer", nil
					},
					func(svc *corev1.Service) (done bool, reasons string, err error) {
						if len(svc.Status.LoadBalancer.Ingress) == 0 {
							return false, "LoadBalancer has no ingress entries yet", nil
						}
						ingress := svc.Status.LoadBalancer.Ingress[0]
						if ingress.IP == "" && ingress.Hostname == "" {
							return false, "LoadBalancer ingress has no IP or hostname", nil
						}
						host := ingress.IP
						if host == "" {
							host = ingress.Hostname
						}
						return true, fmt.Sprintf("oauth-openshift LoadBalancer has external endpoint: %s", host), nil
					},
				},
				e2eutil.WithTimeout(10*time.Minute),
			)
		})

		It("should complete OAuth token flow through LoadBalancer endpoint", func() {
			testCtx := getTestCtx()
			hc := testCtx.GetHostedCluster()

			e2eutil.ValidateOAuthWithIdentityProviderViaLoadBalancer(GinkgoTB(), testCtx.Context, testCtx.MgmtClient, hc)
		})
	})
}

// AzureOAuthLoadBalancerPrivateTest registers tests for Azure OAuth LoadBalancer
// publishing validation in a private topology cluster.
// These tests verify that:
//   - The oauth-openshift Service is created as a LoadBalancer with an allocated endpoint
//   - The Service carries the Azure internal LoadBalancer annotation
//   - The OAuth token flow (kubeadmin + htpasswd IDP) works through that endpoint
//
// The OAuth token flow test requires the test runner to have network connectivity
// to the Azure VNet (e.g., running in the management cluster or via VPN/peering).
func AzureOAuthLoadBalancerPrivateTest(getTestCtx internal.TestContextGetter) {
	Context("Azure OAuth LoadBalancer in Private Topology", Label("Azure", "self-managed-azure-oauth-lb-private"), Ordered, func() {
		var testCtx *internal.TestContext
		var controlPlaneNamespace string
		var hc *hyperv1.HostedCluster

		BeforeAll(func() {
			testCtx = getTestCtx()
			hc = testCtx.GetHostedCluster()
			if hc == nil || hc.Spec.Platform.Type != hyperv1.AzurePlatform {
				Skip("Azure OAuth LB Private tests are only for Azure platform")
			}
			if hc.Spec.Platform.Azure == nil || hc.Spec.Platform.Azure.Topology != hyperv1.AzureTopologyPrivate {
				Skip("Azure OAuth LB Private tests require Private topology")
			}
			controlPlaneNamespace = testCtx.ControlPlaneNamespace
			Expect(controlPlaneNamespace).NotTo(BeEmpty(), "control plane namespace must be set")

			strategy := netutil.ServicePublishingStrategyByTypeByHC(hc, hyperv1.OAuthServer)
			if strategy == nil || strategy.Type != hyperv1.LoadBalancer {
				Skip("Azure OAuth LB Private tests require OAuthServer with LoadBalancer publishing strategy")
			}
		})

		It("should create oauth-openshift Service as LoadBalancer with an allocated endpoint", func() {
			ctx := testCtx.Context

			e2eutil.EventuallyObject(GinkgoTB(), ctx, "oauth-openshift Service is LoadBalancer with an allocated endpoint",
				func(ctx context.Context) (*corev1.Service, error) {
					svc := hcpmanifests.OauthServerService(controlPlaneNamespace)
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(svc), svc)
					return svc, err
				},
				[]e2eutil.Predicate[*corev1.Service]{
					func(svc *corev1.Service) (done bool, reasons string, err error) {
						if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
							return false, fmt.Sprintf("expected Service type LoadBalancer, got %s", svc.Spec.Type), nil
						}
						return true, "oauth-openshift Service is type LoadBalancer", nil
					},
					func(svc *corev1.Service) (done bool, reasons string, err error) {
						if len(svc.Status.LoadBalancer.Ingress) == 0 {
							return false, "LoadBalancer has no ingress entries yet", nil
						}
						ingress := svc.Status.LoadBalancer.Ingress[0]
						if ingress.IP == "" && ingress.Hostname == "" {
							return false, "LoadBalancer ingress has no IP or hostname", nil
						}
						host := ingress.IP
						if host == "" {
							host = ingress.Hostname
						}
						return true, fmt.Sprintf("oauth-openshift LoadBalancer has an allocated endpoint: %s", host), nil
					},
				},
				e2eutil.WithTimeout(10*time.Minute),
			)
		})

		It("should have Azure internal LB annotation on oauth-openshift Service", func() {
			ctx := testCtx.Context
			e2eutil.EventuallyObject(GinkgoTB(), ctx, "oauth-openshift Service has Azure internal LB annotation",
				func(ctx context.Context) (*corev1.Service, error) {
					svc := hcpmanifests.OauthServerService(controlPlaneNamespace)
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(svc), svc)
					return svc, err
				},
				[]e2eutil.Predicate[*corev1.Service]{
					func(svc *corev1.Service) (done bool, reasons string, err error) {
						val, ok := svc.Annotations[azureutil.InternalLoadBalancerAnnotation]
						if !ok || val != azureutil.InternalLoadBalancerValue {
							return false, fmt.Sprintf("expected annotation %q to be %q, got %q (present: %v)",
								azureutil.InternalLoadBalancerAnnotation, azureutil.InternalLoadBalancerValue, val, ok), nil
						}
						return true, "oauth-openshift Service has internal LB annotation", nil
					},
				},
				e2eutil.WithTimeout(10*time.Minute),
			)
		})

		It("should complete OAuth token flow through LoadBalancer endpoint", func() {
			ctx := testCtx.Context
			e2eutil.ValidateOAuthWithIdentityProviderViaLoadBalancer(GinkgoTB(), ctx, testCtx.MgmtClient, hc)
		})
	})
}

// RegisterHostedClusterAzureTests registers all Azure-specific hosted cluster tests.
func RegisterHostedClusterAzureTests(getTestCtx internal.TestContextGetter) {
	AzurePublicClusterTest(getTestCtx)
	AzurePrivateTopologyTest(getTestCtx)
	AzureOAuthLoadBalancerTest(getTestCtx)
	AzureOAuthLoadBalancerPrivateTest(getTestCtx)
}

var _ = Describe("[sig-hypershift][Jira:Hypershift] Hosted Cluster Azure", Label("hosted-cluster-azure"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")
	})

	RegisterHostedClusterAzureTests(func() *internal.TestContext { return testCtx })
})
