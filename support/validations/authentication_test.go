package validations

import (
	"context"
	"testing"

	configv1 "github.com/openshift/api/config/v1"

	"github.com/stretchr/testify/require"
)

func TestValidateAuthenticationSpec(t *testing.T) {
	type testcase struct {
		name           string
		authentication *configv1.AuthenticationSpec
		shouldError    bool
	}

	testcases := []testcase{
		{
			name: "valid OIDC authentication config",
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
								TokenClaimMapping: configv1.TokenClaimMapping{
									Claim: "email",
								},
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
			name: "invalid OIDC authentication config",
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
								TokenClaimMapping: configv1.TokenClaimMapping{
									Claim: "email",
								},
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
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAuthenticationSpec(context.TODO(), nil, tc.authentication, "foo", []string{})
			require.Equal(t, err != nil, tc.shouldError, "expected error state mismatch", "expected an error?", tc.shouldError, "received", err)
		})
	}
}
