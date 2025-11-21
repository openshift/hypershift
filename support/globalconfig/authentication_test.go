package globalconfig

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/stretchr/testify/require"
)

func TestReconcileAuthenticationConfiguration(t *testing.T) {
	testCases := []struct {
		name                  string
		existingAuth          *configv1.Authentication
		config                *hyperv1.ClusterConfiguration
		issuerURL             string
		expectedType          configv1.AuthenticationType
		expectedOIDCProviders int
		expectedServiceIssuer string
	}{
		{
			name:                  "When OIDC config is removed, it should explicitly set IntegratedOAuth type (not empty string)",
			existingAuth:          AuthenticationConfiguration(),
			config:                nil,
			issuerURL:             "https://test-issuer.com",
			expectedType:          configv1.AuthenticationTypeIntegratedOAuth,
			expectedOIDCProviders: 0,
			expectedServiceIssuer: "https://test-issuer.com",
		},
		{
			name: "When transitioning from OIDC to no OIDC, Type must be explicitly IntegratedOAuth",
			existingAuth: &configv1.Authentication{
				Spec: configv1.AuthenticationSpec{
					Type: configv1.AuthenticationTypeOIDC,
					OIDCProviders: []configv1.OIDCProvider{
						{Name: "old-provider"},
					},
				},
			},
			config:                nil,
			issuerURL:             "https://test-issuer.com",
			expectedType:          configv1.AuthenticationTypeIntegratedOAuth,
			expectedOIDCProviders: 0,
			expectedServiceIssuer: "https://test-issuer.com",
		},
		{
			name:         "When OIDC config is provided, it should use the provided spec",
			existingAuth: AuthenticationConfiguration(),
			config: &hyperv1.ClusterConfiguration{
				Authentication: &configv1.AuthenticationSpec{
					Type: configv1.AuthenticationTypeOIDC,
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test-provider",
							Issuer: configv1.TokenIssuer{
								URL: "https://oidc-provider.com",
							},
						},
					},
				},
			},
			issuerURL:             "https://test-issuer.com",
			expectedType:          configv1.AuthenticationTypeOIDC,
			expectedOIDCProviders: 1,
			expectedServiceIssuer: "https://test-issuer.com",
		},
		{
			name:         "When config exists but Authentication is nil, it should set IntegratedOAuth",
			existingAuth: AuthenticationConfiguration(),
			config: &hyperv1.ClusterConfiguration{
				Authentication: nil,
			},
			issuerURL:             "https://test-issuer.com",
			expectedType:          configv1.AuthenticationTypeIntegratedOAuth,
			expectedOIDCProviders: 0,
			expectedServiceIssuer: "https://test-issuer.com",
		},
		{
			name:         "ServiceAccountIssuer should be preserved when switching to IntegratedOAuth",
			existingAuth: AuthenticationConfiguration(),
			config:       nil,
			issuerURL:    "https://different-issuer.com",
			expectedType: configv1.AuthenticationTypeIntegratedOAuth,
			// The key fix: Type should NOT be empty string, it should be IntegratedOAuth
			expectedOIDCProviders: 0,
			expectedServiceIssuer: "https://different-issuer.com",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ReconcileAuthenticationConfiguration(tc.existingAuth, tc.config, tc.issuerURL)

			require.NoError(t, err)
			require.Equal(t, tc.expectedType, tc.existingAuth.Spec.Type,
				"Authentication Type should be set correctly (not empty string)")
			require.NotEmpty(t, tc.existingAuth.Spec.Type,
				"Type field must not be empty - authentication-operator needs explicit type")
			require.Equal(t, tc.expectedServiceIssuer, tc.existingAuth.Spec.ServiceAccountIssuer,
				"ServiceAccountIssuer should always be set")
			require.Len(t, tc.existingAuth.Spec.OIDCProviders, tc.expectedOIDCProviders,
				"OIDCProviders count should match expected")
		})
	}
}
