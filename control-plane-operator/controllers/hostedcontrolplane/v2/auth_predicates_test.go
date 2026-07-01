package v2_test

import (
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	extoidc "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/external_oidc_webhook"
	oapi "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/oauth_apiserver"
	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	component "github.com/openshift/hypershift/support/controlplane-component"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/component-base/featuregate"
	fgtesting "k8s.io/component-base/featuregate/testing"
)

func TestAuthComponentPredicates(t *testing.T) {
	testCases := []struct {
		name                 string
		authType             configv1.AuthenticationType
		featureGates         []featuregate.Feature
		expectOAuthAPI       bool
		expectExtOIDCWebhook bool
	}{
		{
			name:                 "When auth type is empty and gate is off, it should enable oauth-apiserver only",
			authType:             "",
			expectOAuthAPI:       true,
			expectExtOIDCWebhook: false,
		},
		{
			name:                 "When auth type is empty and gate is on, it should enable oauth-apiserver only",
			authType:             "",
			featureGates:         []featuregate.Feature{featuregates.ExternalOIDCExternalClaimsSourcing},
			expectOAuthAPI:       true,
			expectExtOIDCWebhook: false,
		},
		{
			name:                 "When auth type is IntegratedOAuth and gate is off, it should enable oauth-apiserver only",
			authType:             configv1.AuthenticationTypeIntegratedOAuth,
			expectOAuthAPI:       true,
			expectExtOIDCWebhook: false,
		},
		{
			name:                 "When auth type is IntegratedOAuth and gate is on, it should enable oauth-apiserver only",
			authType:             configv1.AuthenticationTypeIntegratedOAuth,
			featureGates:         []featuregate.Feature{featuregates.ExternalOIDCExternalClaimsSourcing},
			expectOAuthAPI:       true,
			expectExtOIDCWebhook: false,
		},
		{
			name:                 "When auth type is OIDC and gate is off, it should disable both",
			authType:             configv1.AuthenticationTypeOIDC,
			expectOAuthAPI:       false,
			expectExtOIDCWebhook: false,
		},
		{
			name:                 "When auth type is OIDC and gate is on, it should enable external-oidc-webhook only",
			authType:             configv1.AuthenticationTypeOIDC,
			featureGates:         []featuregate.Feature{featuregates.ExternalOIDCExternalClaimsSourcing},
			expectOAuthAPI:       false,
			expectExtOIDCWebhook: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			for _, fg := range tc.featureGates {
				fgtesting.SetFeatureGateDuringTest(t, featuregates.Gate(), fg, true)
			}

			cpContext := component.WorkloadContext{HCP: hcpWithAuthType(tc.authType)}

			oauthResult, err := oapi.Predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(oauthResult).To(Equal(tc.expectOAuthAPI))

			extOIDCResult, err := extoidc.Predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(extOIDCResult).To(Equal(tc.expectExtOIDCWebhook))
		})
	}
}

func hcpWithAuthType(authType configv1.AuthenticationType) *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
		Spec: hyperv1.HostedControlPlaneSpec{
			Configuration: &hyperv1.ClusterConfiguration{
				Authentication: &configv1.AuthenticationSpec{
					Type: authType,
				},
			},
		},
	}
}
