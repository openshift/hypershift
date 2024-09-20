package util

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
)

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
