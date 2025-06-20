package globalconfig

import (
	"fmt"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
)

func TestReconcileAuthenticationConfiguration(t *testing.T) {
	type args struct {
		authentication *configv1.Authentication
		config         *hyperv1.ClusterConfiguration
		issuerURL      string
	}
	tests := []struct {
		name       string
		args       args
		secretName string
	}{
		{
			name: "OIDC client secret specified",
			args: args{
				authentication: AuthenticationConfiguration(),
				config: &hyperv1.ClusterConfiguration{
					Authentication: &configv1.AuthenticationSpec{
						Type: configv1.AuthenticationTypeOIDC,
						OIDCProviders: []configv1.OIDCProvider{
							{
								OIDCClients: []configv1.OIDCClientConfig{
									{
										ComponentName: "test-client",
										ClientSecret: configv1.SecretNameReference{
											Name: "test-client-secret",
										},
									},
								},
							},
						},
					},
				},
				issuerURL: "https://example.com/issuer",
			},
			secretName: "test-client-secret",
		},
		{
			name: "OIDC client secret not specified",
			args: args{
				authentication: AuthenticationConfiguration(),
				config: &hyperv1.ClusterConfiguration{
					Authentication: &configv1.AuthenticationSpec{
						Type: configv1.AuthenticationTypeOIDC,
						OIDCProviders: []configv1.OIDCProvider{
							{
								OIDCClients: []configv1.OIDCClientConfig{
									{
										ComponentName: "test-client",
										ClientSecret: configv1.SecretNameReference{
											Name: "",
										},
									},
								},
							},
						},
					},
				},
				issuerURL: "https://example.com/issuer",
			},
			secretName: fmt.Sprintf("%s-%s", "test-client", postInstallClientSecretSuffix),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = ReconcileAuthenticationConfiguration(tt.args.authentication, tt.args.config, tt.args.issuerURL)
			if len(tt.args.authentication.Spec.OIDCProviders) == 0 {
				t.Errorf("Expected OIDCProviders to be set, got none")
			}
			if len(tt.args.authentication.Spec.OIDCProviders[0].OIDCClients) == 0 {
				t.Errorf("Expected OIDCClients to be set, got none")
			}
			if tt.args.authentication.Spec.OIDCProviders[0].OIDCClients[0].ClientSecret.Name != tt.secretName {
				t.Errorf("Expected OIDC client secret name %s, got %s", tt.secretName, tt.args.authentication.Spec.OIDCProviders[0].OIDCClients[0].ClientSecret.Name)
			}
			if tt.args.authentication.Spec.ServiceAccountIssuer != tt.args.issuerURL {
				t.Errorf("Expected ServiceAccountIssuer %s, got %s", tt.args.issuerURL, tt.args.authentication.Spec.ServiceAccountIssuer)
			}
		})
	}
}
