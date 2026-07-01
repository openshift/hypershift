package extoidc

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fgtesting "k8s.io/component-base/featuregate/testing"
)

func TestExternalOIDCWebhookOptions(t *testing.T) {
	t.Parallel()

	options := &externalOIDCWebhook{}

	t.Run("When checking IsRequestServing, it should return true", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		g.Expect(options.IsRequestServing()).To(BeTrue())
	})

	t.Run("When checking MultiZoneSpread, it should return true", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		g.Expect(options.MultiZoneSpread()).To(BeTrue())
	})

	t.Run("When checking NeedsManagementKASAccess, it should return false", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		g.Expect(options.NeedsManagementKASAccess()).To(BeFalse())
	})
}

func TestPredicate(t *testing.T) {
	testCases := []struct {
		name            string
		authentication  *configv1.AuthenticationSpec
		featureEnabled  bool
		expectedEnabled bool
	}{
		{
			name:            "When authentication is not configured, it should return false",
			featureEnabled:  true,
			expectedEnabled: false,
		},
		{
			name: "When authentication is IntegratedOAuth, it should return false",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
			},
			featureEnabled:  true,
			expectedEnabled: false,
		},
		{
			name: "When authentication is OIDC and the feature gate is disabled, it should return false",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
			},
			expectedEnabled: false,
		},
		{
			name: "When authentication is OIDC and the feature gate is enabled, it should return true",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
			},
			featureEnabled:  true,
			expectedEnabled: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			fgtesting.SetFeatureGateDuringTest(t, featuregates.Gate(), featuregates.ExternalOIDCAsWebhook, tc.featureEnabled)

			cpContext := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-hcp",
						Namespace: "test-ns",
					},
					Spec: hyperv1.HostedControlPlaneSpec{
						Configuration: &hyperv1.ClusterConfiguration{
							Authentication: tc.authentication,
						},
					},
				},
			}

			enabled, err := predicate(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(enabled).To(Equal(tc.expectedEnabled))
		})
	}
}
