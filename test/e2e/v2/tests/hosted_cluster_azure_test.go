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
	"net"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	routev1 "github.com/openshift/api/route/v1"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/netutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
			e2eutil.GinkgoAtLeast(e2eutil.Version420)
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

// AzureEndpointAccessTransitionTest validates transitioning a HostedCluster
// between Private and PublicAndPrivate topology on Azure.
// This test MUST be registered after AzurePrivateTopologyTest to ensure
// stateless private-topology assertions run on the pristine Private cluster first.
func AzureEndpointAccessTransitionTest(getTestCtx internal.TestContextGetter) {
	Context("[Feature:AzureEndpointAccess] Azure Endpoint Access Transition", Label("Azure", "self-managed-azure-private"), Ordered, func() {
		var testCtx *internal.TestContext
		var hc *hyperv1.HostedCluster
		var controlPlaneNamespace string

		BeforeAll(func() {
			testCtx = getTestCtx()
			hc = testCtx.GetHostedCluster()
			if hc == nil || hc.Spec.Platform.Type != hyperv1.AzurePlatform {
				Skip("Azure endpoint access transition tests are only for Azure platform")
			}
			if hc.Spec.Platform.Azure == nil || hc.Spec.Platform.Azure.Topology != hyperv1.AzureTopologyPrivate {
				Skip("Azure endpoint access transition tests require Private topology")
			}
			controlPlaneNamespace = testCtx.ControlPlaneNamespace
			Expect(controlPlaneNamespace).NotTo(BeEmpty(), "control plane namespace must be set")

			DeferCleanup(func() {
				// Use context.Background() because DeferCleanup runs after the test completes,
				// when testCtx.Context may already be canceled.
				restoreErr := e2eutil.UpdateObject(GinkgoTB(), context.Background(), testCtx.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
					obj.Spec.Platform.Azure.Topology = hyperv1.AzureTopologyPrivate
				})
				if restoreErr != nil {
					GinkgoTB().Logf("WARNING: failed to restore Private topology: %v", restoreErr)
				}
			})
		})

		It("should transition from Private to PublicAndPrivate", func() {
			ctx := testCtx.Context

			// Verify ExternalPrivateService resources exist in Private topology before transition.
			e2eutil.EventuallyObject(GinkgoTB(), ctx, "KAS ExternalPrivateService exists in Private topology",
				func(ctx context.Context) (*corev1.Service, error) {
					svc := hcpmanifests.KubeAPIServerExternalPrivateService(controlPlaneNamespace)
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(svc), svc)
					return svc, err
				},
				[]e2eutil.Predicate[*corev1.Service]{
					serviceTypePredicate(corev1.ServiceTypeExternalName),
				},
				e2eutil.WithTimeout(2*time.Minute),
			)

			e2eutil.EventuallyObject(GinkgoTB(), ctx, "OAuth ExternalPrivateService exists in Private topology",
				func(ctx context.Context) (*corev1.Service, error) {
					svc := hcpmanifests.OauthServerExternalPrivateService(controlPlaneNamespace)
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(svc), svc)
					return svc, err
				},
				[]e2eutil.Predicate[*corev1.Service]{
					serviceTypePredicate(corev1.ServiceTypeExternalName),
				},
				e2eutil.WithTimeout(2*time.Minute),
			)

			err := e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				obj.Spec.Platform.Azure.Topology = hyperv1.AzureTopologyPublicAndPrivate
			})
			Expect(err).NotTo(HaveOccurred(), "failed to update topology to PublicAndPrivate")

			e2eutil.EventuallyObject(GinkgoTB(), ctx, "KAS external public route exists after transition to PublicAndPrivate",
				func(ctx context.Context) (*routev1.Route, error) {
					route := hcpmanifests.KubeAPIServerExternalPublicRoute(controlPlaneNamespace)
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(route), route)
					return route, err
				},
				[]e2eutil.Predicate[*routev1.Route]{
					routeHasHostPredicate(),
				},
				e2eutil.WithTimeout(10*time.Minute),
			)

			Eventually(func() error {
				route := hcpmanifests.KubeAPIServerExternalPrivateRoute(controlPlaneNamespace)
				return expectDeleted(ctx, testCtx.MgmtClient, route)
			}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed(),
				"KAS external private route should be deleted after transition to PublicAndPrivate")

			e2eutil.EventuallyObject(GinkgoTB(), ctx, "OAuth external public route exists after transition to PublicAndPrivate",
				func(ctx context.Context) (*routev1.Route, error) {
					route := hcpmanifests.OauthServerExternalPublicRoute(controlPlaneNamespace)
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(route), route)
					return route, err
				},
				[]e2eutil.Predicate[*routev1.Route]{
					routeHasHostPredicate(),
				},
				e2eutil.WithTimeout(10*time.Minute),
			)

			Eventually(func() error {
				route := hcpmanifests.OauthServerExternalPrivateRoute(controlPlaneNamespace)
				return expectDeleted(ctx, testCtx.MgmtClient, route)
			}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed(),
				"OAuth external private route should be deleted after transition to PublicAndPrivate")

			e2eutil.EventuallyObject(GinkgoTB(), ctx, "router-public Service is LoadBalancer after transition to PublicAndPrivate",
				func(ctx context.Context) (*corev1.Service, error) {
					svc := hcpmanifests.RouterPublicService(controlPlaneNamespace)
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(svc), svc)
					return svc, err
				},
				[]e2eutil.Predicate[*corev1.Service]{
					serviceTypePredicate(corev1.ServiceTypeLoadBalancer),
				},
				e2eutil.WithTimeout(10*time.Minute),
			)

			e2eutil.EventuallyObjects(GinkgoTB(), ctx, "PLS CRs still exist after transition to PublicAndPrivate",
				func(ctx context.Context) ([]*hyperv1.AzurePrivateLinkService, error) {
					return listPLS(ctx, testCtx.MgmtClient, controlPlaneNamespace)
				},
				[]e2eutil.Predicate[[]*hyperv1.AzurePrivateLinkService]{
					plsExistsPredicate(),
				},
				nil,
				e2eutil.WithTimeout(2*time.Minute),
			)

			Eventually(func() error {
				svc := hcpmanifests.KubeAPIServerExternalPrivateService(controlPlaneNamespace)
				return expectDeleted(ctx, testCtx.MgmtClient, svc)
			}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed(),
				"KAS ExternalPrivateService should be deleted after transition to PublicAndPrivate")

			Eventually(func() error {
				svc := hcpmanifests.OauthServerExternalPrivateService(controlPlaneNamespace)
				return expectDeleted(ctx, testCtx.MgmtClient, svc)
			}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed(),
				"OAuth ExternalPrivateService should be deleted after transition to PublicAndPrivate")

			verifyAPIReachable(testCtx, "after transition to PublicAndPrivate")
		})

		It("should transition from PublicAndPrivate back to Private", func() {
			ctx := testCtx.Context

			err := e2eutil.UpdateObject(GinkgoTB(), ctx, testCtx.MgmtClient, hc, func(obj *hyperv1.HostedCluster) {
				obj.Spec.Platform.Azure.Topology = hyperv1.AzureTopologyPrivate
			})
			Expect(err).NotTo(HaveOccurred(), "failed to update topology to Private")

			e2eutil.EventuallyObject(GinkgoTB(), ctx, "KAS external private route exists after restore to Private",
				func(ctx context.Context) (*routev1.Route, error) {
					route := hcpmanifests.KubeAPIServerExternalPrivateRoute(controlPlaneNamespace)
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(route), route)
					return route, err
				},
				[]e2eutil.Predicate[*routev1.Route]{
					routeHasHostPredicate(),
				},
				e2eutil.WithTimeout(10*time.Minute),
			)

			Eventually(func() error {
				route := hcpmanifests.KubeAPIServerExternalPublicRoute(controlPlaneNamespace)
				return expectDeleted(ctx, testCtx.MgmtClient, route)
			}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed(),
				"KAS external public route should be deleted after restore to Private")

			e2eutil.EventuallyObject(GinkgoTB(), ctx, "OAuth external private route exists after restore to Private",
				func(ctx context.Context) (*routev1.Route, error) {
					route := hcpmanifests.OauthServerExternalPrivateRoute(controlPlaneNamespace)
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(route), route)
					return route, err
				},
				[]e2eutil.Predicate[*routev1.Route]{
					routeHasHostPredicate(),
				},
				e2eutil.WithTimeout(10*time.Minute),
			)

			Eventually(func() error {
				route := hcpmanifests.OauthServerExternalPublicRoute(controlPlaneNamespace)
				return expectDeleted(ctx, testCtx.MgmtClient, route)
			}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed(),
				"OAuth external public route should be deleted after restore to Private")

			Eventually(func() error {
				svc := hcpmanifests.RouterPublicService(controlPlaneNamespace)
				return expectDeleted(ctx, testCtx.MgmtClient, svc)
			}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed(),
				"router-public Service should be deleted after restore to Private")

			e2eutil.EventuallyObjects(GinkgoTB(), ctx, "PLS CRs still exist after restore to Private",
				func(ctx context.Context) ([]*hyperv1.AzurePrivateLinkService, error) {
					return listPLS(ctx, testCtx.MgmtClient, controlPlaneNamespace)
				},
				[]e2eutil.Predicate[[]*hyperv1.AzurePrivateLinkService]{
					plsExistsPredicate(),
				},
				nil,
				e2eutil.WithTimeout(2*time.Minute),
			)

			e2eutil.EventuallyObject(GinkgoTB(), ctx, "KAS ExternalPrivateService recreated after restore to Private",
				func(ctx context.Context) (*corev1.Service, error) {
					svc := hcpmanifests.KubeAPIServerExternalPrivateService(controlPlaneNamespace)
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(svc), svc)
					return svc, err
				},
				[]e2eutil.Predicate[*corev1.Service]{
					serviceTypePredicate(corev1.ServiceTypeExternalName),
				},
				e2eutil.WithTimeout(10*time.Minute),
			)

			e2eutil.EventuallyObject(GinkgoTB(), ctx, "OAuth ExternalPrivateService recreated after restore to Private",
				func(ctx context.Context) (*corev1.Service, error) {
					svc := hcpmanifests.OauthServerExternalPrivateService(controlPlaneNamespace)
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(svc), svc)
					return svc, err
				},
				[]e2eutil.Predicate[*corev1.Service]{
					serviceTypePredicate(corev1.ServiceTypeExternalName),
				},
				e2eutil.WithTimeout(10*time.Minute),
			)
		})

		It("should remain available after restoring Private topology", func() {
			// Private clusters' DNS zones are linked only to the guest VNet, so the
			// management cluster cannot resolve the KAS hostname. Validate health
			// via HostedCluster conditions instead of direct API connectivity,
			// matching the pattern from ValidatePrivateCluster in the v1 framework.
			e2eutil.EventuallyObject(GinkgoTB(), testCtx.Context, "HostedCluster is Available and not Degraded after restore to Private",
				func(ctx context.Context) (*hyperv1.HostedCluster, error) {
					freshHC := &hyperv1.HostedCluster{}
					err := testCtx.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hc), freshHC)
					return freshHC, err
				},
				[]e2eutil.Predicate[*hyperv1.HostedCluster]{
					func(hc *hyperv1.HostedCluster) (done bool, reasons string, err error) {
						for _, cond := range hc.Status.Conditions {
							if cond.Type == string(hyperv1.HostedClusterAvailable) {
								if cond.Status == metav1.ConditionTrue {
									return true, "HostedCluster Available=True", nil
								}
								return false, fmt.Sprintf("HostedCluster Available=%s reason=%s: %s",
									cond.Status, cond.Reason, cond.Message), nil
							}
						}
						return false, "HostedCluster Available condition not found", nil
					},
					func(hc *hyperv1.HostedCluster) (done bool, reasons string, err error) {
						for _, cond := range hc.Status.Conditions {
							if cond.Type == string(hyperv1.HostedClusterDegraded) {
								if cond.Status == metav1.ConditionTrue {
									return false, fmt.Sprintf("HostedCluster Degraded=True reason=%s: %s",
										cond.Reason, cond.Message), nil
								}
								return true, "HostedCluster Degraded!=True", nil
							}
						}
						return true, "HostedCluster Degraded condition not found (not degraded)", nil
					},
				},
				e2eutil.WithTimeout(15*time.Minute),
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

func routeHasHostPredicate() e2eutil.Predicate[*routev1.Route] {
	return func(route *routev1.Route) (done bool, reasons string, err error) {
		if route.Spec.Host != "" {
			return true, fmt.Sprintf("route has host %q", route.Spec.Host), nil
		}
		return false, "route has no host yet", nil
	}
}

func expectDeleted(ctx context.Context, client crclient.Client, obj crclient.Object) error {
	err := client.Get(ctx, crclient.ObjectKeyFromObject(obj), obj)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to check %T %s/%s: %w", obj, obj.GetNamespace(), obj.GetName(), err)
	}
	return fmt.Errorf("%T %s/%s still exists", obj, obj.GetNamespace(), obj.GetName())
}

func serviceTypePredicate(expected corev1.ServiceType) e2eutil.Predicate[*corev1.Service] {
	return func(svc *corev1.Service) (done bool, reasons string, err error) {
		if svc.Spec.Type == expected {
			return true, fmt.Sprintf("service type is %s", expected), nil
		}
		return false, fmt.Sprintf("service type is %s, want %s", svc.Spec.Type, expected), nil
	}
}

func plsExistsPredicate() e2eutil.Predicate[[]*hyperv1.AzurePrivateLinkService] {
	return func(items []*hyperv1.AzurePrivateLinkService) (done bool, reasons string, err error) {
		if len(items) == 0 {
			return false, "no AzurePrivateLinkService CRs found", nil
		}
		return true, fmt.Sprintf("%d AzurePrivateLinkService CR(s) exist", len(items)), nil
	}
}

func verifyAPIReachable(testCtx *internal.TestContext, phase string) {
	attempts := 0
	Eventually(func(g Gomega) {
		attempts++
		hc := testCtx.GetHostedCluster()
		g.Expect(hc).NotTo(BeNil(), "hosted cluster is nil %s", phase)
		g.Expect(hc.Status.KubeConfig).NotTo(BeNil(), "hosted cluster kubeconfig status not set %s", phase)

		var kubeconfigSecret corev1.Secret
		g.Expect(testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKey{
			Namespace: hc.Namespace,
			Name:      hc.Status.KubeConfig.Name,
		}, &kubeconfigSecret)).To(Succeed(), "failed to get kubeconfig secret %s", phase)

		kubeconfigData, ok := kubeconfigSecret.Data["kubeconfig"]
		g.Expect(ok).To(BeTrue(), "kubeconfig key missing from secret %s", phase)

		restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
		g.Expect(err).NotTo(HaveOccurred(), "failed to parse kubeconfig %s", phase)

		if attempts%6 == 1 {
			host := restConfig.Host
			GinkgoLogr.Info("API reachability attempt", "phase", phase, "attempt", attempts, "host", host)
			addrs, dnsErr := net.LookupHost(extractHostname(host))
			if dnsErr != nil {
				GinkgoLogr.Info("DNS lookup failed", "host", host, "error", dnsErr)
			} else {
				GinkgoLogr.Info("DNS lookup succeeded", "host", host, "addrs", addrs)
			}

			controlPlaneNamespace := testCtx.ControlPlaneNamespace
			extPrivRoute := hcpmanifests.KubeAPIServerExternalPrivateRoute(controlPlaneNamespace)
			if routeErr := testCtx.MgmtClient.Get(testCtx.Context, crclient.ObjectKeyFromObject(extPrivRoute), extPrivRoute); routeErr != nil {
				GinkgoLogr.Info("external-private Route not found", "error", routeErr)
			} else {
				visLabel := extPrivRoute.Labels[hyperv1.RouteVisibilityLabel]
				hcpLabel := extPrivRoute.Labels[netutil.HCPRouteLabel]
				intLabel := extPrivRoute.Labels[netutil.InternalRouteLabel]
				var canonicalHost, routerName string
				if len(extPrivRoute.Status.Ingress) > 0 {
					canonicalHost = extPrivRoute.Status.Ingress[0].RouterCanonicalHostname
					routerName = extPrivRoute.Status.Ingress[0].RouterName
				}
				GinkgoLogr.Info("external-private Route state",
					"specHost", extPrivRoute.Spec.Host,
					"visibilityLabel", visLabel,
					"hcpLabel", hcpLabel,
					"internalLabel", intLabel,
					"ingressCount", len(extPrivRoute.Status.Ingress),
					"routerCanonicalHostname", canonicalHost,
					"routerName", routerName,
				)
			}
		}

		freshClient, err := crclient.New(restConfig, crclient.Options{Scheme: hyperapi.Scheme})
		g.Expect(err).NotTo(HaveOccurred(), "failed to create fresh client %s", phase)

		nsList := &corev1.NamespaceList{}
		g.Expect(freshClient.List(testCtx.Context, nsList)).To(Succeed(), "failed to list namespaces %s", phase)
		g.Expect(nsList.Items).NotTo(BeEmpty(), "namespace list is empty %s", phase)
	}).WithTimeout(15 * time.Minute).WithPolling(10 * time.Second).Should(Succeed(), "API server not reachable %s", phase)
}

func extractHostname(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	if strings.HasPrefix(host, "https://") {
		host = strings.TrimPrefix(host, "https://")
		if h, _, err := net.SplitHostPort(host); err == nil {
			return h
		}
		return host
	}
	return host
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
	Context("[Feature:AzureOAuth] Azure OAuth LoadBalancer in Private Topology", Label("Azure", "self-managed-azure-oauth-lb-private"), Ordered, func() {
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
			oauthHost := e2eutil.WaitForOAuthLoadBalancerEndpoint(GinkgoTB(), ctx, testCtx.MgmtClient, hc)
			e2eutil.ValidateOAuthIdentityProviderFlow(GinkgoTB(), ctx, testCtx.MgmtClient, hc, oauthHost)
		})
	})
}

// RegisterHostedClusterAzureTests registers all Azure-specific hosted cluster tests.
func RegisterHostedClusterAzureTests(getTestCtx internal.TestContextGetter) {
	AzurePublicClusterTest(getTestCtx)
	AzurePrivateTopologyTest(getTestCtx)
	AzureEndpointAccessTransitionTest(getTestCtx)
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
