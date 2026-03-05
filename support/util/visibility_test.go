package util

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestIsPrivateHCP(t *testing.T) {
	type args struct {
		hcp *hyperv1.HostedControlPlane
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "AWS Public",
			args: args{
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
			},
			want: false,
		},
		{
			name: "AWS PublicAndPrivate",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.AWSPlatform,
							AWS: &hyperv1.AWSPlatformSpec{
								EndpointAccess: hyperv1.PublicAndPrivate,
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "AWS Private",
			args: args{
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
			},
			want: true,
		},
		{
			name: "None",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.NonePlatform,
						},
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPrivateHCP(tt.args.hcp); got != tt.want {
				t.Errorf("IsPrivateHCP() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsPublicHCP(t *testing.T) {
	type args struct {
		hcp *hyperv1.HostedControlPlane
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "AWS Public",
			args: args{
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
			},
			want: true,
		},
		{
			name: "AWS PublicAndPrivate",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.AWSPlatform,
							AWS: &hyperv1.AWSPlatformSpec{
								EndpointAccess: hyperv1.PublicAndPrivate,
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "AWS Private",
			args: args{
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
			},
			want: false,
		},
		{
			name: "None",
			args: args{
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.NonePlatform,
						},
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPublicHCP(tt.args.hcp); got != tt.want {
				t.Errorf("IsPublicHCP() = %v, want %v", got, tt.want)
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
