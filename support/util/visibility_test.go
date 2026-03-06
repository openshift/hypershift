package util

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type visibilityCase struct {
	name         string
	platformType hyperv1.PlatformType
	awsAccess    *hyperv1.AWSEndpointAccessType
	gcpAccess    *hyperv1.GCPEndpointAccessType
	wantPrivate  bool
	wantPublic   bool
	setupEnv     func(t *testing.T)
	annotations  map[string]string
}

func awsAccess(access hyperv1.AWSEndpointAccessType) *hyperv1.AWSEndpointAccessType {
	return &access
}

func gcpAccess(access hyperv1.GCPEndpointAccessType) *hyperv1.GCPEndpointAccessType {
	return &access
}

func baseVisibilityCases() []visibilityCase {
	return []visibilityCase{
		{
			name:         "When AWS endpoint is public it should be public and not private",
			platformType: hyperv1.AWSPlatform,
			awsAccess:    awsAccess(hyperv1.Public),
			wantPrivate:  false,
			wantPublic:   true,
		},
		{
			name:         "When AWS endpoint is public and private it should be public and private",
			platformType: hyperv1.AWSPlatform,
			awsAccess:    awsAccess(hyperv1.PublicAndPrivate),
			wantPrivate:  true,
			wantPublic:   true,
		},
		{
			name:         "When AWS endpoint is private it should be private and not public",
			platformType: hyperv1.AWSPlatform,
			awsAccess:    awsAccess(hyperv1.Private),
			wantPrivate:  true,
			wantPublic:   false,
		},
		{
			name:         "When GCP endpoint is private it should be private and not public",
			platformType: hyperv1.GCPPlatform,
			gcpAccess:    gcpAccess(hyperv1.GCPEndpointAccessPrivate),
			wantPrivate:  true,
			wantPublic:   false,
		},
		{
			name:         "When GCP endpoint is public and private it should be public and private",
			platformType: hyperv1.GCPPlatform,
			gcpAccess:    gcpAccess(hyperv1.GCPEndpointAccessPublicAndPrivate),
			wantPrivate:  true,
			wantPublic:   true,
		},
		{
			name:         "When is ARO with no Swift annotation (CI) it should not be private",
			platformType: hyperv1.NonePlatform,
			setupEnv: func(t *testing.T) {
				t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
			},
			wantPrivate: false,
			wantPublic:  true,
		},
		{
			name:         "When is ARO with Swift it should be public and private",
			platformType: hyperv1.NonePlatform,
			setupEnv: func(t *testing.T) {
				t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
			},
			annotations: map[string]string{
				hyperv1.SwiftPodNetworkInstanceAnnotation: "test-swift-instance",
			},
			wantPrivate: true,
			wantPublic:  true,
		},
	}
}

func platformSpecFromCase(tc visibilityCase) hyperv1.PlatformSpec {
	spec := hyperv1.PlatformSpec{
		Type: tc.platformType,
	}
	if tc.awsAccess != nil {
		spec.AWS = &hyperv1.AWSPlatformSpec{
			EndpointAccess: *tc.awsAccess,
		}
	}
	if tc.gcpAccess != nil {
		spec.GCP = &hyperv1.GCPPlatformSpec{
			EndpointAccess: *tc.gcpAccess,
		}
	}
	return spec
}

func hcpFromCase(tc visibilityCase) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: tc.annotations,
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: platformSpecFromCase(tc),
		},
	}
}

func hcFromCase(tc visibilityCase) *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: tc.annotations,
		},
		Spec: hyperv1.HostedClusterSpec{
			Platform: platformSpecFromCase(tc),
		},
	}
}

func TestIsPrivateHCP(t *testing.T) {
	for _, tc := range baseVisibilityCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setupEnv != nil {
				tc.setupEnv(t)
			}
			if got := IsPrivateHCP(hcpFromCase(tc)); got != tc.wantPrivate {
				t.Errorf("IsPrivateHCP() = %v, want %v", got, tc.wantPrivate)
			}
		})
	}
}

func TestIsPublicHCP(t *testing.T) {
	for _, tc := range baseVisibilityCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setupEnv != nil {
				tc.setupEnv(t)
			}
			if got := IsPublicHCP(hcpFromCase(tc)); got != tc.wantPublic {
				t.Errorf("IsPublicHCP() = %v, want %v", got, tc.wantPublic)
			}
		})
	}
}

func TestIsPrivateHC(t *testing.T) {
	for _, tc := range baseVisibilityCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setupEnv != nil {
				tc.setupEnv(t)
			}
			if got := IsPrivateHC(hcFromCase(tc)); got != tc.wantPrivate {
				t.Errorf("IsPrivateHC() = %v, want %v", got, tc.wantPrivate)
			}
		})
	}
}

func TestIsPublicHC(t *testing.T) {
	for _, tc := range baseVisibilityCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setupEnv != nil {
				tc.setupEnv(t)
			}
			if got := IsPublicHC(hcFromCase(tc)); got != tc.wantPublic {
				t.Errorf("IsPublicHC() = %v, want %v", got, tc.wantPublic)
			}
		})
	}
}

func TestLabelHCPRoutes(t *testing.T) {
	tests := []struct {
		name    string
		hcp     *hyperv1.HostedControlPlane
		want    bool
		envVars map[string]string
	}{
		// Shared Ingress Tests
		{
			name: "When shared ingress is active (ARO HCP), it should always label routes regardless of platform",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
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
			want:    true,
			envVars: map[string]string{"MANAGED_SERVICE": "ARO-HCP"},
		},

		// AWS Platform Tests
		{
			name: "When AWS cluster is Private, it should label routes for HCP router",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							EndpointAccess: hyperv1.Private,
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
			want: true,
		},
		{
			name: "When AWS cluster is PublicAndPrivate with KAS LoadBalancer, it should not label routes for HCP router",
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
			want: false,
		},
		{
			name: "When AWS cluster is PublicAndPrivate with KAS Route and hostname, it should label routes for HCP router",
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
			name: "When AWS cluster is Public with KAS LoadBalancer, it should not label routes for HCP router",
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
								Type: hyperv1.LoadBalancer,
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "When AWS cluster is Public with KAS Route and hostname, it should label routes for HCP router",
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
			name: "When AWS cluster is Public with KAS Route but no hostname, it should not label routes for HCP router",
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
							},
						},
					},
				},
			},
			want: false,
		},

		// GCP Platform Tests
		{
			name: "When GCP cluster is Private, it should label routes for HCP router",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.GCPPlatform,
						GCP: &hyperv1.GCPPlatformSpec{
							EndpointAccess: hyperv1.GCPEndpointAccessPrivate,
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
			want: true,
		},
		{
			name: "When GCP cluster is PublicAndPrivate with KAS LoadBalancer, it should not label routes for HCP router",
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
								Type: hyperv1.LoadBalancer,
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "When GCP cluster is PublicAndPrivate with KAS Route and hostname, it should label routes for HCP router",
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
									Hostname: "api.gcp.example.com",
								},
							},
						},
					},
				},
			},
			want: true,
		},

		// Agent Platform Tests (Bare Metal) - Critical for OCPBUGS-70152
		{
			name: "When Agent cluster has KAS LoadBalancer, it should not label routes for HCP router",
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
			want: false,
		},
		{
			name: "When Agent cluster has KAS Route with hostname, it should label routes for HCP router",
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
									Hostname: "api.agent.example.com",
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "When Agent cluster has KAS NodePort, it should not label routes for HCP router",
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
								Type: hyperv1.NodePort,
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "When Agent cluster has OAuth Route with hostname but KAS LoadBalancer, it should not label routes for HCP router (OCPBUGS-70152 bug scenario)",
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
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "oauth.agent.example.com",
								},
							},
						},
					},
				},
			},
			want: false, // Should NOT create HCP router - this was the bug
		},

		// KubeVirt Platform Tests
		{
			name: "When KubeVirt cluster has KAS LoadBalancer, it should not label routes for HCP router",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "kubevirt-cluster",
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
			want: false,
		},
		{
			name: "When KubeVirt cluster has KAS Route with hostname, it should label routes for HCP router",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.KubevirtPlatform,
						Kubevirt: &hyperv1.KubevirtPlatformSpec{
							GenerateID: "kubevirt-cluster",
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "api.kubevirt.example.com",
								},
							},
						},
					},
				},
			},
			want: true,
		},

		// OpenStack Platform Tests
		{
			name: "When OpenStack cluster has KAS LoadBalancer, it should not label routes for HCP router",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.OpenStackPlatform,
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name: "openstack-creds",
							},
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
			want: false,
		},
		{
			name: "When OpenStack cluster has KAS Route with hostname, it should label routes for HCP router",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.OpenStackPlatform,
						OpenStack: &hyperv1.OpenStackPlatformSpec{
							IdentityRef: hyperv1.OpenStackIdentityReference{
								Name: "openstack-creds",
							},
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "api.openstack.example.com",
								},
							},
						},
					},
				},
			},
			want: true,
		},

		// IBM Cloud Platform Tests
		{
			name: "When IBM Cloud cluster has KAS LoadBalancer, it should not label routes for HCP router",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
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
			want: false,
		},
		{
			name: "When IBM Cloud cluster has KAS Route with hostname, it should not label routes for HCP router",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.IBMCloudPlatform,
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "api.ibmcloud.example.com",
								},
							},
						},
					},
				},
			},
			want: false, // IBM Cloud never uses HCP router
		},

		// PowerVS Platform Tests
		{
			name: "When PowerVS cluster has KAS LoadBalancer, it should not label routes for HCP router",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.PowerVSPlatform,
						PowerVS: &hyperv1.PowerVSPlatformSpec{
							AccountID:      "test-account",
							CISInstanceCRN: "test-crn",
							ResourceGroup:  "test-rg",
							Region:         "us-south",
							Zone:           "us-south-1",
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
			want: false,
		},
		{
			name: "When PowerVS cluster has KAS Route with hostname, it should label routes for HCP router",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.PowerVSPlatform,
						PowerVS: &hyperv1.PowerVSPlatformSpec{
							AccountID:      "test-account",
							CISInstanceCRN: "test-crn",
							ResourceGroup:  "test-rg",
							Region:         "us-south",
							Zone:           "us-south-1",
						},
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "api.powervs.example.com",
								},
							},
						},
					},
				},
			},
			want: true,
		},

		// None Platform Tests
		{
			name: "When None platform cluster has KAS LoadBalancer, it should not label routes for HCP router",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.NonePlatform,
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
			want: false,
		},
		{
			name: "When None platform cluster has KAS Route with hostname, it should label routes for HCP router",
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
									Hostname: "api.none.example.com",
								},
							},
						},
					},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}
			got := LabelHCPRoutes(tt.hcp)
			if got != tt.want {
				t.Errorf("LabelHCPRoutes() = %v, want %v", got, tt.want)
			}
		})
	}
}
