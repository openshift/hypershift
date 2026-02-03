package oauth

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"
	osinv1 "github.com/openshift/api/osin/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestAdaptOAuthConfigWithHTPasswd verifies that HTPasswd IDP is properly
// configured in the OAuth server config
func TestAdaptOAuthConfigWithHTPasswd(t *testing.T) {
	testCases := []struct {
		name              string
		identityProviders []configv1.IdentityProvider
		idpSecrets        []*corev1.Secret
		expectError       bool
		expectedIDPCount  int
	}{
		{
			name: "When HTPasswd IDP is configured with secret, it should add IDP to config",
			identityProviders: []configv1.IdentityProvider{
				{
					Name: "htpasswd-idp",
					IdentityProviderConfig: configv1.IdentityProviderConfig{
						Type: configv1.IdentityProviderTypeHTPasswd,
						HTPasswd: &configv1.HTPasswdIdentityProvider{
							FileData: configv1.SecretNameReference{
								Name: "htpasswd-secret",
							},
						},
					},
				},
			},
			idpSecrets: []*corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "htpasswd-secret",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"htpasswd": []byte("admin:$apr1$xyz"),
					},
				},
			},
			expectError:      false,
			expectedIDPCount: 1,
		},
		{
			name: "When multiple IDPs are configured, all should be added to config",
			identityProviders: []configv1.IdentityProvider{
				{
					Name: "htpasswd-idp",
					IdentityProviderConfig: configv1.IdentityProviderConfig{
						Type: configv1.IdentityProviderTypeHTPasswd,
						HTPasswd: &configv1.HTPasswdIdentityProvider{
							FileData: configv1.SecretNameReference{
								Name: "htpasswd-secret",
							},
						},
					},
				},
				{
					Name: "github-idp",
					IdentityProviderConfig: configv1.IdentityProviderConfig{
						Type: configv1.IdentityProviderTypeGitHub,
						GitHub: &configv1.GitHubIdentityProvider{
							ClientID: "test-client",
							ClientSecret: configv1.SecretNameReference{
								Name: "github-secret",
							},
						},
					},
				},
			},
			idpSecrets: []*corev1.Secret{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "htpasswd-secret",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"htpasswd": []byte("admin:$apr1$xyz"),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "github-secret",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"clientSecret": []byte("github-secret-value"),
					},
				},
			},
			expectError:      false,
			expectedIDPCount: 2,
		},
		{
			name:              "When no IDPs are configured, config should have no IDPs",
			identityProviders: []configv1.IdentityProvider{},
			idpSecrets:        []*corev1.Secret{},
			expectError:       false,
			expectedIDPCount:  0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			// Create fake client with IDP secrets
			fakeClientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			for _, secret := range tc.idpSecrets {
				fakeClientBuilder.WithObjects(secret)
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						OAuth: &configv1.OAuthSpec{
							IdentityProviders: tc.identityProviders,
						},
					},
				},
				Status: hyperv1.HostedControlPlaneStatus{
					ControlPlaneEndpoint: hyperv1.APIEndpoint{
						Host: "api.test.example.com",
						Port: 6443,
					},
				},
			}

			cpContext := component.WorkloadContext{
				Client: fakeClientBuilder.Build(),
				HCP:    hcp,
				InfraStatus: infra.InfrastructureStatus{
					OAuthHost: "oauth.test.example.com",
					OAuthPort: 443,
				},
			}

			// Create a minimal OAuth config
			cfg := &osinv1.OsinServerConfig{
				GenericAPIServerConfig: configv1.GenericAPIServerConfig{
					ServingInfo: configv1.HTTPServingInfo{},
				},
				OAuthConfig: osinv1.OAuthConfig{},
			}

			// Adapt the config
			err := adaptOAuthConfig(cpContext, cfg)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).ToNot(HaveOccurred())

			// Verify the correct number of IDPs were added
			g.Expect(len(cfg.OAuthConfig.IdentityProviders)).To(Equal(tc.expectedIDPCount),
				"Expected %d identity providers but found %d", tc.expectedIDPCount, len(cfg.OAuthConfig.IdentityProviders))

			// If IDPs are expected, verify they are properly configured
			if tc.expectedIDPCount > 0 {
				idpNames := make(map[string]bool)
				for _, idp := range cfg.OAuthConfig.IdentityProviders {
					idpNames[idp.Name] = true
				}

				// Verify all configured IDPs are present
				for _, expectedIDP := range tc.identityProviders {
					g.Expect(idpNames).To(HaveKey(expectedIDP.Name),
						"Expected IDP %s to be in config", expectedIDP.Name)
				}
			}
		})
	}
}
