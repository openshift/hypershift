package reconcilerpolicy

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
)

func TestConfigOAuthEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		authentication *configv1.AuthenticationSpec
		expected       bool
	}{
		{
			name:     "When authentication is absent, it should enable OAuth",
			expected: true,
		},
		{
			name:           "When authentication uses default values, it should enable OAuth",
			authentication: &configv1.AuthenticationSpec{},
			expected:       true,
		},
		{
			name: "When authentication uses integrated OAuth, it should enable OAuth",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
			},
			expected: true,
		},
		{
			name: "When authentication uses OIDC, it should disable OAuth",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			g.Expect(ConfigOAuthEnabled(tt.authentication)).To(Equal(tt.expected))
		})
	}
}

func TestHCOAuthEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		configuration *hyperv1.ClusterConfiguration
		expected      bool
	}{
		{
			name:     "When HostedCluster configuration is absent, it should enable OAuth",
			expected: true,
		},
		{
			name:          "When HostedCluster authentication is absent, it should enable OAuth",
			configuration: &hyperv1.ClusterConfiguration{},
			expected:      true,
		},
		{
			name: "When HostedCluster authentication uses OIDC, it should disable OAuth",
			configuration: &hyperv1.ClusterConfiguration{
				Authentication: &configv1.AuthenticationSpec{Type: configv1.AuthenticationTypeOIDC},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hc := &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{Configuration: tt.configuration}}
			g.Expect(HCOAuthEnabled(hc)).To(Equal(tt.expected))
		})
	}
}

func TestHCPOAuthEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		configuration *hyperv1.ClusterConfiguration
		expected      bool
	}{
		{
			name:     "When HostedControlPlane configuration is absent, it should enable OAuth",
			expected: true,
		},
		{
			name:          "When HostedControlPlane authentication is absent, it should enable OAuth",
			configuration: &hyperv1.ClusterConfiguration{},
			expected:      true,
		},
		{
			name: "When HostedControlPlane authentication uses OIDC, it should disable OAuth",
			configuration: &hyperv1.ClusterConfiguration{
				Authentication: &configv1.AuthenticationSpec{Type: configv1.AuthenticationTypeOIDC},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{Configuration: tt.configuration}}
			g.Expect(HCPOAuthEnabled(hcp)).To(Equal(tt.expected))
		})
	}
}
