package router

import (
	"os"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestUseHCPRouter(t *testing.T) {
	// Ensure shared ingress is not enabled for these tests
	os.Unsetenv("MANAGED_SERVICE")

	// Test platforms that should behave the same for public HCPs
	publicPlatforms := []hyperv1.PlatformType{
		hyperv1.NonePlatform,
		hyperv1.AgentPlatform,
		hyperv1.KubevirtPlatform,
	}

	tests := []struct {
		name                 string
		hcp                  *hyperv1.HostedControlPlane
		defaultIngressDomain string
		want                 bool
	}{
		{
			name: "AWS private - needs router",
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
			defaultIngressDomain: "apps.example.com",
			want:                 true,
		},
		{
			name: "AWS publicAndPrivate - needs router",
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
			defaultIngressDomain: "apps.example.com",
			want:                 true,
		},
		{
			name: "AWS public with apps domain hostname - no router needed",
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
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "oauth.apps.example.com",
								},
							},
						},
					},
				},
			},
			defaultIngressDomain: "apps.example.com",
			want:                 false,
		},
		{
			name: "AWS public with external DNS - needs router",
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
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "oauth.external.com",
								},
							},
						},
					},
				},
			},
			defaultIngressDomain: "apps.example.com",
			want:                 true,
		},
	}

	// Add platform-specific tests for public platforms with external DNS and apps domain hostnames
	for _, platform := range publicPlatforms {
		tests = append(tests,
			struct {
				name                 string
				hcp                  *hyperv1.HostedControlPlane
				defaultIngressDomain string
				want                 bool
			}{
				name: string(platform) + " with external DNS - needs router",
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: platform,
						},
						Services: []hyperv1.ServicePublishingStrategyMapping{
							{
								Service: hyperv1.OAuthServer,
								ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
									Type: hyperv1.Route,
									Route: &hyperv1.RoutePublishingStrategy{
										Hostname: "oauth.external.com",
									},
								},
							},
						},
					},
				},
				defaultIngressDomain: "apps.example.com",
				want:                 true,
			},
			struct {
				name                 string
				hcp                  *hyperv1.HostedControlPlane
				defaultIngressDomain string
				want                 bool
			}{
				name: string(platform) + " with apps domain hostname - no router needed",
				hcp: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: platform,
						},
						Services: []hyperv1.ServicePublishingStrategyMapping{
							{
								Service: hyperv1.OAuthServer,
								ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
									Type: hyperv1.Route,
									Route: &hyperv1.RoutePublishingStrategy{
										Hostname: "oauth.apps.example.com",
									},
								},
							},
						},
					},
				},
				defaultIngressDomain: "apps.example.com",
				want:                 false,
			},
		)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UseHCPRouter(tt.hcp, tt.defaultIngressDomain); got != tt.want {
				t.Errorf("UseHCPRouter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUseHCPRouterWithSharedIngress(t *testing.T) {
	// Set up shared ingress mode
	os.Setenv("MANAGED_SERVICE", string(hyperv1.AroHCP))
	defer os.Unsetenv("MANAGED_SERVICE")

	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					EndpointAccess: hyperv1.Private,
				},
			},
		},
	}

	// When shared ingress is enabled, UseHCPRouter should always return false
	if got := UseHCPRouter(hcp, "apps.example.com"); got != false {
		t.Errorf("UseHCPRouter() with shared ingress = %v, want false", got)
	}
}
