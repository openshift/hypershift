package validations

import (
	"crypto/x509/pkix"
	"testing"

	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	"github.com/openshift/hypershift/support/certs"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/component-base/featuregate"
	fgtesting "k8s.io/component-base/featuregate/testing"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stretchr/testify/require"
)

func TestValidateAuthenticationSpec(t *testing.T) {
	type testcase struct {
		name                 string
		authentication       *configv1.AuthenticationSpec
		serviceAccountIssuer string
		shouldError          bool
		featureGates         []featuregate.Feature
	}

	testcases := []testcase{
		{
			name: "When authentication is not configured, it should succeed",
		},
		{
			name: "When authentication type is None, it should succeed",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeNone,
			},
		},
		{
			name: "When authentication type is IntegratedOAuth, it should succeed",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeIntegratedOAuth,
			},
		},
		{
			name: "When authentication type is unknown, it should return an error",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationType("Unknown"),
			},
			shouldError: true,
		},
		{
			name: "When valid OIDC authentication config is provided, it should succeed",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "foo",
						Issuer: configv1.TokenIssuer{
							URL: "https://foo.io/auth",
							CertificateAuthority: configv1.ConfigMapNameReference{
								Name: "",
							},
							Audiences: []configv1.TokenAudience{
								"bar",
								"baz",
							},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoPrefix,
								Claim:        "email",
							},
							Groups: configv1.PrefixedClaimMapping{
								TokenClaimMapping: configv1.TokenClaimMapping{
									Claim: "groups",
								},
								Prefix: "group-prefix:",
							},
							UID: &configv1.TokenClaimOrExpressionMapping{
								Claim: "groups",
							},
							Extra: []configv1.ExtraMapping{
								{
									Key:             "foo.io/role",
									ValueExpression: "claims.role",
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name: "When invalid OIDC authentication config is provided, it should return an error",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "foo",
						Issuer: configv1.TokenIssuer{
							// Invalid URL
							URL: "https://A&!@^*(#&!(*@$^&$Y",
							CertificateAuthority: configv1.ConfigMapNameReference{
								Name: "",
							},
							// Duplicate audiences
							Audiences: []configv1.TokenAudience{
								"bar",
								"bar",
							},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoPrefix,
								Claim:        "email",
							},
							Groups: configv1.PrefixedClaimMapping{
								TokenClaimMapping: configv1.TokenClaimMapping{
									Claim: "groups",
								},
								Prefix: "group-prefix:",
							},
							UID: &configv1.TokenClaimOrExpressionMapping{
								Claim: "groups",
							},
							Extra: []configv1.ExtraMapping{
								{
									// reserved key
									Key:             "kubernetes.io/role",
									ValueExpression: "claims.role",
								},
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name: "When the OIDC issuer overlaps with the service account issuer, it should return an error",
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
				OIDCProviders: []configv1.OIDCProvider{{
					Name: "test-provider",
					Issuer: configv1.TokenIssuer{
						URL: "https://service-account-issuer.example.com",
					},
					ClaimMappings: configv1.TokenClaimMappings{
						Username: configv1.UsernameClaimMapping{
							Claim:        "sub",
							PrefixPolicy: configv1.NoPrefix,
						},
					},
				}},
			},
			serviceAccountIssuer: "https://service-account-issuer.example.com",
			shouldError:          true,
		},
		{
			name: "When OIDC provider has CEL validation and username expression, it should succeed",
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test-provider",
						Issuer: configv1.TokenIssuer{
							URL: "https://test.example.com",
							Audiences: []configv1.TokenAudience{
								"test-audience",
							},
						},
						ClaimValidationRules: []configv1.TokenClaimValidationRule{
							{
								Type: configv1.TokenValidationRuleTypeCEL,
								CEL: configv1.TokenClaimValidationCELRule{
									Expression: "claims.email_verified == true",
									Message:    "email must be verified",
								},
							},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								Expression: "has(claims.email) ? claims.email : claims.sub",
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name: "When OIDC provider has invalid CEL expression syntax, it should return an error",
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC,
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test-provider",
						Issuer: configv1.TokenIssuer{
							URL: "https://test.example.com",
							Audiences: []configv1.TokenAudience{
								"test-audience",
							},
						},
						ClaimValidationRules: []configv1.TokenClaimValidationRule{
							{
								Type: configv1.TokenValidationRuleTypeCEL,
								CEL: configv1.TokenClaimValidationCELRule{
									Expression: "invalid CEL syntax!!!",
									Message:    "this should fail",
								},
							},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								Claim:        "email",
								PrefixPolicy: configv1.NoPrefix,
							},
						},
					},
				},
			},
			shouldError: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.featureGates) > 0 {
				for _, feature := range tc.featureGates {
					fgtesting.SetFeatureGateDuringTest(t, featuregates.Gate(), feature, true)
				}
			}
			err := ValidateAuthenticationSpec(t.Context(), nil, tc.authentication, "foo", tc.serviceAccountIssuer)
			require.Equal(t, err != nil, tc.shouldError, "expected error state mismatch", "expected an error?", tc.shouldError, "received", err)
		})
	}
}

func TestValidateAuthenticationSpecExternalOIDCAsWebhook(t *testing.T) {
	fgtesting.SetFeatureGateDuringTest(t, featuregates.Gate(), featuregates.ExternalOIDCAsWebhook, true)

	newAuthentication := func(issuerURL string) *configv1.AuthenticationSpec {
		return &configv1.AuthenticationSpec{
			Type: configv1.AuthenticationTypeOIDC,
			OIDCProviders: []configv1.OIDCProvider{{
				Name: "test-provider",
				Issuer: configv1.TokenIssuer{
					URL: issuerURL,
				},
				ClaimMappings: configv1.TokenClaimMappings{
					Username: configv1.UsernameClaimMapping{
						Claim:        "sub",
						PrefixPolicy: configv1.NoPrefix,
					},
				},
			}},
		}
	}

	t.Run("validates webhook authentication configuration", func(t *testing.T) {
		authn := newAuthentication("https://issuer.example.com")
		err := ValidateAuthenticationSpec(t.Context(), nil, authn, "test", "https://service-account-issuer.example.com")
		require.NoError(t, err)
		require.Empty(t, authn.ServiceAccountIssuer, "validation must not mutate the supplied authentication configuration")
	})

	t.Run("validates a referenced certificate authority", func(t *testing.T) {
		_, certificateAuthority, err := certs.GenerateSelfSignedCertificate(&certs.CertCfg{
			IsCA:    true,
			Subject: pkix.Name{CommonName: "test-ca", OrganizationalUnit: []string{"test"}},
		})
		require.NoError(t, err)

		scheme := runtime.NewScheme()
		require.NoError(t, corev1.AddToScheme(scheme))
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "issuer-ca", Namespace: "test"},
			Data: map[string]string{
				certs.UserCABundleMapKey: string(certs.CertToPem(certificateAuthority)),
			},
		}).Build()

		authn := newAuthentication("https://issuer.example.com")
		authn.OIDCProviders[0].Issuer.CertificateAuthority.Name = "issuer-ca"
		require.NoError(t, ValidateAuthenticationSpec(t.Context(), client, authn, "test", ""))
	})

	t.Run("rejects an issuer that overlaps with the service account issuer", func(t *testing.T) {
		err := ValidateAuthenticationSpec(
			t.Context(),
			nil,
			newAuthentication("https://service-account-issuer.example.com"),
			"test",
			"https://service-account-issuer.example.com",
		)
		require.ErrorContains(t, err, "issuer url cannot overlap with the ServiceAccount issuer url")
	})

	t.Run("When a referenced certificate authority ConfigMap is missing, it should return an error", func(t *testing.T) {
		scheme := runtime.NewScheme()
		require.NoError(t, corev1.AddToScheme(scheme))
		client := fake.NewClientBuilder().WithScheme(scheme).Build()

		authn := newAuthentication("https://issuer.example.com")
		authn.OIDCProviders[0].Issuer.CertificateAuthority.Name = "missing-ca"
		err := ValidateAuthenticationSpec(t.Context(), client, authn, "test", "")
		require.ErrorContains(t, err, `failed to get CA configmap "missing-ca"`)
	})

	t.Run("When an OIDC provider has an invalid CEL expression, it should return an error", func(t *testing.T) {
		authn := newAuthentication("https://issuer.example.com")
		authn.OIDCProviders[0].ClaimValidationRules = []configv1.TokenClaimValidationRule{{
			Type: configv1.TokenValidationRuleTypeCEL,
			CEL: configv1.TokenClaimValidationCELRule{
				Expression: "invalid CEL syntax!!!",
				Message:    "this should fail",
			},
		}}

		err := ValidateAuthenticationSpec(t.Context(), nil, authn, "test", "")
		require.Error(t, err)
	})
}
