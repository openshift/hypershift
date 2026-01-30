package infra

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/events"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	routev1 "github.com/openshift/api/route/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	testNamespace        = "test-namespace"
	testClusterName      = "test-cluster"
	testIngressDomain    = "apps.example.com"
	testKASHostname      = "api.test.example.com"
	testOAuthHostname    = "oauth.test.example.com"
	testKonnectivityHost = "konnectivity.test.example.com"
)

// infraResources holds all the infrastructure resources created by the reconciler.
// This is used for fixture comparison.
type infraResources struct {
	Services corev1.ServiceList `json:"services"`
	Routes   routev1.RouteList  `json:"routes"`
}

// baseAWSHCP creates a basic HostedControlPlane for testing with AWS platform.
func baseAWSHCP() *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClusterName,
			Namespace: testNamespace,
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					Region: "us-east-1",
				},
			},
		},
	}
}

// baseAzureHCP creates a basic HostedControlPlane for testing with Azure platform.
func baseAzureHCP() *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClusterName,
			Namespace: testNamespace,
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
				Azure: &hyperv1.AzurePlatformSpec{
					ResourceGroupName: "test-rg",
					VnetID:            "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet",
					Location:          "eastus",
				},
			},
		},
	}
}

// withAWSEndpointAccess sets the AWS endpoint access type on the HCP.
func withAWSEndpointAccess(hcp *hyperv1.HostedControlPlane, access hyperv1.AWSEndpointAccessType) *hyperv1.HostedControlPlane {
	if hcp.Spec.Platform.AWS == nil {
		hcp.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{}
	}
	hcp.Spec.Platform.AWS.EndpointAccess = access
	return hcp
}

// withServices sets the service publishing strategies on the HCP.
func withServices(hcp *hyperv1.HostedControlPlane, services []hyperv1.ServicePublishingStrategyMapping) *hyperv1.HostedControlPlane {
	hcp.Spec.Services = services
	return hcp
}

// allServicesRouteWithHostnames creates service publishing strategies with all services using Route type with required hostnames.
// Note: APIServer requires a hostname when using Route publishing.
func allServicesRouteWithHostnames() []hyperv1.ServicePublishingStrategyMapping {
	return []hyperv1.ServicePublishingStrategyMapping{
		{
			Service: hyperv1.APIServer,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: testKASHostname,
				},
			},
		},
		{
			Service: hyperv1.Konnectivity,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: testKonnectivityHost,
				},
			},
		},
		{
			Service: hyperv1.OAuthServer,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: testOAuthHostname,
				},
			},
		},
		{
			Service: hyperv1.Ignition,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},
		},
	}
}

// kasServiceLoadBalancerOthersRoute creates service publishing strategies with LoadBalancer for APIServer
// and Route for others (Konnectivity, OAuthServer, Ignition).
func kasServiceLoadBalancerOthersRoute() []hyperv1.ServicePublishingStrategyMapping {
	return []hyperv1.ServicePublishingStrategyMapping{
		{
			Service: hyperv1.APIServer,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
			},
		},
		{
			Service: hyperv1.Konnectivity,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: testKonnectivityHost,
				},
			},
		},
		{
			Service: hyperv1.OAuthServer,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: testOAuthHostname,
				},
			},
		},
		{
			Service: hyperv1.Ignition,
			ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},
		},
	}
}

// collectResources retrieves all services and routes from the fake client and prepares them for fixture comparison.
func collectResources(ctx context.Context, c client.Client, namespace string) (*infraResources, error) {
	resources := &infraResources{}

	if err := c.List(ctx, &resources.Services, client.InNamespace(namespace)); err != nil {
		return nil, err
	}

	if err := c.List(ctx, &resources.Routes, client.InNamespace(namespace)); err != nil {
		return nil, err
	}

	// Clean up dynamic fields for stable fixture comparison
	for i := range resources.Services.Items {
		cleanServiceForFixture(&resources.Services.Items[i])
	}
	for i := range resources.Routes.Items {
		cleanRouteForFixture(&resources.Routes.Items[i])
	}

	return resources, nil
}

// cleanServiceForFixture removes dynamic fields from a Service for stable fixture comparison.
func cleanServiceForFixture(svc *corev1.Service) {
	svc.ResourceVersion = ""
	svc.UID = ""
	svc.CreationTimestamp = metav1.Time{}
	svc.ManagedFields = nil
	// Clean up owner references UID which is dynamic
	for i := range svc.OwnerReferences {
		svc.OwnerReferences[i].UID = ""
	}
}

// cleanRouteForFixture removes dynamic fields from a Route for stable fixture comparison.
func cleanRouteForFixture(route *routev1.Route) {
	route.ResourceVersion = ""
	route.UID = ""
	route.CreationTimestamp = metav1.Time{}
	route.ManagedFields = nil
	// Clean up owner references UID which is dynamic
	for i := range route.OwnerReferences {
		route.OwnerReferences[i].UID = ""
	}
}

// simulateLBServiceProvisioned simulates a cloud provider provisioning a LoadBalancer service
// by populating its status with an ingress hostname.
func simulateLBServiceProvisioned(ctx context.Context, c client.Client, svc *corev1.Service, hostname string) error {
	if err := c.Get(ctx, client.ObjectKeyFromObject(svc), svc); err != nil {
		return err
	}
	svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{Hostname: hostname}}
	return c.Status().Update(ctx, svc)
}

// simulateRouteAdmitted simulates a router admitting a route by populating its status.
func simulateRouteAdmitted(ctx context.Context, c client.Client, route *routev1.Route, routerHost string) error {
	if err := c.Get(ctx, client.ObjectKeyFromObject(route), route); err != nil {
		return err
	}
	route.Status.Ingress = []routev1.RouteIngress{
		{
			Host:       route.Spec.Host,
			RouterName: "default",
			Conditions: []routev1.RouteIngressCondition{
				{
					Type:   routev1.RouteAdmitted,
					Status: corev1.ConditionTrue,
				},
			},
			RouterCanonicalHostname: routerHost,
		},
	}
	return c.Status().Update(ctx, route)
}

func TestReconcileInfrastructure(t *testing.T) {
	const (
		testRouterLBHostname     = "router.test.elb.amazonaws.com"
		testKASLBHostname        = "kas.test.elb.amazonaws.com"
		testInternalRouterLBHost = "private-router.test.elb.amazonaws.com"
	)

	testCases := []struct {
		name            string
		hcp             *hyperv1.HostedControlPlane
		existingObjects []client.Object
		expectError     bool
		// setupEnv is an optional function to set up environment variables for the test.
		// The test uses t.Setenv so variables are automatically cleaned up after the test.
		setupEnv func(t *testing.T)
		// expectedStatus defines the expected InfrastructureStatus after reconciliation.
		// If nil, status validation is skipped.
		expectedStatus *InfrastructureStatus
	}{
		{
			name: "AWS_Public_Route",
			hcp: withServices(
				withAWSEndpointAccess(baseAWSHCP(), hyperv1.Public),
				allServicesRouteWithHostnames(),
			),
			expectError: false,
			// For Route strategy:
			// - APIHost comes from strategy.Route.Hostname (testKASHostname)
			// - External router is needed when LabelHCPRoutes (public + KAS uses Route with hostname)
			expectedStatus: &InfrastructureStatus{
				APIHost:               testKASHostname,
				APIPort:               443,
				OAuthEnabled:          true,
				OAuthHost:             testOAuthHostname,
				OAuthPort:             443,
				KonnectivityHost:      testKonnectivityHost,
				KonnectivityPort:      443,
				NeedInternalRouter:    false,
				NeedExternalRouter:    true,
				ExternalHCPRouterHost: testRouterLBHostname,
			},
		},
		{
			name: "AWS_Private_Route",
			hcp: withServices(
				withAWSEndpointAccess(baseAWSHCP(), hyperv1.Private),
				allServicesRouteWithHostnames(),
			),
			expectError: false,
			// For Private with Route:
			// - APIHost comes from strategy.Route.Hostname
			// - Internal router is needed when IsPrivateHCP
			// - External router NOT needed (not public)
			expectedStatus: &InfrastructureStatus{
				APIHost:               testKASHostname,
				APIPort:               443,
				OAuthEnabled:          true,
				OAuthHost:             testOAuthHostname,
				OAuthPort:             443,
				KonnectivityHost:      testKonnectivityHost,
				KonnectivityPort:      443,
				NeedInternalRouter:    true,
				InternalHCPRouterHost: testInternalRouterLBHost,
				NeedExternalRouter:    false,
			},
		},
		{
			name: "AWS_PublicAndPrivate_Route",
			hcp: withServices(
				withAWSEndpointAccess(baseAWSHCP(), hyperv1.PublicAndPrivate),
				allServicesRouteWithHostnames(),
			),
			expectError: false,
			// For PublicAndPrivate with Route:
			// - Both internal and external routers needed
			expectedStatus: &InfrastructureStatus{
				APIHost:               testKASHostname,
				APIPort:               443,
				OAuthEnabled:          true,
				OAuthHost:             testOAuthHostname,
				OAuthPort:             443,
				KonnectivityHost:      testKonnectivityHost,
				KonnectivityPort:      443,
				NeedInternalRouter:    true,
				InternalHCPRouterHost: testInternalRouterLBHost,
				NeedExternalRouter:    true,
				ExternalHCPRouterHost: testRouterLBHostname,
			},
		},
		{
			// With LabelHCPRoutes logic: Public + KAS LoadBalancer = routes NOT labeled,
			// so no external HCP router is needed.
			name: "AWS_Public_KAS_LoadBalancer",
			hcp: withServices(
				withAWSEndpointAccess(baseAWSHCP(), hyperv1.Public),
				kasServiceLoadBalancerOthersRoute(),
			),
			expectError: false,
			// For LoadBalancer strategy:
			// - APIHost comes from the LB service status
			// - External router is NOT needed: KAS uses LoadBalancer, not Route,
			//   so LabelHCPRoutes returns false for Public clusters
			expectedStatus: &InfrastructureStatus{
				APIHost:            testKASLBHostname,
				APIPort:            config.KASSVCPort,
				OAuthEnabled:       true,
				OAuthHost:          testOAuthHostname,
				OAuthPort:          443,
				KonnectivityHost:   testKonnectivityHost,
				KonnectivityPort:   443,
				NeedInternalRouter: false,
				NeedExternalRouter: false,
			},
		},
		{
			name: "AWS_Private_KAS_LoadBalancer",
			hcp: withServices(
				withAWSEndpointAccess(baseAWSHCP(), hyperv1.Private),
				kasServiceLoadBalancerOthersRoute(),
			),
			expectError: false,
			// For Private with LB:
			// - APIHost from private KAS LB
			// - Internal router needed for private HCP
			expectedStatus: &InfrastructureStatus{
				APIHost:               testKASLBHostname,
				APIPort:               config.KASSVCPort,
				OAuthEnabled:          true,
				OAuthHost:             testOAuthHostname,
				OAuthPort:             443,
				KonnectivityHost:      testKonnectivityHost,
				KonnectivityPort:      443,
				NeedInternalRouter:    true,
				InternalHCPRouterHost: testInternalRouterLBHost,
				NeedExternalRouter:    false,
			},
		},
		{
			name: "AWS_PublicAndPrivate_KAS_LoadBalancer",
			hcp: withServices(
				withAWSEndpointAccess(baseAWSHCP(), hyperv1.PublicAndPrivate),
				kasServiceLoadBalancerOthersRoute(),
			),
			expectError: false,
			// For PublicAndPrivate with LB:
			// - APIHost from public KAS LB
			// - Internal router needed for private access
			// - External router is NOT needed: KAS uses LoadBalancer, not Route,
			//   so LabelHCPRoutes returns false for public-facing routes
			expectedStatus: &InfrastructureStatus{
				APIHost:               testKASLBHostname,
				APIPort:               config.KASSVCPort,
				OAuthEnabled:          true,
				OAuthHost:             testOAuthHostname,
				OAuthPort:             443,
				KonnectivityHost:      testKonnectivityHost,
				KonnectivityPort:      443,
				NeedInternalRouter:    true,
				InternalHCPRouterHost: testInternalRouterLBHost,
				NeedExternalRouter:    false,
			},
		},
		// ARO HCP test cases - use shared ingress
		{
			name: "ARO_Route_SharedIngress",
			hcp: withServices(
				baseAzureHCP(),
				allServicesRouteWithHostnames(),
			),
			setupEnv: func(t *testing.T) {
				t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
			},
			expectError: false,
			// For ARO with shared ingress:
			// - APIHost comes directly from strategy.Route.Hostname
			// - Port is 443 (ExternalDNSLBPort)
			// - No internal/external routers needed (shared ingress handles it)
			expectedStatus: &InfrastructureStatus{
				APIHost:            testKASHostname,
				APIPort:            443,
				OAuthEnabled:       true,
				OAuthHost:          testOAuthHostname,
				OAuthPort:          443,
				KonnectivityHost:   testKonnectivityHost,
				KonnectivityPort:   443,
				NeedInternalRouter: false,
				NeedExternalRouter: false,
			},
		},
		{
			name: "ARO_Route_SharedIngress_And_Swift",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := withServices(baseAzureHCP(), allServicesRouteWithHostnames())
				hcp.Annotations = map[string]string{
					hyperv1.SwiftPodNetworkInstanceAnnotation: "swift-network-instance",
				}
				return hcp
			}(),
			setupEnv: func(t *testing.T) {
				t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
			},
			expectError: false,
			// For ARO with Swift:
			// - Swift handles pod networking, so no router services are needed
			// - APIHost comes from shared ingress (KasRouteHostname)
			// - Port is 443 (ExternalDNSLBPort)
			// - Konnectivity (and ignition see v2/ignitionserver) Routes use hypershift.local, kas and auth use both hypershift.local and external routes
			expectedStatus: &InfrastructureStatus{
				APIHost:            testKASHostname,
				APIPort:            443,
				OAuthEnabled:       true,
				OAuthHost:          testOAuthHostname,
				OAuthPort:          443,
				KonnectivityHost:   testKonnectivityHost,
				KonnectivityPort:   443,
				NeedInternalRouter: false,
				NeedExternalRouter: false,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ctx := context.Background()

			// Run optional environment setup
			if tc.setupEnv != nil {
				tc.setupEnv(t)
			}

			// Build fake client with event indexer for message collection
			clientBuilder := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithIndex(&corev1.Event{}, events.EventInvolvedObjectUIDField, func(obj client.Object) []string {
					return []string{string(obj.(*corev1.Event).InvolvedObject.UID)}
				})
			if len(tc.existingObjects) > 0 {
				clientBuilder = clientBuilder.WithObjects(tc.existingObjects...)
			}
			fakeClient := clientBuilder.Build()

			reconciler := NewReconciler(fakeClient, testIngressDomain)
			createOrUpdate := upsert.New(false).CreateOrUpdate

			err := reconciler.ReconcileInfrastructure(ctx, tc.hcp, createOrUpdate)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())

			// Collect all created resources
			resources, err := collectResources(ctx, fakeClient, testNamespace)
			g.Expect(err).NotTo(HaveOccurred())

			// Compare with fixture
			testutil.CompareWithFixture(t, resources)

			// If expectedStatus is provided, simulate LB provisioning and validate the status
			if tc.expectedStatus != nil {
				// Simulate cloud provider provisioning LoadBalancer services
				// and router admitting routes
				if err := simulateInfraProvisioning(ctx, fakeClient, tc.hcp, testRouterLBHostname, testInternalRouterLBHost, testKASLBHostname); err != nil {
					t.Fatalf("failed to simulate infra provisioning: %v", err)
				}

				// Call ReconcileInfrastructureStatus
				status, err := reconciler.ReconcileInfrastructureStatus(ctx, tc.hcp)
				g.Expect(err).NotTo(HaveOccurred())

				// Validate the key status fields
				g.Expect(status.APIHost).To(Equal(tc.expectedStatus.APIHost), "APIHost mismatch")
				g.Expect(status.APIPort).To(Equal(tc.expectedStatus.APIPort), "APIPort mismatch")
				g.Expect(status.OAuthEnabled).To(Equal(tc.expectedStatus.OAuthEnabled), "OAuthEnabled mismatch")
				g.Expect(status.OAuthHost).To(Equal(tc.expectedStatus.OAuthHost), "OAuthHost mismatch")
				g.Expect(status.OAuthPort).To(Equal(tc.expectedStatus.OAuthPort), "OAuthPort mismatch")
				g.Expect(status.KonnectivityHost).To(Equal(tc.expectedStatus.KonnectivityHost), "KonnectivityHost mismatch")
				g.Expect(status.KonnectivityPort).To(Equal(tc.expectedStatus.KonnectivityPort), "KonnectivityPort mismatch")
				g.Expect(status.NeedInternalRouter).To(Equal(tc.expectedStatus.NeedInternalRouter), "NeedInternalRouter mismatch")
				g.Expect(status.NeedExternalRouter).To(Equal(tc.expectedStatus.NeedExternalRouter), "NeedExternalRouter mismatch")
				if tc.expectedStatus.NeedInternalRouter {
					g.Expect(status.InternalHCPRouterHost).To(Equal(tc.expectedStatus.InternalHCPRouterHost), "InternalHCPRouterHost mismatch")
				} else {
					g.Expect(status.InternalHCPRouterHost).To(BeEmpty(), "InternalHCPRouterHost should be empty")
				}
				if tc.expectedStatus.NeedExternalRouter {
					g.Expect(status.ExternalHCPRouterHost).To(Equal(tc.expectedStatus.ExternalHCPRouterHost), "ExternalHCPRouterHost mismatch")
				} else {
					g.Expect(status.ExternalHCPRouterHost).To(BeEmpty(), "ExternalHCPRouterHost should be empty")
				}

				// Verify IsReady returns true when all hosts are populated
				g.Expect(status.IsReady()).To(BeTrue(), "Expected status to be ready")
			}
		})
	}
}

// simulateInfraProvisioning simulates cloud provider and router provisioning the infrastructure.
// It unconditionally tries to provision all possible services/routes, ignoring "not found" errors.
func simulateInfraProvisioning(ctx context.Context, c client.Client, hcp *hyperv1.HostedControlPlane, externalRouterLBHost, internalRouterLBHost, kasLBHost string) error {
	// List of all LoadBalancer services that might need provisioning
	lbServices := []struct {
		svc      *corev1.Service
		hostname string
	}{
		{manifests.RouterPublicService(hcp.Namespace), externalRouterLBHost},
		{manifests.PrivateRouterService(hcp.Namespace), internalRouterLBHost},
		{manifests.KubeAPIServerService(hcp.Namespace), kasLBHost},
		{manifests.KubeAPIServerPrivateService(hcp.Namespace), kasLBHost},
	}

	for _, lb := range lbServices {
		if err := simulateLBServiceProvisioned(ctx, c, lb.svc, lb.hostname); err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}
		}
	}

	// List of all routes that might need admission
	routes := []*routev1.Route{
		manifests.KonnectivityServerRoute(hcp.Namespace),
		manifests.OauthServerExternalPublicRoute(hcp.Namespace),
		manifests.OauthServerExternalPrivateRoute(hcp.Namespace),
	}

	for _, route := range routes {
		if err := simulateRouteAdmitted(ctx, c, route, externalRouterLBHost); err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}
		}
	}

	return nil
}

func TestReconcileInfrastructure_ErrorCases(t *testing.T) {
	testCases := []struct {
		name string
		hcp  *hyperv1.HostedControlPlane
	}{
		{
			name: "When services are nil it should return an error",
			hcp:  withAWSEndpointAccess(baseAWSHCP(), hyperv1.Public),
		},
		{
			name: "When Route publishing without hostname for APIServer it should return an error",
			hcp: withServices(
				withAWSEndpointAccess(baseAWSHCP(), hyperv1.Public),
				[]hyperv1.ServicePublishingStrategyMapping{
					{
						Service: hyperv1.APIServer,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.Route,
							// Missing hostname - should cause error
						},
					},
					{
						Service: hyperv1.Konnectivity,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.Route,
							Route: &hyperv1.RoutePublishingStrategy{
								Hostname: testKonnectivityHost,
							},
						},
					},
					{
						Service: hyperv1.OAuthServer,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.Route,
							Route: &hyperv1.RoutePublishingStrategy{
								Hostname: testOAuthHostname,
							},
						},
					},
					{
						Service: hyperv1.Ignition,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.Route,
						},
					},
				},
			),
		},
		{
			name: "When LoadBalancer publishing for OAuth it should return an error",
			hcp: withServices(
				withAWSEndpointAccess(baseAWSHCP(), hyperv1.Public),
				[]hyperv1.ServicePublishingStrategyMapping{
					{
						Service: hyperv1.APIServer,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.LoadBalancer,
						},
					},
					{
						Service: hyperv1.Konnectivity,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.LoadBalancer,
						},
					},
					{
						Service: hyperv1.OAuthServer,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.LoadBalancer, // Not supported for OAuth
						},
					},
					{
						Service: hyperv1.Ignition,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.Route,
						},
					},
				},
			),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			reconciler := NewReconciler(fakeClient, testIngressDomain)
			createOrUpdate := upsert.New(false).CreateOrUpdate

			err := reconciler.ReconcileInfrastructure(context.Background(), tc.hcp, createOrUpdate)
			g.Expect(err).To(HaveOccurred())
		})
	}
}

func TestReconcileInfrastructure_WhenTransitioningFromPublicToPrivate_ItShouldCleanUpPublicResources(t *testing.T) {
	g := NewGomegaWithT(t)

	// Start with Public configuration
	hcp := withServices(
		withAWSEndpointAccess(baseAWSHCP(), hyperv1.Public),
		allServicesRouteWithHostnames(),
	)

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		Build()

	reconciler := NewReconciler(fakeClient, testIngressDomain)
	createOrUpdate := upsert.New(false).CreateOrUpdate

	// First reconcile with Public
	err := reconciler.ReconcileInfrastructure(context.Background(), hcp, createOrUpdate)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify public route exists
	publicRoute := &routev1.Route{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{
		Namespace: testNamespace,
		Name:      manifests.KubeAPIServerExternalPublicRoute(testNamespace).Name,
	}, publicRoute)
	g.Expect(err).NotTo(HaveOccurred())

	// Now transition to Private
	hcp = withServices(
		withAWSEndpointAccess(baseAWSHCP(), hyperv1.Private),
		allServicesRouteWithHostnames(),
	)

	err = reconciler.ReconcileInfrastructure(context.Background(), hcp, createOrUpdate)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify public route is deleted
	err = fakeClient.Get(context.Background(), client.ObjectKey{
		Namespace: testNamespace,
		Name:      manifests.KubeAPIServerExternalPublicRoute(testNamespace).Name,
	}, publicRoute)
	g.Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
	g.Expect(err).To(HaveOccurred()) // Should be NotFound

	// Verify private route now exists
	privateRoute := &routev1.Route{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{
		Namespace: testNamespace,
		Name:      manifests.KubeAPIServerExternalPrivateRoute(testNamespace).Name,
	}, privateRoute)
	g.Expect(err).NotTo(HaveOccurred())
}

func TestReconcileInfrastructure_WhenTransitioningFromPrivateToPublic_ItShouldCleanUpPrivateResources(t *testing.T) {
	g := NewGomegaWithT(t)

	// Start with Private configuration
	hcp := withServices(
		withAWSEndpointAccess(baseAWSHCP(), hyperv1.Private),
		allServicesRouteWithHostnames(),
	)

	fakeClient := fake.NewClientBuilder().
		WithScheme(api.Scheme).
		Build()

	reconciler := NewReconciler(fakeClient, testIngressDomain)
	createOrUpdate := upsert.New(false).CreateOrUpdate

	// First reconcile with Private
	err := reconciler.ReconcileInfrastructure(context.Background(), hcp, createOrUpdate)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify private route exists
	privateRoute := &routev1.Route{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{
		Namespace: testNamespace,
		Name:      manifests.KubeAPIServerExternalPrivateRoute(testNamespace).Name,
	}, privateRoute)
	g.Expect(err).NotTo(HaveOccurred())

	// Now transition to Public
	hcp = withServices(
		withAWSEndpointAccess(baseAWSHCP(), hyperv1.Public),
		allServicesRouteWithHostnames(),
	)

	err = reconciler.ReconcileInfrastructure(context.Background(), hcp, createOrUpdate)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify private route is deleted
	err = fakeClient.Get(context.Background(), client.ObjectKey{
		Namespace: testNamespace,
		Name:      manifests.KubeAPIServerExternalPrivateRoute(testNamespace).Name,
	}, privateRoute)
	g.Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
	g.Expect(err).To(HaveOccurred()) // Should be NotFound

	// Verify public route now exists
	publicRoute := &routev1.Route{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{
		Namespace: testNamespace,
		Name:      manifests.KubeAPIServerExternalPublicRoute(testNamespace).Name,
	}, publicRoute)
	g.Expect(err).NotTo(HaveOccurred())
}

// Tests moved from hostedcontrolplane_controller_test.go

func TestReconcileOAuthService(t *testing.T) {
	targetNamespace := "test"
	apiPort := int32(config.KASSVCPort)
	hostname := "test.example.com"
	allowCIDR := []hyperv1.CIDRBlock{"1.2.3.4/24"}
	ipFamilyPolicy := corev1.IPFamilyPolicyPreferDualStack

	ownerRef := metav1.OwnerReference{
		APIVersion:         "hypershift.openshift.io/v1beta1",
		Kind:               "HostedControlPlane",
		Name:               "test",
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
	oauthPublicService := func(m ...func(*corev1.Service)) corev1.Service {
		svc := corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:       targetNamespace,
				Name:            manifests.OauthServerService(targetNamespace).Name,
				OwnerReferences: []metav1.OwnerReference{ownerRef},
			},
			Spec: corev1.ServiceSpec{
				Type:           corev1.ServiceTypeClusterIP,
				IPFamilyPolicy: &ipFamilyPolicy,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       apiPort,
						TargetPort: intstr.FromInt32(apiPort),
					},
				},
				Selector: map[string]string{
					"app": "oauth-openshift",
					"hypershift.openshift.io/control-plane-component": "oauth-openshift",
				},
			},
		}
		for _, m := range m {
			m(&svc)
		}
		return svc
	}
	oauthExternalPublicRoute := func(m ...func(*routev1.Route)) routev1.Route {
		route := routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:       targetNamespace,
				Name:            "oauth",
				OwnerReferences: []metav1.OwnerReference{ownerRef},
			},
			Spec: routev1.RouteSpec{
				Host: hostname,
				To: routev1.RouteTargetReference{
					Kind: "Service",
					Name: manifests.OauthServerService("").Name,
				},
				TLS: &routev1.TLSConfig{
					Termination:                   routev1.TLSTerminationPassthrough,
					InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
				},
			},
		}
		for _, m := range m {
			m(&route)
		}
		return route
	}
	oauthInternalRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      "oauth-internal",
			Labels: map[string]string{
				"hypershift.openshift.io/hosted-control-plane": targetNamespace,
				"hypershift.openshift.io/internal-route":       "true",
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: routev1.RouteSpec{
			Host: "oauth.apps.test.hypershift.local",
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: manifests.OauthServerService("").Name,
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationPassthrough,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
			},
		},
	}
	testsCases := []struct {
		name                    string
		endpointAccess          hyperv1.AWSEndpointAccessType
		oauthPublishingStrategy hyperv1.ServicePublishingStrategy

		expectedServices []corev1.Service
		expectedRoutes   []routev1.Route
	}{
		{
			name:           "Route strategy, Public",
			endpointAccess: hyperv1.Public,
			oauthPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: hostname,
				},
			},
			expectedServices: []corev1.Service{
				oauthPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
				}),
			},
			expectedRoutes: []routev1.Route{
				oauthExternalPublicRoute(),
			},
		},
		{
			name:           "Route strategy, PublicPrivate",
			endpointAccess: hyperv1.PublicAndPrivate,
			oauthPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: hostname,
				},
			},

			expectedServices: []corev1.Service{
				oauthPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
				}),
			},
			expectedRoutes: []routev1.Route{
				oauthExternalPublicRoute(),
				oauthInternalRoute,
			},
		},
		{
			name:           "Route strategy, PublicPrivate, no hostname",
			endpointAccess: hyperv1.PublicAndPrivate,
			oauthPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
			},

			expectedServices: []corev1.Service{
				oauthPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
				}),
			},
			expectedRoutes: []routev1.Route{
				oauthExternalPublicRoute(func(s *routev1.Route) {
					s.Spec.Host = ""
					// The route should not be admitted by the private router.
					delete(s.Labels, "hypershift.openshift.io/hosted-control-plane")
				}),
				oauthInternalRoute,
			},
		},
		{
			name:           "Route strategy, Private",
			endpointAccess: hyperv1.Private,
			oauthPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type:  hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{},
			},
			expectedServices: []corev1.Service{
				oauthPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
				}),
			},
			expectedRoutes: []routev1.Route{
				oauthInternalRoute,
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "test",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							Port:              &apiPort,
							AllowedCIDRBlocks: allowCIDR,
						},
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: tc.endpointAccess,
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{{
						Service:                   hyperv1.OAuthServer,
						ServicePublishingStrategy: tc.oauthPublishingStrategy,
					}},
				},
			}

			ctx := context.Background()

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			r := NewReconciler(fakeClient, testIngressDomain)

			if err := r.reconcileOAuthServerService(ctx, hcp, controllerutil.CreateOrUpdate); err != nil {
				t.Fatalf("reconcileOAuthServerService failed: %v", err)
			}

			var actualServices corev1.ServiceList
			if err := fakeClient.List(ctx, &actualServices); err != nil {
				t.Fatalf("failed to list services: %v", err)
			}

			if diff := testutil.MarshalYamlAndDiff(&actualServices, &corev1.ServiceList{Items: tc.expectedServices}, t); diff != "" {
				t.Errorf("actual services differ from expected: %s", diff)
			}

			var actualRoutes routev1.RouteList
			if err := fakeClient.List(ctx, &actualRoutes); err != nil {
				t.Fatalf("failed to list routes: %v", err)
			}
			if diff := testutil.MarshalYamlAndDiff(&actualRoutes, &routev1.RouteList{Items: tc.expectedRoutes}, t); diff != "" {
				t.Errorf("actual routes differ from expected: %s", diff)
			}
		})
	}
}

func TestReconcileAPIServerService(t *testing.T) {
	targetNamespace := "test"
	apiPort := int32(config.KASSVCPort)
	kasPort := "client"
	hostname := "test.example.com"
	allowCIDR := []hyperv1.CIDRBlock{"1.2.3.4/24"}
	allowCIDRString := []string{"1.2.3.4/24"}
	ipFamilyPolicy := corev1.IPFamilyPolicyPreferDualStack

	ownerRef := metav1.OwnerReference{
		APIVersion:         "hypershift.openshift.io/v1beta1",
		Kind:               "HostedControlPlane",
		Name:               "test",
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
	kasPublicService := func(m ...func(*corev1.Service)) corev1.Service {
		svc := corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: targetNamespace,
				Name:      manifests.KubeAPIServerService(targetNamespace).Name,
				Annotations: map[string]string{
					"service.beta.kubernetes.io/aws-load-balancer-type": "nlb",
					hyperv1.ExternalDNSHostnameAnnotation:               hostname,
				},
				Labels: map[string]string{
					"app": "kube-apiserver",
					"hypershift.openshift.io/control-plane-component": "kube-apiserver",
				},
				OwnerReferences: []metav1.OwnerReference{ownerRef},
			},
			Spec: corev1.ServiceSpec{
				Type:           corev1.ServiceTypeLoadBalancer,
				IPFamilyPolicy: &ipFamilyPolicy,
				Ports: []corev1.ServicePort{
					{
						Protocol:   corev1.ProtocolTCP,
						Port:       apiPort,
						TargetPort: intstr.FromString(kasPort),
					},
				},
				Selector: map[string]string{
					"app": "kube-apiserver",
					"hypershift.openshift.io/control-plane-component": "kube-apiserver",
				},
			},
		}
		for _, m := range m {
			m(&svc)
		}
		return svc
	}
	kasPrivateService := func(m ...func(*corev1.Service)) corev1.Service {
		return kasPublicService(append(m, func(s *corev1.Service) {
			s.Name = manifests.KubeAPIServerPrivateService(targetNamespace).Name

			delete(s.Annotations, hyperv1.ExternalDNSHostnameAnnotation)
			s.Annotations["service.beta.kubernetes.io/aws-load-balancer-internal"] = "true"

			s.Labels = nil
		})...)
	}
	withCrossZoneAnnotation := func(svc *corev1.Service) {
		svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled"] = "true"
	}
	withLoadBalancerSourceRanges := func(svc *corev1.Service) {
		svc.Spec.LoadBalancerSourceRanges = allowCIDRString
	}
	kasExternalPublicRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      "kube-apiserver",
			Labels: map[string]string{
				"hypershift.openshift.io/hosted-control-plane": targetNamespace,
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: routev1.RouteSpec{
			Host: hostname,
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: manifests.KubeAPIServerService("").Name,
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationPassthrough,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
			},
		},
	}
	kasExternalPrivateRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      "kube-apiserver-private",
			Labels: map[string]string{
				"hypershift.openshift.io/hosted-control-plane": targetNamespace,
				hyperv1.RouteVisibilityLabel:                   string(hyperv1.RouteVisibilityPrivate),
				util.InternalRouteLabel:                        "true",
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: routev1.RouteSpec{
			Host: hostname,
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: manifests.KubeAPIServerService("").Name,
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationPassthrough,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
			},
		},
	}
	kasInternalRoute := routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      "kube-apiserver-internal",
			Labels: map[string]string{
				"hypershift.openshift.io/hosted-control-plane": targetNamespace,
				"hypershift.openshift.io/internal-route":       "true",
			},
			OwnerReferences: []metav1.OwnerReference{ownerRef},
		},
		Spec: routev1.RouteSpec{
			Host: "api.test.hypershift.local",
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: manifests.KubeAPIServerService("").Name,
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationPassthrough,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
			},
		},
	}
	testsCases := []struct {
		name                  string
		endpointAccess        hyperv1.AWSEndpointAccessType
		apiPublishingStrategy hyperv1.ServicePublishingStrategy

		expectedServices []corev1.Service
		expectedRoutes   []routev1.Route
	}{
		{
			name:           "LB strategy, public",
			endpointAccess: hyperv1.Public,
			apiPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					Hostname: hostname,
				},
			},

			expectedServices: []corev1.Service{
				kasPublicService(withLoadBalancerSourceRanges),
			},
		},
		{
			name:           "LB strategy, publicPrivate",
			endpointAccess: hyperv1.PublicAndPrivate,
			apiPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					Hostname: hostname,
				},
			},

			expectedServices: []corev1.Service{
				kasPublicService(withCrossZoneAnnotation, withLoadBalancerSourceRanges),
				kasPrivateService(withCrossZoneAnnotation),
			},
		},
		{
			name:           "LB strategy, private",
			endpointAccess: hyperv1.Private,
			apiPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.LoadBalancer,
				LoadBalancer: &hyperv1.LoadBalancerPublishingStrategy{
					Hostname: hostname,
				},
			},

			expectedServices: []corev1.Service{
				kasPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
					delete(s.Annotations, "external-dns.alpha.kubernetes.io/hostname")
				}),
				kasPrivateService(withCrossZoneAnnotation),
			},
		},
		{
			name:           "Route strategy, public",
			endpointAccess: hyperv1.Public,
			apiPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: hostname,
				},
			},

			expectedServices: []corev1.Service{
				kasPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
					delete(s.Annotations, "external-dns.alpha.kubernetes.io/hostname")
				}),
			},
			expectedRoutes: []routev1.Route{
				kasExternalPublicRoute,
				kasInternalRoute,
			},
		},
		{
			name:           "Route strategy, publicPrivate",
			endpointAccess: hyperv1.PublicAndPrivate,
			apiPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: hostname,
				},
			},

			expectedServices: []corev1.Service{
				kasPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
					delete(s.Annotations, "external-dns.alpha.kubernetes.io/hostname")
				}),
			},
			expectedRoutes: []routev1.Route{
				kasExternalPublicRoute,
				kasInternalRoute,
			},
		},
		{
			name:           "Route strategy, private",
			endpointAccess: hyperv1.Private,
			apiPublishingStrategy: hyperv1.ServicePublishingStrategy{
				Type: hyperv1.Route,
				Route: &hyperv1.RoutePublishingStrategy{
					Hostname: hostname,
				},
			},

			expectedServices: []corev1.Service{
				kasPublicService(func(s *corev1.Service) {
					s.Spec.Type = corev1.ServiceTypeClusterIP
					delete(s.Annotations, "external-dns.alpha.kubernetes.io/hostname")
				}),
			},
			expectedRoutes: []routev1.Route{
				kasInternalRoute,
				kasExternalPrivateRoute,
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: targetNamespace,
					Name:      "test",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{
							Port:              &apiPort,
							AllowedCIDRBlocks: allowCIDR,
						},
					},
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: tc.endpointAccess,
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{{
						Service:                   hyperv1.APIServer,
						ServicePublishingStrategy: tc.apiPublishingStrategy,
					}},
				},
			}

			ctx := context.Background()

			fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			r := NewReconciler(fakeClient, testIngressDomain)

			if err := r.reconcileAPIServerService(ctx, hcp, controllerutil.CreateOrUpdate); err != nil {
				t.Fatalf("reconcileAPIServerService failed: %v", err)
			}

			var actualServices corev1.ServiceList
			if err := fakeClient.List(ctx, &actualServices); err != nil {
				t.Fatalf("failed to list services: %v", err)
			}

			if diff := testutil.MarshalYamlAndDiff(&actualServices, &corev1.ServiceList{Items: tc.expectedServices}, t); diff != "" {
				t.Errorf("actual services differ from expected: %s", diff)
			}

			var actualRoutes routev1.RouteList
			if err := fakeClient.List(ctx, &actualRoutes); err != nil {
				t.Fatalf("failed to list routes: %v", err)
			}
			if diff := testutil.MarshalYamlAndDiff(&actualRoutes, &routev1.RouteList{Items: tc.expectedRoutes}, t); diff != "" {
				t.Errorf("actual routes differ from expected: %s", diff)
			}
		})
	}
}

func TestReconcileHCPRouterServices(t *testing.T) {
	const namespace = "test-ns"
	publicService := func(m ...func(*corev1.Service)) *corev1.Service {
		svc := corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "router",
				Namespace: namespace,
				Annotations: map[string]string{
					"service.beta.kubernetes.io/aws-load-balancer-type": "nlb",
				},
				Labels: map[string]string{"app": "private-router"},
			},
			Spec: corev1.ServiceSpec{
				Type:     corev1.ServiceTypeLoadBalancer,
				Selector: map[string]string{"app": "private-router"},
				Ports: []corev1.ServicePort{
					{Name: "https", Port: 443, TargetPort: intstr.FromString("https"), Protocol: corev1.ProtocolTCP},
				},
			},
		}

		for _, m := range m {
			m(&svc)
		}
		return &svc
	}
	privateService := func(m ...func(*corev1.Service)) *corev1.Service {
		return publicService(append(m, func(s *corev1.Service) {
			s.Name = "private-router"
			s.Annotations["service.beta.kubernetes.io/aws-load-balancer-internal"] = "true"
		})...)
	}
	withCrossZoneAnnotation := func(svc *corev1.Service) {
		svc.Annotations["service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled"] = "true"
	}
	tests := []struct {
		name                         string
		endpointAccess               hyperv1.AWSEndpointAccessType
		exposeAPIServerThroughRouter bool
		existingObjects              []client.Object
		expectedServices             []corev1.Service
		setupEnv                     func(t *testing.T)
		hcpModifier                  func(*hyperv1.HostedControlPlane)
	}{
		{
			name:                         "Public HCP gets public LB only",
			endpointAccess:               hyperv1.Public,
			exposeAPIServerThroughRouter: true,
			expectedServices: []corev1.Service{
				*publicService(),
			},
		},
		{
			name:                         "PublicPrivate gets public and private LB",
			endpointAccess:               hyperv1.PublicAndPrivate,
			exposeAPIServerThroughRouter: true,
			expectedServices: []corev1.Service{
				*privateService(withCrossZoneAnnotation),
				*publicService(withCrossZoneAnnotation),
			},
		},
		{
			name:                         "Private gets private LB only",
			endpointAccess:               hyperv1.Private,
			exposeAPIServerThroughRouter: true,
			expectedServices: []corev1.Service{
				*privateService(withCrossZoneAnnotation),
			},
		},
		{
			name:                         "Public LB gets removed when switching to Private",
			endpointAccess:               hyperv1.Private,
			exposeAPIServerThroughRouter: true,
			existingObjects:              []client.Object{publicService(), privateService()},
			expectedServices: []corev1.Service{
				*privateService(withCrossZoneAnnotation),
			},
		},
		{
			name:                         "Private LB gets removed when switching to Public",
			endpointAccess:               hyperv1.Public,
			exposeAPIServerThroughRouter: true,
			existingObjects:              []client.Object{privateService()},
			expectedServices: []corev1.Service{
				*publicService(),
			},
		},
		{
			name:                         "Public LB gets removed when PublicAndPrivate but not using Route",
			endpointAccess:               hyperv1.PublicAndPrivate,
			exposeAPIServerThroughRouter: false,
			existingObjects:              []client.Object{publicService()},
			expectedServices: []corev1.Service{
				*privateService(withCrossZoneAnnotation),
			},
		},
		{
			name:                         "No LB created when public and not using Route",
			endpointAccess:               hyperv1.Public,
			exposeAPIServerThroughRouter: false,
			expectedServices:             nil,
		},
		{
			name:                         "When ARO is enabled it should not create any services",
			endpointAccess:               hyperv1.Public,
			exposeAPIServerThroughRouter: true,
			expectedServices:             nil,
			setupEnv: func(t *testing.T) {
				t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
			},
			hcpModifier: func(hcp *hyperv1.HostedControlPlane) {
				hcp.Spec.Platform.Type = hyperv1.AzurePlatform
				hcp.Spec.Platform.AWS = nil
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setupEnv != nil {
				tc.setupEnv(t)
			}
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcp",
					Namespace: namespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: tc.endpointAccess,
						},
					},
				},
			}
			if tc.exposeAPIServerThroughRouter {
				hcp.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
					{
						Service: hyperv1.APIServer,
						ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
							Type: hyperv1.Route,
							Route: &hyperv1.RoutePublishingStrategy{
								Hostname: "apiserver.example.com",
							},
						},
					},
				}
			}
			if tc.hcpModifier != nil {
				tc.hcpModifier(hcp)
			}

			ctx := context.Background()
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(append(tc.existingObjects, hcp)...).Build()

			r := NewReconciler(c, testIngressDomain)

			if err := r.reconcileHCPRouterServices(ctx, hcp, controllerutil.CreateOrUpdate); err != nil {
				t.Fatalf("reconcileRouter failed: %v", err)
			}

			var services corev1.ServiceList
			if err := c.List(ctx, &services); err != nil {
				t.Fatalf("failed to list services: %v", err)
			}
			if diff := testutil.MarshalYamlAndDiff(&services, &corev1.ServiceList{Items: tc.expectedServices}, t); diff != "" {
				t.Errorf("actual services differ from expected: %s", diff)
			}
		})
	}
}

type fakeMessageCollector struct {
	msg string
}

func (c *fakeMessageCollector) ErrorMessages(resource client.Object) ([]string, error) {
	return []string{c.msg}, nil
}

var _ events.MessageCollector = &fakeMessageCollector{}

func TestReconcileRouterServiceStatus(t *testing.T) {
	const namespace = "test-ns"
	const svcName = "test"
	tests := []struct {
		name         string
		svc          *corev1.Service
		expectedHost string
		expectMsg    bool
	}{
		{
			name: "Non-existent service",
		},
		{
			name: "Service that has not been provisioned",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: namespace},
			},
			expectMsg: true,
		},
		{
			name: "Service with host populated",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: namespace},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{
								Hostname: "test.host",
							},
						},
					},
				},
			},
			expectedHost: "test.host",
		},
		{
			name: "Service with IP populated",
			svc: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: namespace},
				Status: corev1.ServiceStatus{
					LoadBalancer: corev1.LoadBalancerStatus{
						Ingress: []corev1.LoadBalancerIngress{
							{
								IP: "1.2.3.4",
							},
						},
					},
				},
			},
			expectedHost: "1.2.3.4",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			existing := []client.Object{}
			if tc.svc != nil {
				existing = append(existing, tc.svc)
			}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(existing...).Build()

			r := NewReconciler(c, testIngressDomain)
			msgCollector := &fakeMessageCollector{msg: "test message"}
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: namespace},
			}
			host, needed, msg, err := r.reconcileRouterServiceStatus(ctx, svc, msgCollector)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if !needed {
				t.Fatalf("unexpected, needed == false")
			}
			if host != tc.expectedHost {
				t.Errorf("unexpected host, actual: %s, expected: %s", host, tc.expectedHost)
			}
			if tc.expectMsg {
				if msg == "" {
					t.Errorf("did not get an event message")
				}
			} else {
				if len(msg) > 0 {
					t.Errorf("got unexpected event message")
				}
			}
		})
	}
}

func TestReconcileInternalRouterServiceStatus(t *testing.T) {
	testCases := []struct {
		name       string
		setup      func(t *testing.T)
		hcp        *hyperv1.HostedControlPlane
		wantNeeded bool
		wantHost   string
		wantMsg    string
	}{
		{
			name: "When ARO swift is enabled it should not need internal router",
			setup: func(t *testing.T) {
				t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						hyperv1.SwiftPodNetworkInstanceAnnotation: "swift-network-instance",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
						Azure: &hyperv1.AzurePlatformSpec{
							Location: "eastus",
						},
					},
				},
			},
			wantNeeded: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup(t)
			}
			ctx := context.Background()
			c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()
			r := NewReconciler(c, testIngressDomain)

			host, needed, msg, err := r.reconcileInternalRouterServiceStatus(ctx, tc.hcp)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if needed != tc.wantNeeded {
				t.Fatalf("unexpected needed, got %t want %t", needed, tc.wantNeeded)
			}
			if host != tc.wantHost {
				t.Fatalf("unexpected host, got %q want %q", host, tc.wantHost)
			}
			if msg != tc.wantMsg {
				t.Fatalf("unexpected message, got %q want %q", msg, tc.wantMsg)
			}
		})
	}
}
