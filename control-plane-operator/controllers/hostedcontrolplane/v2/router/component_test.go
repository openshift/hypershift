package router

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/router/util"
	"github.com/openshift/hypershift/support/azureutil"
	component "github.com/openshift/hypershift/support/controlplane-component"

	routev1 "github.com/openshift/api/route/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestUseHCPRouter(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		setupEnv func(t *testing.T)
		want     bool
	}{
		{
			name: "When platform is IBMCloud it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
					},
				},
			},
			want: false,
		},
		{
			name: "When ARO Swift is enabled it should return true because the HCP router handles routing",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.SwiftPodNetworkInstanceAnnotation: "test-instance",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
			setupEnv: func(t *testing.T) {
				azureutil.SetAsAroHCPTest(t)
			},
			want: true,
		},
		{
			name: "When ARO with no Swift annotation (CI) it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
				},
			},
			setupEnv: func(t *testing.T) {
				azureutil.SetAsAroHCPTest(t)
			},
			want: false,
		},
		{
			name: "When NonePlatform has services exposed with Routes it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.NonePlatform,
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "cluster1.api.tenant1.com",
								},
							},
						},
						{
							Service: hyperv1.Konnectivity,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "cluster1.tunnel.tenant1.com",
								},
							},
						},
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "cluster1.ignition.tenant1.com",
								},
							},
						},
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "cluster1.oauth.tenant1.com",
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "When AWS has private endpoint access it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.Private,
						},
					},
				},
			},
			want: true,
		},
		{
			name: "When AWS has public and private endpoint access with KAS LoadBalancer it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.PublicAndPrivate,
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
				},
			},
			want: true, // Router infrastructure needed for internal routes
		},
		{
			name: "When AWS has public and private endpoint access with KAS Route it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.PublicAndPrivate,
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "cluster1.api.tenant1.com",
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "When AWS has public endpoint access without DNS it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.Public,
						},
					},
				},
			},
			want: false,
		},
		{
			name: "When AWS has public endpoint access with DNS for APIServer it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.Public,
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "api.example.com",
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "When GCP has private endpoint access it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							EndpointAccess: hyperv1.GCPEndpointAccessPrivate,
						},
					},
				},
			},
			want: true,
		},
		{
			name: "When GCP has public and private endpoint access with KAS Route it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							EndpointAccess: hyperv1.GCPEndpointAccessPublicAndPrivate,
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "cluster1.api.tenant1.com",
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "When Agent platform has KAS LoadBalancer it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AgentPlatform,
						Agent: &hyperv1.AgentPlatformSpec{
							AgentNamespace: "agent-ns",
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
				},
			},
			want: false, // Router infrastructure not needed when KAS uses LoadBalancer
		},
		{
			name: "When Agent platform has KAS Route it should return true",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AgentPlatform,
						Agent: &hyperv1.AgentPlatformSpec{
							AgentNamespace: "agent-ns",
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "cluster1.api.tenant1.com",
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "When platform is None it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.NonePlatform,
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupEnv != nil {
				tt.setupEnv(t)
			}
			if got := util.UseHCPRouter(tt.hcp); got != tt.want {
				t.Errorf("UseHCPRouter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func aroHCP() *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
				Azure: &hyperv1.AzurePlatformSpec{
					Topology: hyperv1.AzureTopologyPrivate,
					AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
						AzureAuthenticationConfigType: hyperv1.AzureAuthenticationTypeManagedIdentities,
					},
				},
			},
		},
	}
}

func TestAroExpectedHCPRouterRouteNames(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected []string
	}{
		{
			name: "When HCP is not ARO, it should return nil",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			expected: nil,
		},
		{
			name: "When ARO HCP has no metrics forwarding, it should return base routes",
			hcp:  aroHCP(),
			expected: []string{
				"kube-apiserver-internal",
				"konnectivity-server",
				"oauth-internal",
				"ignition-server",
			},
		},
		{
			name: "When ARO HCP has metrics forwarding enabled, it should include metrics-proxy",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := aroHCP()
				hcp.Spec.Monitoring.MetricsForwarding.Mode = hyperv1.MetricsForwardingModeForward
				return hcp
			}(),
			expected: []string{
				"kube-apiserver-internal",
				"konnectivity-server",
				"oauth-internal",
				"ignition-server",
				"metrics-proxy",
			},
		},
		{
			name: "When ARO HCP has disabled monitoring, it should exclude metrics-proxy",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := aroHCP()
				hcp.Annotations = map[string]string{
					hyperv1.DisableMonitoringServices: "true",
				}
				hcp.Spec.Monitoring.MetricsForwarding.Mode = hyperv1.MetricsForwardingModeForward
				return hcp
			}(),
			expected: []string{
				"kube-apiserver-internal",
				"konnectivity-server",
				"oauth-internal",
				"ignition-server",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			got := aroExpectedHCPRouterRouteNames(tc.hcp)
			if tc.expected == nil {
				g.Expect(got).To(BeNil())
			} else {
				g.Expect(got).To(Equal(tc.expected))
			}
		})
	}
}

func TestMetricsProxyRouteRequired(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected bool
	}{
		{
			name: "When platform is not Azure, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			expected: false,
		},
		{
			name:     "When ARO HCP has no metrics forwarding, it should return false",
			hcp:      aroHCP(),
			expected: false,
		},
		{
			name: "When ARO HCP has metrics forwarding enabled, it should return true",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := aroHCP()
				hcp.Spec.Monitoring.MetricsForwarding.Mode = hyperv1.MetricsForwardingModeForward
				return hcp
			}(),
			expected: true,
		},
		{
			name: "When ARO HCP has disabled monitoring, it should return false",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := aroHCP()
				hcp.Annotations = map[string]string{
					hyperv1.DisableMonitoringServices: "true",
				}
				hcp.Spec.Monitoring.MetricsForwarding.Mode = hyperv1.MetricsForwardingModeForward
				return hcp
			}(),
			expected: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(metricsProxyRouteRequired(tc.hcp)).To(Equal(tc.expected))
		})
	}
}

func TestHcpRouterRouteReady(t *testing.T) {
	tests := []struct {
		name     string
		route    *routev1.Route
		expected bool
	}{
		{
			name: "When route has a host, it should be ready",
			route: &routev1.Route{
				Spec: routev1.RouteSpec{
					Host: "test.example.com",
				},
			},
			expected: true,
		},
		{
			name: "When route has no host, it should not be ready",
			route: &routev1.Route{
				Spec: routev1.RouteSpec{},
			},
			expected: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(hcpRouterRouteReady(tc.route)).To(Equal(tc.expected))
		})
	}
}

func TestEnsureHCPRouterRoutesExist(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := routev1.Install(scheme); err != nil {
		t.Fatalf("install Route scheme: %v", err)
	}

	readyRoute := func(name string) *routev1.Route {
		return &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "test-ns",
			},
			Spec: routev1.RouteSpec{
				Host: name + ".example.com",
			},
		}
	}

	tests := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		routes      []runtime.Object
		expectedErr string
	}{
		{
			name: "When all base routes are present and ready, it should succeed",
			hcp:  aroHCP(),
			routes: []runtime.Object{
				readyRoute("kube-apiserver-internal"),
				readyRoute("konnectivity-server"),
				readyRoute("oauth-internal"),
				readyRoute("ignition-server"),
			},
		},
		{
			name: "When ignition-server route is missing, it should return an error",
			hcp:  aroHCP(),
			routes: []runtime.Object{
				readyRoute("kube-apiserver-internal"),
				readyRoute("konnectivity-server"),
				readyRoute("oauth-internal"),
			},
			expectedErr: "waiting for HCP router routes: ignition-server",
		},
		{
			name: "When route exists but has no host, it should return an error",
			hcp:  aroHCP(),
			routes: []runtime.Object{
				readyRoute("kube-apiserver-internal"),
				readyRoute("konnectivity-server"),
				readyRoute("oauth-internal"),
				&routev1.Route{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ignition-server",
						Namespace: "test-ns",
					},
				},
			},
			expectedErr: "waiting for HCP router routes: ignition-server",
		},
		{
			name:        "When all routes are missing, it should return an error listing all",
			hcp:         aroHCP(),
			routes:      []runtime.Object{},
			expectedErr: "waiting for HCP router routes: kube-apiserver-internal, konnectivity-server, oauth-internal, ignition-server",
		},
		{
			name: "When metrics forwarding is enabled and metrics-proxy is missing, it should return an error",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := aroHCP()
				hcp.Spec.Monitoring.MetricsForwarding.Mode = hyperv1.MetricsForwardingModeForward
				return hcp
			}(),
			routes: []runtime.Object{
				readyRoute("kube-apiserver-internal"),
				readyRoute("konnectivity-server"),
				readyRoute("oauth-internal"),
				readyRoute("ignition-server"),
			},
			expectedErr: "waiting for HCP router routes: metrics-proxy",
		},
		{
			name: "When metrics forwarding is enabled and all routes are present, it should succeed",
			hcp: func() *hyperv1.HostedControlPlane {
				hcp := aroHCP()
				hcp.Spec.Monitoring.MetricsForwarding.Mode = hyperv1.MetricsForwardingModeForward
				return hcp
			}(),
			routes: []runtime.Object{
				readyRoute("kube-apiserver-internal"),
				readyRoute("konnectivity-server"),
				readyRoute("oauth-internal"),
				readyRoute("ignition-server"),
				readyRoute("metrics-proxy"),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(tc.routes...).
				Build()
			cpContext := component.WorkloadContext{
				Context: context.Background(),
				Client:  fakeClient,
				HCP:     tc.hcp,
			}
			err := ensureHCPRouterRoutesExist(cpContext)
			if tc.expectedErr == "" {
				g.Expect(err).ToNot(HaveOccurred())
			} else {
				g.Expect(err).To(MatchError(tc.expectedErr))
			}
		})
	}
}

func TestRouterPredicate(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := routev1.Install(scheme); err != nil {
		t.Fatalf("install Route scheme: %v", err)
	}

	readyRoute := func(name string) *routev1.Route {
		return &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "test-ns",
			},
			Spec: routev1.RouteSpec{
				Host: name + ".example.com",
			},
		}
	}

	tests := []struct {
		name      string
		hcp       *hyperv1.HostedControlPlane
		routes    []runtime.Object
		expected  bool
		expectErr bool
	}{
		{
			name: "When platform does not use HCP router, it should return false",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
					},
				},
			},
			expected: false,
		},
		{
			name: "When ARO HCP has all routes ready, it should return true",
			hcp:  aroHCP(),
			routes: []runtime.Object{
				readyRoute("kube-apiserver-internal"),
				readyRoute("konnectivity-server"),
				readyRoute("oauth-internal"),
				readyRoute("ignition-server"),
			},
			expected: true,
		},
		{
			name:      "When ARO HCP has missing routes, it should return false with error",
			hcp:       aroHCP(),
			routes:    []runtime.Object{},
			expected:  false,
			expectErr: true,
		},
		{
			name: "When AWS has private endpoint access, it should return true without route checks",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.Private,
						},
					},
				},
			},
			expected: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(tc.routes...).
				Build()
			cpContext := component.WorkloadContext{
				Context: context.Background(),
				Client:  fakeClient,
				HCP:     tc.hcp,
			}
			got, err := routerPredicate(cpContext)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(got).To(Equal(tc.expected))
		})
	}
}
