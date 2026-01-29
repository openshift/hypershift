package router

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
)

func TestUseHCPRouter(t *testing.T) {
	testsCases := []struct {
		name           string
		hcp            *hyperv1.HostedControlPlane
		expectedResult bool
	}{
		{
			name: "Provider is IBM Cloud",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "tenant1",
					Name:      "cluster1",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
					},
				},
			},
			expectedResult: false,
		},
		{
			name: "Provider is NonePlatform, services are exposed with Routes",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "tenant1",
					Name:      "cluster1",
				},
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
			expectedResult: true,
		},
		{
			name: "Provider is AWS, Public and Private, KAS Loadbalancer",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "tenant1",
					Name:      "cluster1",
				},
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
			expectedResult: true, // Router infrastructure needed for internal routes
		},
		{
			name: "Provider is AWS, Public and Private, KAS Route",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "tenant1",
					Name:      "cluster1",
				},
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
			expectedResult: true,
		},
		{
			name: "Provider is AWS, Private",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "tenant1",
					Name:      "cluster1",
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
			expectedResult: true,
		},
		{
			name: "Provider is Agent, KAS LoadBalancer",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "tenant1",
					Name:      "cluster1",
				},
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
			expectedResult: false, // Router infrastructure not needed when KAS uses LoadBalancer
		},
		{
			name: "Provider is Agent, KAS Route",
			hcp: &hyperv1.HostedControlPlane{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "tenant1",
					Name:      "cluster1",
				},
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
			expectedResult: true,
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectedResult, UseHCPRouter(tc.hcp))
		})
	}
}
