package validations

import (
	"testing"

	"github.com/openshift/hypershift/control-plane-operator/featuregates"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/component-base/featuregate"
	fgtesting "k8s.io/component-base/featuregate/testing"

	"github.com/stretchr/testify/require"
)

func TestValidateAuthenticationSpec(t *testing.T) {
	type testcase struct {
		name           string
		authentication *configv1.AuthenticationSpec
		shouldError    bool
		featureGates   []featuregate.Feature
	}

	testcases := []testcase{
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
			err := ValidateAuthenticationSpec(t.Context(), nil, tc.authentication, "foo", []string{})
			require.Equal(t, err != nil, tc.shouldError, "expected error state mismatch", "expected an error?", tc.shouldError, "received", err)
		})
	}
}
