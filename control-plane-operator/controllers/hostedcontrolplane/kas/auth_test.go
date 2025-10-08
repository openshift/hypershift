package kas

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	hcpconfig "github.com/openshift/hypershift/support/config"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/component-base/featuregate"
	fgtesting "k8s.io/component-base/featuregate/testing"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/google/go-cmp/cmp"
)

const caContents = `-----BEGIN CERTIFICATE-----
MIIGPTCCBCWgAwIBAgIUcmOOr6dehS2FldUvybAiFr+hMT4wDQYJKoZIhvcNAQEL
BQAwga0xCzAJBgNVBAYTAlVTMRcwFQYDVQQIDA5Ob3J0aCBDYXJvbGluYTEQMA4G
A1UEBwwHUmFsZWlnaDEQMA4GA1UECgwHUmVkIEhhdDESMBAGA1UECwwJT3BlblNo
aWZ0MSowKAYDVQQDDCFoeXBlcnNoaWZ0LnVuaXQudGVzdC5vcGVuc2hpZnQuaW8x
ITAfBgkqhkiG9w0BCQEWEmJwYWxtZXJAcmVkaGF0LmNvbTAeFw0yNTA0MTUxOTI1
NDZaFw0yNjA0MTUxOTI1NDZaMIGtMQswCQYDVQQGEwJVUzEXMBUGA1UECAwOTm9y
dGggQ2Fyb2xpbmExEDAOBgNVBAcMB1JhbGVpZ2gxEDAOBgNVBAoMB1JlZCBIYXQx
EjAQBgNVBAsMCU9wZW5TaGlmdDEqMCgGA1UEAwwhaHlwZXJzaGlmdC51bml0LnRl
c3Qub3BlbnNoaWZ0LmlvMSEwHwYJKoZIhvcNAQkBFhJicGFsbWVyQHJlZGhhdC5j
b20wggIiMA0GCSqGSIb3DQEBAQUAA4ICDwAwggIKAoICAQC/ia2VK/e1VK/sI4jm
bf1+ozOOgBCPvoDqWuxJn327i0Hhw7eBwz8H0BmwJZLvFRQ6L01Xu4ht73z4ajQ2
/F3SIbE9BBdamespILA4VZjDsRxXXlWZ8ZHR2+DcXlxsZp/xjFp5EErQIqM9LggZ
s5p1d2wkZQLb10XDztRazAuT1Jo5yqksoTxhOolqFccj+ePrht7EckFbgVtbD/e2
E+1SxsqSu393pOsLISbNLpKEaqzbLEBti6Hw8wdOtiwN1TdnVyFQH+1prG57Iaib
wpCr6zWQUFHMIBgj8hugoWnqEFdl/lkfaaL+1gC8BebZber13N1ruyJ16NTK1xgC
cKXtJZgCvtbrSj8vKuRCTHvhU2YyY5SMscJcLD3GxIfNPqZKP99ujF78hYoNtThO
0OM/3XR/tc0M+pX0h7Trq3ej4H3o5g/sObBsF4YrLDLLeqtk/v+CFQP8kkC5Jv8f
ijHpw2RPOiGaiq6co48uQ3yUHQNspxnxYZbUqPh4t7QoZT2tLhOZpimxB8OBN/Fx
N5bpV49TG0jS3vxFOtXnlcF3N9gEFlLOTxP97WT90MUjARSPOWQynIz7k+QAa6r6
WNti0ONu9K+nngNOGR6WJxvQlI8tybHHJYPzE2tJc6SLV6toCNt/hUkc9R55c/bb
cBfe4od8kvWalLffCb4L4i/fSQIDAQABo1MwUTAdBgNVHQ4EFgQUPGHl+9/pe8tN
xw8+GNW8ReLUQrIwHwYDVR0jBBgwFoAUPGHl+9/pe8tNxw8+GNW8ReLUQrIwDwYD
VR0TAQH/BAUwAwEB/zANBgkqhkiG9w0BAQsFAAOCAgEAR5v3IUKGbzfKdhEt1gIw
H6AE+ALcJJgqqiZFymdaVUz0b4TWwN9K2AghXw+vgyuU/j+mh88uVaA3u2H1UijU
i+CdsN28qN8/oWio5UxbMP4oz8NkruIdqs4S+8aklWYuOKkMWpQLyvUihvmETc18
fp5IgQup60x8oW6grD0QXppJfCRJs1B0csZea278csyPRP7l808G4EWkMlYaVrgo
9uA3XmAkFo/sIloniJSiM5KshMXLp8HKqToNl7X907QDs55KHv2eMldqogt8b/xu
3nry9YsDc9zEhL/z4As9rXktXiV2/ctc8ngkqoTKqJuaYvUSld/Pl1rbE5bcjO0b
N9lYAKlYiprST4CJtLuZfe7cK8rg3flOc6DqOquxc5P0S8sexxcsCnga/2U87n+c
gH3swCNFeTH843Pgk3YdF3/TzQ7LW8v5SoiZ+S1pHAbZHCTUCCN3dVYJNMTxAtWo
bcF+De2MzfieNkBtKKQ7skC5RhG/n2xLj1r8wvilIO/YlqB/C7s2aJaGPwH+ti7M
QA38ecQf1z6DgLDWdlQncQ2gE1ca19GBgA0Kyyfudr4z6bQG77w+3j3p1ZlexY9n
wGfTN9G6sx4vuPjXe8NlEZMJ1eE8pDnIUvQZD8RzL/9EksQeTu7ofNn2KC9J7pfn
MlubpsoEK2bYQDZskgDGCHI=
-----END CERTIFICATE-----
`

func TestReconcileAuthConfig(t *testing.T) {
	type testCase struct {
		name                                string
		client                              crclient.Client
		expectedAuthenticationConfiguration *AuthenticationConfiguration
		kasParams                           KubeAPIServerConfigParams
		shouldError                         bool
		featureGates                        []featuregate.Feature
	}

	testCases := []testCase{
		{
			name:   "nil authentication spec provided, empty structured authentication configuration configmap provided",
			client: nil, // no client necessary here, we never make it to a client call
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: v1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{},
			},
			kasParams: KubeAPIServerConfigParams{
				Authentication: nil,
			},
		},
		{
			name:   "non-nil authentication spec provided, getting CA configmap fails, error",
			client: fake.NewClientBuilder().Build(),
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: v1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{},
			},
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "test.com",
								CertificateAuthority: configv1.ConfigMapNameReference{
									Name: "test-provider-ca",
								},
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "non-nil authentication spec provided, getting CA configmap succeeds, doesn't contain key 'ca-bundle.crt', error",
			client: fake.NewClientBuilder().WithObjects(&corev1.ConfigMap{ObjectMeta: v1.ObjectMeta{Name: "test-provider-ca", Namespace: "test"}, Data: map[string]string{"foo": "bar"}}).Build(),
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: v1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{},
			},
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "test.com",
								CertificateAuthority: configv1.ConfigMapNameReference{
									Name: "test-provider-ca",
								},
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "non-nil authentication spec provided, getting CA configmap succeeds, contains key 'ca-bundle.crt', no error, oidc providers are mapped appropriately",
			client: fake.NewClientBuilder().WithObjects(&corev1.ConfigMap{ObjectMeta: v1.ObjectMeta{Name: "test-provider-ca", Namespace: "test"}, Data: map[string]string{"ca-bundle.crt": caContents}}).Build(),
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: v1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                  "https://test.com",
							CertificateAuthority: caContents,
							Audiences: []string{
								"one",
								"two",
							},
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To("https://test.com#"),
								Claim:  "username",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "",
							},
							UID: ClaimOrExpression{
								Claim: "sub",
							},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
					},
				},
			},
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								CertificateAuthority: configv1.ConfigMapNameReference{
									Name: "test-provider-ca",
								},
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									PrefixPolicy: configv1.NoOpinion,
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "authn spec provided, username claim mapping specified, username mapping prefix policy of NoPrefix, no error, successful mapping",
			client: nil,
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: v1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences: []string{
								"one",
								"two",
							},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "username",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "",
							},
							UID: ClaimOrExpression{
								Claim: "sub",
							},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
					},
				},
			},
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
									PrefixPolicy: configv1.NoPrefix,
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "authn spec provided, username claim mapping specified, username mapping prefix policy of Prefix, prefix provided, no error, successful mapping",
			client: nil,
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: v1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences: []string{
								"one",
								"two",
							},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To("providedPrefix"),
								Claim:  "username",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "",
							},
							UID: ClaimOrExpression{
								Claim: "sub",
							},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
					},
				},
			},
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
									PrefixPolicy: configv1.Prefix,
									Prefix: &configv1.UsernamePrefix{
										PrefixString: "providedPrefix",
									},
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "authn spec provided, username claim mapping specified, username mapping prefix policy of Prefix, prefix not provided, error",
			client: nil,
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
									PrefixPolicy: configv1.Prefix,
								},
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "authn spec provided, username claim mapping specified, username mapping prefix policy of NoOpinion, username claim is not email, no error, successful mapping with issuer URL as prefix",
			client: nil,
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: v1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences: []string{
								"one",
								"two",
							},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To("https://test.com#"),
								Claim:  "username",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "",
							},
							UID: ClaimOrExpression{
								Claim: "sub",
							},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
					},
				},
			},
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
									PrefixPolicy: configv1.NoOpinion,
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "authn spec provided, username claim mapping specified, username mapping prefix policy of NoOpinion, username claim is email, no error, successful mapping with no username prefix",
			client: nil,
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: v1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences: []string{
								"one",
								"two",
							},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "email",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "",
							},
							UID: ClaimOrExpression{
								Claim: "sub",
							},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
					},
				},
			},
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "email",
									},
									PrefixPolicy: configv1.NoOpinion,
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "authn spec provided, groups claim mapping specified, no error, successful mapping",
			client: nil,
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: v1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences: []string{
								"one",
								"two",
							},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To("https://test.com#"),
								Claim:  "username",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To("groups-prefix"),
								Claim:  "groups",
							},
							UID: ClaimOrExpression{
								Claim: "sub",
							},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
					},
				},
			},
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{

								Username: configv1.UsernameClaimMapping{
									PrefixPolicy: configv1.NoOpinion,
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
								},
								Groups: configv1.PrefixedClaimMapping{
									Prefix: "groups-prefix",
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "groups",
									},
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "authn spec provided, uid claim mapping specified, non-empty claim provided, no error, successful mapping",
			client: nil,
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: v1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences: []string{
								"one",
								"two",
							},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To("https://test.com#"),
								Claim:  "username",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "",
							},
							UID: ClaimOrExpression{
								Claim: "custom",
							},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
					},
				},
			},
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									PrefixPolicy: configv1.NoOpinion,
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
								},
								UID: &configv1.TokenClaimOrExpressionMapping{
									Claim: "custom",
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "authn spec provided, uid claim mapping specified, non-empty expression provided, no error, successful mapping",
			client: nil,
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: v1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences: []string{
								"one",
								"two",
							},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To("https://test.com#"),
								Claim:  "username",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "",
							},
							UID: ClaimOrExpression{
								Expression: "claims.foo",
							},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
					},
				},
			},
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									PrefixPolicy: configv1.NoOpinion,
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
								},
								UID: &configv1.TokenClaimOrExpressionMapping{
									Expression: "claims.foo",
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "authn spec provided, uid claim mapping specified, non-empty claim and expression provided, error",
			client: nil,
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									PrefixPolicy: configv1.NoOpinion,
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
								},
								UID: &configv1.TokenClaimOrExpressionMapping{
									Claim:      "foo",
									Expression: "claims.foo",
								},
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "authn spec provided, uid claim mapping specified, empty claim, non-empty but invalid expression provided, error",
			client: nil,
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									PrefixPolicy: configv1.NoOpinion,
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
								},
								UID: &configv1.TokenClaimOrExpressionMapping{
									Expression: "#@!$&*(^)",
								},
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "authn spec provided, extra claim mapping specified, non-empty key and valueExpression provided, no error, successful mapping",
			client: nil,
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: v1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences: []string{
								"one",
								"two",
							},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To("https://test.com#"),
								Claim:  "username",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "",
							},
							UID: ClaimOrExpression{
								Claim: "sub",
							},
							Extra: []ExtraMapping{
								{
									Key:             "example.com/foo",
									ValueExpression: "claims.foo",
								},
							},
						},
						ClaimValidationRules: []ClaimValidationRule{},
					},
				},
			},
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									PrefixPolicy: configv1.NoOpinion,
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
								},
								Extra: []configv1.ExtraMapping{
									{
										Key:             "example.com/foo",
										ValueExpression: "claims.foo",
									},
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "authn spec provided, extra claim mapping specified, non-empty key, invalid valueExpression, error",
			client: nil,
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									PrefixPolicy: configv1.NoOpinion,
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
								},
								Extra: []configv1.ExtraMapping{
									{
										Key:             "example.com/foo",
										ValueExpression: "#@!$&*(^)",
									},
								},
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "authn spec provided, extra claim mapping specified, empty key provided, error",
			client: nil,
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									PrefixPolicy: configv1.NoOpinion,
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
								},
								Extra: []configv1.ExtraMapping{
									{
										ValueExpression: "claims.foo",
									},
								},
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "authn spec provided, extra claim mapping specified, non-empty key and empty valueExpression provided, error",
			client: nil,
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									PrefixPolicy: configv1.NoOpinion,
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
								},
								Extra: []configv1.ExtraMapping{
									{
										Key: "example.com/foo",
									},
								},
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "authn spec provided, claim validation rules specified, type set to RequiredClaim, requiredClaim is set, no error, successful mapping",
			client: nil,
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: v1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences: []string{
								"one",
								"two",
							},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To("https://test.com#"),
								Claim:  "username",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "groups",
							},
							UID: ClaimOrExpression{
								Claim: "sub",
							},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{
							{
								Claim:         "foo",
								RequiredValue: "bar",
							},
						},
					},
				},
			},
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimMappings: configv1.TokenClaimMappings{
								Username: configv1.UsernameClaimMapping{
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "username",
									},
									PrefixPolicy: configv1.NoOpinion,
								},
								Groups: configv1.PrefixedClaimMapping{
									TokenClaimMapping: configv1.TokenClaimMapping{
										Claim: "groups",
									},
									Prefix: "",
								},
							},
							ClaimValidationRules: []configv1.TokenClaimValidationRule{
								{
									Type: configv1.TokenValidationRuleTypeRequiredClaim,
									RequiredClaim: &configv1.TokenRequiredClaim{
										Claim:         "foo",
										RequiredValue: "bar",
									},
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "authn spec provided, claim validation rules specified, type set to RequiredClaim, requiredClaim not set, error",
			client: nil,
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimValidationRules: []configv1.TokenClaimValidationRule{
								{
									Type: configv1.TokenValidationRuleTypeRequiredClaim,
								},
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "authn spec provided, claim validation rules specified, type set to invalid value, error",
			client: nil,
			kasParams: KubeAPIServerConfigParams{
				Authentication: &configv1.AuthenticationSpec{
					OIDCProviders: []configv1.OIDCProvider{
						{
							Name: "test",
							Issuer: configv1.TokenIssuer{
								URL: "https://test.com",
								Audiences: []configv1.TokenAudience{
									"one",
									"two",
								},
							},
							ClaimValidationRules: []configv1.TokenClaimValidationRule{
								{
									Type: "Invalid",
								},
							},
						},
					},
				},
			},
			shouldError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cm := &corev1.ConfigMap{
				ObjectMeta: v1.ObjectMeta{
					Namespace: "test",
				},
			}

			ownerRef := hcpconfig.OwnerRef{
				Reference: nil,
			}

			if len(tc.featureGates) > 0 {
				for _, feature := range tc.featureGates {
					fgtesting.SetFeatureGateDuringTest(t, featuregates.Gate(), feature, true)
				}
			}

			ctx := context.TODO()
			err := ReconcileAuthConfig(ctx, tc.client, cm, ownerRef, tc.kasParams)

			if tc.shouldError && err == nil {
				t.Fatal("expected an error to have occurred but got none")
			} else if !tc.shouldError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			switch {
			case tc.shouldError && err == nil:
				t.Fatal("expected an error to have occurred but got none")
			case !tc.shouldError && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.shouldError && err != nil:
				// as expected
				return
			}

			serializedExpectedAuthConfig, err := json.Marshal(tc.expectedAuthenticationConfiguration)
			if err != nil {
				t.Fatalf("serializing expected AuthenticationConfiguration: %v", err)
			}

			actualAuthConfig, ok := cm.Data[AuthenticationConfigKey]
			if !ok {
				t.Fatalf("ConfigMap.Data does not contain expected key %s", AuthenticationConfigKey)
			}

			if diff := cmp.Diff(string(serializedExpectedAuthConfig), actualAuthConfig); diff != "" {
				t.Fatal("actual structured authentication configuration does not match expected", diff)
			}
		})
	}
}
