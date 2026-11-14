package integration

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestParamsFor(t *testing.T) {
	for _, tc := range []struct {
		name          string
		configuration *hyperv1.ClusterConfiguration
		expectedOAuth bool
	}{
		{name: "When the HCP has no configuration, it should enable OAuth", configuration: nil, expectedOAuth: true},
		{name: "When authentication is not configured, it should enable OAuth", configuration: &hyperv1.ClusterConfiguration{}, expectedOAuth: true},
		{name: "When authentication uses integrated OAuth, it should enable OAuth", configuration: &hyperv1.ClusterConfiguration{Authentication: &configv1.AuthenticationSpec{Type: configv1.AuthenticationTypeIntegratedOAuth}}, expectedOAuth: true},
		{name: "When authentication uses external OIDC, it should disable OAuth", configuration: &hyperv1.ClusterConfiguration{Authentication: &configv1.AuthenticationSpec{Type: configv1.AuthenticationTypeOIDC}}, expectedOAuth: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-example", Name: "example"}}
			hcp.Spec.Configuration = tc.configuration

			params := ParamsFor(hcp, "oauth.example.com", 6443)

			g.Expect(params).To(Equal(Params{Namespace: "clusters-example", OAuthEnabled: tc.expectedOAuth, OAuthAddress: "oauth.example.com", OAuthPort: 6443}))
		})
	}

	t.Run("When the HCP changes after params are built, it should keep the captured facts", func(t *testing.T) {
		g := NewWithT(t)
		hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters-example"}}

		params := ParamsFor(hcp, "oauth.example.com", 443)
		hcp.Namespace = "changed"
		hcp.Spec.Configuration = &hyperv1.ClusterConfiguration{Authentication: &configv1.AuthenticationSpec{Type: configv1.AuthenticationTypeOIDC}}

		g.Expect(params).To(Equal(Params{Namespace: "clusters-example", OAuthEnabled: true, OAuthAddress: "oauth.example.com", OAuthPort: 443}))
	})
}
