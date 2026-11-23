package proxy

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
)

func TestAuthProxyOverride(t *testing.T) {
	override := operatorv1.AuthenticationProxyConfig{
		HTTPProxy:  "http://auth:8080",
		HTTPSProxy: "https://auth:8080",
		NoProxy:    []string{"kube-apiserver", "idp.example.com"},
		TrustedCA:  operatorv1.AuthenticationConfigMapReference{Name: "auth-ca"},
	}

	testCases := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected *operatorv1.AuthenticationProxyConfig
	}{
		{
			name:     "When the hosted control plane is nil, it should return nil",
			hcp:      nil,
			expected: nil,
		},
		{
			name:     "When no operator configuration is set, it should return nil",
			hcp:      &hyperv1.HostedControlPlane{},
			expected: nil,
		},
		{
			name: "When the authentication proxy is zero, it should return nil",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						Authentication: hyperv1.AuthenticationOperatorSpec{},
					},
				},
			},
			expected: nil,
		},
		{
			name: "When the override is set, it should return it as-is",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						Authentication: hyperv1.AuthenticationOperatorSpec{
							Proxy: override,
						},
					},
				},
			},
			expected: &override,
		},
		{
			name: "When the override carries only a trusted CA, it should return nil",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					OperatorConfiguration: &hyperv1.OperatorConfiguration{
						Authentication: hyperv1.AuthenticationOperatorSpec{
							Proxy: operatorv1.AuthenticationProxyConfig{
								TrustedCA: operatorv1.AuthenticationConfigMapReference{Name: "auth-ca"},
							},
						},
					},
				},
			},
			expected: nil,
		},
		{
			name: "When a cluster-wide proxy is set without an override, it should not be returned",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							HTTPProxy:  "http://cluster:3128",
							HTTPSProxy: "https://cluster:3128",
						},
					},
				},
			},
			expected: nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(AuthProxyOverride(tc.hcp)).To(Equal(tc.expected))
		})
	}
}
