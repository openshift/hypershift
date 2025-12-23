package util

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestIsSubdomain(t *testing.T) {
	tests := []struct {
		name     string
		hostname string
		domain   string
		want     bool
	}{
		{
			name:     "proper subdomain",
			hostname: "oauth.apps.example.com",
			domain:   "apps.example.com",
			want:     true,
		},
		{
			name:     "multi-level subdomain",
			hostname: "foo.bar.apps.example.com",
			domain:   "apps.example.com",
			want:     true,
		},
		{
			name:     "exact match is not a subdomain",
			hostname: "apps.example.com",
			domain:   "apps.example.com",
			want:     false,
		},
		{
			name:     "different domain",
			hostname: "oauth.external.com",
			domain:   "apps.example.com",
			want:     false,
		},
		{
			name:     "partial label match is not a subdomain",
			hostname: "foobar.apps.example.com",
			domain:   "bar.apps.example.com",
			want:     false,
		},
		{
			name:     "suffix match but not label boundary",
			hostname: "evilapps.example.com",
			domain:   "apps.example.com",
			want:     false,
		},
		{
			name:     "empty hostname",
			hostname: "",
			domain:   "apps.example.com",
			want:     false,
		},
		{
			name:     "empty domain",
			hostname: "oauth.apps.example.com",
			domain:   "",
			want:     false,
		},
		{
			name:     "case insensitive matching",
			hostname: "OAuth.Apps.Example.Com",
			domain:   "apps.example.com",
			want:     true,
		},
		{
			name:     "hostname shorter than domain",
			hostname: "example.com",
			domain:   "apps.example.com",
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSubdomain(tt.hostname, tt.domain); got != tt.want {
				t.Errorf("IsSubdomain(%q, %q) = %v, want %v", tt.hostname, tt.domain, got, tt.want)
			}
		})
	}
}

func TestUseDedicatedDNSWithExternalDomain(t *testing.T) {
	tests := []struct {
		name                 string
		hcp                  *hyperv1.HostedControlPlane
		svcType              hyperv1.ServiceType
		defaultIngressDomain string
		want                 bool
	}{
		{
			name: "route with hostname under apps domain",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
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
					},
				},
			},
			svcType:              hyperv1.OAuthServer,
			defaultIngressDomain: "apps.mgmt-cluster.example.com",
			want:                 false,
		},
		{
			name: "route with hostname external to apps domain",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
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
			svcType:              hyperv1.OAuthServer,
			defaultIngressDomain: "apps.mgmt-cluster.example.com",
			want:                 true,
		},
		{
			name: "route with no hostname",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
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
			svcType:              hyperv1.OAuthServer,
			defaultIngressDomain: "apps.mgmt-cluster.example.com",
			want:                 false,
		},
		{
			name: "LoadBalancer service type",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
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
			svcType:              hyperv1.APIServer,
			defaultIngressDomain: "apps.mgmt-cluster.example.com",
			want:                 false,
		},
		{
			name: "empty default ingress domain",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
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
			svcType:              hyperv1.OAuthServer,
			defaultIngressDomain: "",
			want:                 true,
		},
		{
			name:                 "service not found",
			hcp:                  &hyperv1.HostedControlPlane{},
			svcType:              hyperv1.OAuthServer,
			defaultIngressDomain: "apps.mgmt-cluster.example.com",
			want:                 false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UseDedicatedDNSWithExternalDomain(tt.hcp, tt.svcType, tt.defaultIngressDomain); got != tt.want {
				t.Errorf("UseDedicatedDNSWithExternalDomain() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsLBKASByHC(t *testing.T) {
	tests := []struct {
		description string
		hc          *hyperv1.HostedCluster
		expected    bool
	}{
		{
			description: "hc.spec.services is an empty array",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{},
				},
			},
			expected: false,
		},
		{
			description: "hc.spec.services does not contain an entry for KAS",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			description: "hc.spec.services contains an LB KAS entry",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
							},
						},
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			description: "hc.spec.services contains a Route KAS entry",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
							},
						},
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
							},
						},
					},
				},
			},
			expected: false,
		},
	}
	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			if res := IsLBKASByHC(test.hc); res != test.expected {
				t.Errorf("IsLBKASByHC() = %v, expected %v", res, test.expected)
			}
		})
	}
}
