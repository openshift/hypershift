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

func TestIsPublicWithExternalDNS(t *testing.T) {
	tests := []struct {
		name                 string
		hcp                  *hyperv1.HostedControlPlane
		defaultIngressDomain string
		want                 bool
	}{
		{
			name: "public HCP with external DNS hostname",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.NonePlatform,
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "oauth.external-domain.com",
								},
							},
						},
					},
				},
			},
			defaultIngressDomain: "apps.mgmt-cluster.example.com",
			want:                 true,
		},
		{
			name: "public HCP with hostname under apps domain",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.NonePlatform,
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "oauth.apps.mgmt-cluster.example.com",
								},
							},
						},
						{
							Service: hyperv1.Konnectivity,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "konnectivity.apps.mgmt-cluster.example.com",
								},
							},
						},
					},
				},
			},
			defaultIngressDomain: "apps.mgmt-cluster.example.com",
			want:                 false,
		},
		{
			name: "public HCP with mixed hostnames - one external",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.NonePlatform,
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "oauth.apps.mgmt-cluster.example.com",
								},
							},
						},
						{
							Service: hyperv1.Konnectivity,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "konnectivity.external.com",
								},
							},
						},
					},
				},
			},
			defaultIngressDomain: "apps.mgmt-cluster.example.com",
			want:                 true,
		},
		{
			name: "private AWS HCP - not public",
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
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "oauth.external-domain.com",
								},
							},
						},
					},
				},
			},
			defaultIngressDomain: "apps.mgmt-cluster.example.com",
			want:                 false,
		},
		{
			name: "public HCP with no hostname set",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.NonePlatform,
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type:  hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{},
							},
						},
					},
				},
			},
			defaultIngressDomain: "apps.mgmt-cluster.example.com",
			want:                 false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPublicWithExternalDNS(tt.hcp, tt.defaultIngressDomain); got != tt.want {
				t.Errorf("IsPublicWithExternalDNS() = %v, want %v", got, tt.want)
			}
		})
	}
}
