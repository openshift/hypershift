package netutil

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestKasRouteHostname(t *testing.T) {
	tests := []struct {
		description string
		hcp         *hyperv1.HostedControlPlane
		expected    string
	}{
		{
			description: "When hcp has no APIServer service entry, it should return empty string",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{},
				},
			},
			expected: "",
		},
		{
			description: "When hcp has APIServer entry with nil Route, it should return empty string",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
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
			expected: "",
		},
		{
			description: "When hcp has APIServer Route with hostname, it should return the hostname",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "kas.example.com",
								},
							},
						},
					},
				},
			},
			expected: "kas.example.com",
		},
	}
	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			if res := KasRouteHostname(test.hcp); res != test.expected {
				t.Errorf("KasRouteHostname() = %q, expected %q", res, test.expected)
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
