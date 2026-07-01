package kas

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestGenerateAuthConfig(t *testing.T) {
	type testCase struct {
		name                                string
		ctx                                 context.Context
		spec                                *configv1.AuthenticationSpec
		client                              crclient.Reader
		namespace                           string
		expectedAuthenticationConfiguration *AuthenticationConfiguration
		shouldError                         bool
		errSubstr                           string
		featureGates                        []featuregate.Feature
	}

	testCases := []testCase{
		{
			name:        "When authentication spec is nil, it should return an error",
			ctx:         context.Background(),
			spec:        nil,
			client:      nil,
			namespace:   "test",
			shouldError: true,
		},
		{
			name: "When valid OIDC provider is provided, it should generate valid authentication configuration",
			ctx:  context.Background(),
			spec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test-provider",
						Issuer: configv1.TokenIssuer{
							URL: "https://test.example.com",
							Audiences: []configv1.TokenAudience{
								"test-audience",
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
			client:    fake.NewClientBuilder().Build(),
			namespace: "test-namespace",
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.example.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"test-audience"},
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
			shouldError: false,
		},
		{
			name: "When issuer references a missing CA ConfigMap, it should return a wrapped error",
			spec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test-provider",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.example.com",
							Audiences: []configv1.TokenAudience{"test-audience"},
							CertificateAuthority: configv1.ConfigMapNameReference{
								Name: "nonexistent-ca-configmap",
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
			client:      fake.NewClientBuilder().Build(),
			namespace:   "test-namespace",
			shouldError: true,
			errSubstr:   "generating JWT authenticator for provider",
		},
		{
			name: "When OIDC provider with CEL validation is provided, it should generate configuration with validation rules",
			ctx:  context.Background(),
			spec: &configv1.AuthenticationSpec{
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
								Expression: "claims.email",
							},
						},
					},
				},
			},
			client:    fake.NewClientBuilder().Build(),
			namespace: "test-namespace",
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.example.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"test-audience"},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Expression: "claims.email",
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
						ClaimValidationRules: []ClaimValidationRule{
							{
								Expression: "claims.email_verified == true",
								Message:    "email must be verified",
							},
						},
						UserValidationRules: []UserValidationRule{},
					},
				},
			},
			shouldError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.featureGates) > 0 {
				for _, feature := range tc.featureGates {
					fgtesting.SetFeatureGateDuringTest(t, featuregates.Gate(), feature, true)
				}
			}

			actualConfig, err := GenerateAuthConfig(tc.ctx, tc.spec, tc.client, tc.namespace)

			switch {
			case tc.shouldError && err == nil:
				t.Fatal("expected an error to have occurred but got none")
			case !tc.shouldError && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.shouldError && err != nil:
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Fatalf("expected error to contain %q, got: %v", tc.errSubstr, err)
				}
				return
			}

			if diff := cmp.Diff(tc.expectedAuthenticationConfiguration, actualConfig); diff != "" {
				t.Fatalf("actual authentication configuration does not match expected (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAdaptAuthConfig(t *testing.T) {
	type testCase struct {
		name                                string
		client                              crclient.Reader
		expectedAuthenticationConfiguration *AuthenticationConfiguration
		hcpAuthenticationSpec               *configv1.AuthenticationSpec
		shouldError                         bool
		featureGates                        []featuregate.Feature
	}

	testCases := []testCase{
		{
			name:   "non-nil authentication spec provided, getting CA configmap fails, error",
			client: fake.NewClientBuilder().Build(),
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
			shouldError: true,
		},
		{
			name:   "non-nil authentication spec provided, getting CA configmap succeeds, doesn't contain key 'ca-bundle.crt', error",
			client: fake.NewClientBuilder().WithObjects(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test-provider-ca", Namespace: "test"}, Data: map[string]string{"foo": "bar"}}).Build(),
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
			shouldError: true,
		},
		{
			name:   "non-nil authentication spec provided, getting CA configmap succeeds, contains key 'ca-bundle.crt', no error, oidc providers are mapped appropriately",
			client: fake.NewClientBuilder().WithObjects(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test-provider-ca", Namespace: "test"}, Data: map[string]string{"ca-bundle.crt": caContents}}).Build(),
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
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
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
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
				TypeMeta: metav1.TypeMeta{
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
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
								PrefixPolicy: configv1.NoPrefix,
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
				TypeMeta: metav1.TypeMeta{
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
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
								PrefixPolicy: configv1.Prefix,
								Prefix: &configv1.UsernamePrefix{
									PrefixString: "providedPrefix",
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
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
								PrefixPolicy: configv1.Prefix,
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
				TypeMeta: metav1.TypeMeta{
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
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
								PrefixPolicy: configv1.NoOpinion,
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
				TypeMeta: metav1.TypeMeta{
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
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "email",
								PrefixPolicy: configv1.NoOpinion,
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
				TypeMeta: metav1.TypeMeta{
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
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
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
			shouldError: false,
		},
		{
			name:   "authn spec provided, uid claim mapping specified, non-empty claim provided, no error, successful mapping",
			client: nil,
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
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
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
							},
							UID: &configv1.TokenClaimOrExpressionMapping{
								Claim: "custom",
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
				TypeMeta: metav1.TypeMeta{
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
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
							},
							UID: &configv1.TokenClaimOrExpressionMapping{
								Expression: "claims.foo",
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
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
							},
							UID: &configv1.TokenClaimOrExpressionMapping{
								Claim:      "foo",
								Expression: "claims.foo",
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
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
							},
							UID: &configv1.TokenClaimOrExpressionMapping{
								Expression: "#@!$&*(^)",
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
				TypeMeta: metav1.TypeMeta{
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
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
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
			shouldError: false,
		},
		{
			name:   "authn spec provided, extra claim mapping specified, non-empty key, invalid valueExpression, error",
			client: nil,
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
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
			shouldError: true,
		},
		{
			name:   "authn spec provided, extra claim mapping specified, empty key provided, error",
			client: nil,
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
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
			shouldError: true,
		},
		{
			name:   "authn spec provided, extra claim mapping specified, non-empty key and empty valueExpression provided, error",
			client: nil,
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
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
			shouldError: true,
		},
		{
			name:   "authn spec provided, claim validation rules specified, type set to RequiredClaim, requiredClaim is set, no error, successful mapping",
			client: nil,
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
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
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
								Claim:        "username",
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
			shouldError: false,
		},
		{
			name:   "authn spec provided, claim validation rules specified, type set to RequiredClaim, requiredClaim not set, error",
			client: nil,
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
			shouldError: true,
		},
		{
			name:   "authn spec provided, claim validation rules specified, type set to invalid value, error",
			client: nil,
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
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
			shouldError: true,
		},
		{
			name:   "invalid discovery URL (http instead of https)",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							DiscoveryURL: "http://insecure-url.com",
							Audiences: []configv1.TokenAudience{
								"one",
								"two",
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "invalid discovery URL (identical to issuer URL)",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:          "https://issuer.example.com",
							DiscoveryURL: "https://issuer.example.com",
							Audiences: []configv1.TokenAudience{
								"one",
								"two",
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "invalid discovery URL (identical to issuer URL except trailing slash)",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:          "https://issuer.example.com",
							DiscoveryURL: "https://issuer.example.com/",
							Audiences: []configv1.TokenAudience{
								"one",
								"two",
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "invalid discovery URL (missing host)",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:          "https://issuer.example.com",
							DiscoveryURL: "https://path", // missing host
							Audiences: []configv1.TokenAudience{
								"one",
								"two",
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "invalid discovery URL (contains user info)",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:          "https://issuer.example.com",
							DiscoveryURL: "https://user@discovery.example.com/path", // contains user info
							Audiences: []configv1.TokenAudience{
								"one",
								"two",
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "invalid discovery URL (contains query string)",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:          "https://issuer.example.com",
							DiscoveryURL: "https://discovery.example.com/path?q=1", // contains query string
							Audiences: []configv1.TokenAudience{
								"one",
								"two",
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "invalid discovery URL (contains fragment)",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:          "https://issuer.example.com",
							DiscoveryURL: "https://discovery.example.com/path#fragment", // contains fragment
							Audiences: []configv1.TokenAudience{
								"one",
								"two",
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "invalid discovery URL (parse error)",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:          "https://issuer.example.com",
							DiscoveryURL: "https://%zz", // parse error
							Audiences: []configv1.TokenAudience{
								"one",
								"two",
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "authn spec provided, username expression set with claims.email and claims.email_verified in claimValidationRule, no error, successful mapping",
			client: nil,
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"one", "two"},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Expression: "claims.email",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "",
							},
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{
							{
								Expression: "claims.email_verified == true",
								Message:    "email must be verified",
							},
						},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								Expression: "claims.email",
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
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "authn spec provided, username expression and prefix policy set to Prefix, error",
			client: nil,
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								Expression:   "claims.sub",
								PrefixPolicy: configv1.Prefix,
								Prefix: &configv1.UsernamePrefix{
									PrefixString: "oidc-user:",
								},
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "authn spec provided, groups expression and prefix set, error",
			client: nil,
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoOpinion,
								Claim:        "username",
							},
							Groups: configv1.PrefixedClaimMapping{
								TokenClaimMapping: configv1.TokenClaimMapping{
									Expression: "claims.groups",
								},
								Prefix: "oidc-group:",
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "authn spec provided, groups expression set, no error, successful mapping",
			client: nil,
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"one", "two"},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To("https://test.com#"),
								Claim:  "username",
							},
							Groups: PrefixedClaimOrExpression{
								Expression: "claims.groups",
							},
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoOpinion,
								Claim:        "username",
							},
							Groups: configv1.PrefixedClaimMapping{
								TokenClaimMapping: configv1.TokenClaimMapping{
									Expression: "claims.groups",
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "authn spec provided, username claim and expression both set, error",
			client: nil,
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								Claim:      "username",
								Expression: "claims.email",
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "authn spec provided, groups claim and expression both set, error",
			client: nil,
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoOpinion,
								Claim:        "username",
							},
							Groups: configv1.PrefixedClaimMapping{
								TokenClaimMapping: configv1.TokenClaimMapping{
									Claim:      "groups",
									Expression: "claims.groups",
								},
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "user validation rule invalid expression",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL: "https://issuer.example.com",
							Audiences: []configv1.TokenAudience{
								"one",
								"two",
							},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoOpinion,
								Claim:        "username",
							},
						},
						UserValidationRules: []configv1.TokenUserValidationRule{
							{
								Expression: "", // invalid: empty expression
								Message:    "must have a valid expression",
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "authn spec provided, username expression uses claims.email without claims.email_verified, error",
			client: nil,
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								Expression: "claims.email",
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "When discovery URL has a different host from issuer URL, it should generate valid authentication configuration",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://issuer.example.com",
							DiscoveryURL:        "https://discovery.example.com/.well-known/openid-configuration",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"one", "two"},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To("https://issuer.example.com#"),
								Claim:  "username",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "",
							},
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
						UserValidationRules:  []UserValidationRule{},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:          "https://issuer.example.com",
							DiscoveryURL: "https://discovery.example.com/.well-known/openid-configuration",
							Audiences:    []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoOpinion,
								Claim:        "username",
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "When discovery URL has a different path from issuer URL, it should generate valid authentication configuration",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://example.com/issuer",
							DiscoveryURL:        "https://example.com/discovery/.well-known/openid-configuration",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"one", "two"},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To("https://example.com/issuer#"),
								Claim:  "username",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "",
							},
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
						UserValidationRules:  []UserValidationRule{},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:          "https://example.com/issuer",
							DiscoveryURL: "https://example.com/discovery/.well-known/openid-configuration",
							Audiences:    []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoOpinion,
								Claim:        "username",
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "When a valid userValidationRule with single expression is provided, it should generate valid authentication configuration",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"one", "two"},
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
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
						UserValidationRules: []UserValidationRule{
							{
								Expression: "!user.username.startsWith('system:')",
								Message:    "username cannot use reserved system: prefix",
							},
						},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoOpinion,
								Claim:        "username",
							},
						},
						UserValidationRules: []configv1.TokenUserValidationRule{
							{
								Expression: "!user.username.startsWith('system:')",
								Message:    "username cannot use reserved system: prefix",
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "When valid userValidationRules with multiple expressions ANDed together are provided, it should generate valid authentication configuration",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"one", "two"},
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
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
						UserValidationRules: []UserValidationRule{
							{
								Expression: "!user.username.startsWith('system:')",
								Message:    "username cannot use reserved system: prefix",
							},
							{
								Expression: "user.groups.size() > 0",
								Message:    "user must have at least one group",
							},
							{
								Expression: "user.username.contains('@')",
								Message:    "username must be an email address",
							},
						},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoOpinion,
								Claim:        "username",
							},
						},
						UserValidationRules: []configv1.TokenUserValidationRule{
							{
								Expression: "!user.username.startsWith('system:')",
								Message:    "username cannot use reserved system: prefix",
							},
							{
								Expression: "user.groups.size() > 0",
								Message:    "user must have at least one group",
							},
							{
								Expression: "user.username.contains('@')",
								Message:    "username must be an email address",
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "When valid claimValidationRules with CEL and multiple expressions are provided, it should generate valid authentication configuration",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"one", "two"},
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
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{
							{
								Expression: "has(claims.email) && claims.email.endsWith('@example.com')",
								Message:    "email must be from example.com domain",
							},
							{
								Expression: "claims.email_verified == true",
								Message:    "email must be verified",
							},
						},
						UserValidationRules: []UserValidationRule{},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoOpinion,
								Claim:        "username",
							},
						},
						ClaimValidationRules: []configv1.TokenClaimValidationRule{
							{
								Type: configv1.TokenValidationRuleTypeCEL,
								CEL: configv1.TokenClaimValidationCELRule{
									Expression: "has(claims.email) && claims.email.endsWith('@example.com')",
									Message:    "email must be from example.com domain",
								},
							},
							{
								Type: configv1.TokenValidationRuleTypeCEL,
								CEL: configv1.TokenClaimValidationCELRule{
									Expression: "claims.email_verified == true",
									Message:    "email must be verified",
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "When full feature parity with discoveryURL, CEL claim mappings, claim validation, and user validation is configured, it should generate valid authentication configuration",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://issuer.example.com",
							DiscoveryURL:        "https://discovery.example.com/.well-known/openid-configuration",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"my-app"},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Expression: "claims.email.split('@')[0]",
							},
							Groups: PrefixedClaimOrExpression{
								Expression: "type(claims.groups) == list ? claims.groups : []",
							},
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{
							{
								Expression: "has(claims.email) && claims.email.endsWith('@example.com')",
								Message:    "email must be from example.com domain",
							},
							{
								Expression: "claims.email_verified == true",
								Message:    "email must be verified",
							},
						},
						UserValidationRules: []UserValidationRule{
							{
								Expression: "!user.username.startsWith('system:')",
								Message:    "username cannot use reserved system: prefix",
							},
							{
								Expression: "user.groups.size() > 0",
								Message:    "user must have at least one group",
							},
						},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:          "https://issuer.example.com",
							DiscoveryURL: "https://discovery.example.com/.well-known/openid-configuration",
							Audiences:    []configv1.TokenAudience{"my-app"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								Expression: "claims.email.split('@')[0]",
							},
							Groups: configv1.PrefixedClaimMapping{
								TokenClaimMapping: configv1.TokenClaimMapping{
									Expression: "type(claims.groups) == list ? claims.groups : []",
								},
							},
						},
						ClaimValidationRules: []configv1.TokenClaimValidationRule{
							{
								Type: configv1.TokenValidationRuleTypeCEL,
								CEL: configv1.TokenClaimValidationCELRule{
									Expression: "has(claims.email) && claims.email.endsWith('@example.com')",
									Message:    "email must be from example.com domain",
								},
							},
							{
								Type: configv1.TokenValidationRuleTypeCEL,
								CEL: configv1.TokenClaimValidationCELRule{
									Expression: "claims.email_verified == true",
									Message:    "email must be verified",
								},
							},
						},
						UserValidationRules: []configv1.TokenUserValidationRule{
							{
								Expression: "!user.username.startsWith('system:')",
								Message:    "username cannot use reserved system: prefix",
							},
							{
								Expression: "user.groups.size() > 0",
								Message:    "user must have at least one group",
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "When claimValidationRule with CEL has an empty expression, it should return an error",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoOpinion,
								Claim:        "username",
							},
						},
						ClaimValidationRules: []configv1.TokenClaimValidationRule{
							{
								Type: configv1.TokenValidationRuleTypeCEL,
								CEL: configv1.TokenClaimValidationCELRule{
									Expression: "", // empty expression
									Message:    "validation failed",
								},
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "When username expression uses complex CEL to extract from nested claims, it should generate valid authentication configuration",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"one", "two"},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Expression: "has(claims.preferred_username) ? claims.preferred_username : claims.sub",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "",
							},
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
						UserValidationRules:  []UserValidationRule{},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								Expression: "has(claims.preferred_username) ? claims.preferred_username : claims.sub",
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "When groups expression uses complex CEL with conditionals based on claim type, it should generate valid authentication configuration",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"one", "two"},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To("https://test.com#"),
								Claim:  "username",
							},
							Groups: PrefixedClaimOrExpression{
								Expression: "claims.?groups.orValue([])",
							},
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
						UserValidationRules:  []UserValidationRule{},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoOpinion,
								Claim:        "username",
							},
							Groups: configv1.PrefixedClaimMapping{
								TokenClaimMapping: configv1.TokenClaimMapping{
									Expression: "claims.?groups.orValue([])",
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "When multiple claimValidationRules with CEL type and complex expressions are provided, it should generate valid authentication configuration",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"one", "two"},
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
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{
							{
								Expression: "has(claims.email) && claims.email.contains('@')",
								Message:    "token must have valid email claim",
							},
							{
								Expression: "claims.email_verified == true",
								Message:    "email must be verified",
							},
							{
								Expression: "has(claims.groups) && type(claims.groups) == list",
								Message:    "groups claim must be a list",
							},
						},
						UserValidationRules: []UserValidationRule{},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoOpinion,
								Claim:        "username",
							},
						},
						ClaimValidationRules: []configv1.TokenClaimValidationRule{
							{
								Type: configv1.TokenValidationRuleTypeCEL,
								CEL: configv1.TokenClaimValidationCELRule{
									Expression: "has(claims.email) && claims.email.contains('@')",
									Message:    "token must have valid email claim",
								},
							},
							{
								Type: configv1.TokenValidationRuleTypeCEL,
								CEL: configv1.TokenClaimValidationCELRule{
									Expression: "claims.email_verified == true",
									Message:    "email must be verified",
								},
							},
							{
								Type: configv1.TokenValidationRuleTypeCEL,
								CEL: configv1.TokenClaimValidationCELRule{
									Expression: "has(claims.groups) && type(claims.groups) == list",
									Message:    "groups claim must be a list",
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "When CEL expression for username and groups with filtering omits prefix and prefixPolicy, it should generate valid authentication configuration",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"one", "two"},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Expression: "claims.email.split('@')[0]",
							},
							Groups: PrefixedClaimOrExpression{
								Expression: "claims.?groups.orValue(dyn([])).filter(g, g.startsWith('ocp-'))",
							},
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{
							{
								Expression: "claims.email_verified == true",
								Message:    "email must be verified",
							},
						},
						UserValidationRules: []UserValidationRule{
							{
								Expression: "user.username.size() > 5",
								Message:    "username must be longer than 5 characters",
							},
							{
								Expression: "user.groups.size() > 0",
								Message:    "user must belong to at least one group after filtering",
							},
						},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								// Omitting prefixPolicy when using expression - should be allowed
								Expression: "claims.email.split('@')[0]",
							},
							Groups: configv1.PrefixedClaimMapping{
								TokenClaimMapping: configv1.TokenClaimMapping{
									// Omitting prefix when using expression - should be allowed
									Expression: "claims.?groups.orValue(dyn([])).filter(g, g.startsWith('ocp-'))",
								},
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
						UserValidationRules: []configv1.TokenUserValidationRule{
							{
								Expression: "user.username.size() > 5",
								Message:    "username must be longer than 5 characters",
							},
							{
								Expression: "user.groups.size() > 0",
								Message:    "user must belong to at least one group after filtering",
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "When combined claim and user validation with CEL expressions is configured, it should generate valid authentication configuration",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"one", "two"},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Expression: "claims.email.split('@')[0]",
							},
							Groups: PrefixedClaimOrExpression{
								Expression: "claims.?groups.orValue(dyn([])).filter(g, g.startsWith('ocp-'))",
							},
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{
							{
								Expression: "has(claims.email) && claims.email.contains('@')",
								Message:    "token must have valid email claim",
							},
							{
								Expression: "claims.email_verified == true",
								Message:    "email must be verified",
							},
						},
						UserValidationRules: []UserValidationRule{
							{
								Expression: "user.username.size() > 5",
								Message:    "mapped username must be longer than 5 characters",
							},
							{
								Expression: "user.groups.size() > 0",
								Message:    "user must have at least one group after filtering",
							},
							{
								Expression: "!user.username.startsWith('system:')",
								Message:    "username cannot use reserved system: prefix",
							},
						},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								Expression: "claims.email.split('@')[0]",
							},
							Groups: configv1.PrefixedClaimMapping{
								TokenClaimMapping: configv1.TokenClaimMapping{
									Expression: "claims.?groups.orValue(dyn([])).filter(g, g.startsWith('ocp-'))",
								},
							},
						},
						ClaimValidationRules: []configv1.TokenClaimValidationRule{
							{
								Type: configv1.TokenValidationRuleTypeCEL,
								CEL: configv1.TokenClaimValidationCELRule{
									Expression: "has(claims.email) && claims.email.contains('@')",
									Message:    "token must have valid email claim",
								},
							},
							{
								Type: configv1.TokenValidationRuleTypeCEL,
								CEL: configv1.TokenClaimValidationCELRule{
									Expression: "claims.email_verified == true",
									Message:    "email must be verified",
								},
							},
						},
						UserValidationRules: []configv1.TokenUserValidationRule{
							{
								Expression: "user.username.size() > 5",
								Message:    "mapped username must be longer than 5 characters",
							},
							{
								Expression: "user.groups.size() > 0",
								Message:    "user must have at least one group after filtering",
							},
							{
								Expression: "!user.username.startsWith('system:')",
								Message:    "username cannot use reserved system: prefix",
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "When username expression uses conditional logic with fallback, it should generate valid authentication configuration",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"one", "two"},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Expression: "has(claims.preferred_username) && claims.preferred_username != '' ? claims.preferred_username : claims.email.split('@')[0]",
							},
							Groups: PrefixedClaimOrExpression{
								Prefix: ptr.To(""),
								Claim:  "",
							},
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{
							{
								Expression: "claims.email_verified == true",
								Message:    "email must be verified when used for username",
							},
						},
						UserValidationRules: []UserValidationRule{},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								Expression: "has(claims.preferred_username) && claims.preferred_username != '' ? claims.preferred_username : claims.email.split('@')[0]",
							},
						},
						ClaimValidationRules: []configv1.TokenClaimValidationRule{
							{
								Type: configv1.TokenValidationRuleTypeCEL,
								CEL: configv1.TokenClaimValidationCELRule{
									Expression: "claims.email_verified == true",
									Message:    "email must be verified when used for username",
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
		{
			name:   "When groups expression uses map and filter operations with orValue for type safety, it should generate valid authentication configuration",
			client: fake.NewClientBuilder().Build(),
			featureGates: []featuregate.Feature{
				featuregates.ExternalOIDCWithUpstreamParity,
			},
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL:                 "https://test.com",
							AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
							Audiences:           []string{"one", "two"},
						},
						ClaimMappings: ClaimMappings{
							Username: PrefixedClaimOrExpression{
								Prefix: ptr.To("https://test.com#"),
								Claim:  "username",
							},
							Groups: PrefixedClaimOrExpression{
								// Use optional access (?) and dyn([]) to handle optional 'roles' claim and provide type-safe default for filter/map
								Expression: "claims.?roles.orValue(dyn([])).filter(r, r.startsWith('openshift-')).map(r, r.substring(10))",
							},
							UID:   ClaimOrExpression{Claim: "sub"},
							Extra: []ExtraMapping{},
						},
						ClaimValidationRules: []ClaimValidationRule{},
						UserValidationRules:  []UserValidationRule{},
					},
				},
			},
			hcpAuthenticationSpec: &configv1.AuthenticationSpec{
				OIDCProviders: []configv1.OIDCProvider{
					{
						Name: "test",
						Issuer: configv1.TokenIssuer{
							URL:       "https://test.com",
							Audiences: []configv1.TokenAudience{"one", "two"},
						},
						ClaimMappings: configv1.TokenClaimMappings{
							Username: configv1.UsernameClaimMapping{
								PrefixPolicy: configv1.NoOpinion,
								Claim:        "username",
							},
							Groups: configv1.PrefixedClaimMapping{
								TokenClaimMapping: configv1.TokenClaimMapping{
									// Use optional access (?) and dyn([]) to handle optional 'roles' claim and provide type-safe default for filter/map
									Expression: "claims.?roles.orValue(dyn([])).filter(r, r.startsWith('openshift-')).map(r, r.substring(10))",
								},
							},
						},
					},
				},
			},
			shouldError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
				},
			}

			if len(tc.featureGates) > 0 {
				for _, feature := range tc.featureGates {
					fgtesting.SetFeatureGateDuringTest(t, featuregates.Gate(), feature, true)
				}
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Authentication: tc.hcpAuthenticationSpec,
					},
				},
			}

			err := adaptAuthConfig(controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				Client:  tc.client,
				HCP:     hcp,
			}, cm)

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

func TestGenerateClaimMappings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		mappings  configv1.TokenClaimMappings
		issuerURL string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "When username mapping is valid, it should return mappings without error",
			mappings: configv1.TokenClaimMappings{
				Username: configv1.UsernameClaimMapping{
					Claim:        "email",
					PrefixPolicy: configv1.NoPrefix,
				},
			},
			issuerURL: "https://issuer.example.com",
		},
		{
			name: "When username mapping has Prefix policy with nil prefix, it should return a wrapped error",
			mappings: configv1.TokenClaimMappings{
				Username: configv1.UsernameClaimMapping{
					Claim:        "email",
					PrefixPolicy: configv1.Prefix,
					Prefix:       nil,
				},
			},
			issuerURL: "https://issuer.example.com",
			wantErr:   true,
			errSubstr: "generating username claim mapping",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := generateClaimMappings(tt.mappings, tt.issuerURL)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestGenerateExtraMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		extra     configv1.ExtraMapping
		wantErr   bool
		errSubstr string
	}{
		{
			name: "When key and valueExpression are valid, it should return mapping without error",
			extra: configv1.ExtraMapping{
				Key:             "example.com/foo",
				ValueExpression: "claims.groups",
			},
		},
		{
			name: "When key is empty, it should return an error",
			extra: configv1.ExtraMapping{
				Key:             "",
				ValueExpression: "claims.groups",
			},
			wantErr:   true,
			errSubstr: "must specify a key",
		},
		{
			name: "When valueExpression is empty, it should return an error",
			extra: configv1.ExtraMapping{
				Key:             "example.com/foo",
				ValueExpression: "",
			},
			wantErr:   true,
			errSubstr: "must specify a valueExpression",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := generateExtraMapping(tt.extra)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestGenerateClaimValidationRule(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		rule      configv1.TokenClaimValidationRule
		wantErr   bool
		errSubstr string
	}{
		{
			name: "When type is RequiredClaim with valid required claim, it should return rule without error",
			rule: configv1.TokenClaimValidationRule{
				Type: configv1.TokenValidationRuleTypeRequiredClaim,
				RequiredClaim: &configv1.TokenRequiredClaim{
					Claim:         "aud",
					RequiredValue: "my-audience",
				},
			},
		},
		{
			name: "When type is RequiredClaim but requiredClaim is nil, it should return an error",
			rule: configv1.TokenClaimValidationRule{
				Type: configv1.TokenValidationRuleTypeRequiredClaim,
			},
			wantErr:   true,
			errSubstr: "requiredClaim is not set",
		},
		{
			name: "When type is CEL with valid expression, it should return rule without error",
			rule: configv1.TokenClaimValidationRule{
				Type: configv1.TokenValidationRuleTypeCEL,
				CEL: configv1.TokenClaimValidationCELRule{
					Expression: "claims.email_verified == true",
					Message:    "email must be verified",
				},
			},
		},
		{
			name: "When type is CEL but expression is empty, it should return an error",
			rule: configv1.TokenClaimValidationRule{
				Type: configv1.TokenValidationRuleTypeCEL,
				CEL:  configv1.TokenClaimValidationCELRule{},
			},
			wantErr:   true,
			errSubstr: "expression is not set",
		},
		{
			name: "When type is unknown, it should return an error",
			rule: configv1.TokenClaimValidationRule{
				Type: "UnknownType",
			},
			wantErr:   true,
			errSubstr: "unknown claimValidationRule type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := generateClaimValidationRule(tt.rule)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestGenerateClaimValidationRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		rules   []configv1.TokenClaimValidationRule
		wantErr bool
	}{
		{
			name: "When all rules are valid, it should return rules without error",
			rules: []configv1.TokenClaimValidationRule{
				{
					Type: configv1.TokenValidationRuleTypeRequiredClaim,
					RequiredClaim: &configv1.TokenRequiredClaim{
						Claim:         "aud",
						RequiredValue: "my-audience",
					},
				},
				{
					Type: configv1.TokenValidationRuleTypeCEL,
					CEL: configv1.TokenClaimValidationCELRule{
						Expression: "claims.email_verified == true",
						Message:    "email must be verified",
					},
				},
			},
		},
		{
			name: "When a rule is invalid, it should return an error",
			rules: []configv1.TokenClaimValidationRule{
				{
					Type: configv1.TokenValidationRuleTypeRequiredClaim,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := generateClaimValidationRules(tt.rules...)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestGenerateUserValidationRule(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		rule      configv1.TokenUserValidationRule
		wantErr   bool
		errSubstr string
	}{
		{
			name: "When expression is valid, it should return rule without error",
			rule: configv1.TokenUserValidationRule{
				Expression: "user.username != 'admin'",
				Message:    "admin not allowed",
			},
		},
		{
			name: "When expression is empty, it should return an error",
			rule: configv1.TokenUserValidationRule{
				Expression: "",
				Message:    "should fail",
			},
			wantErr:   true,
			errSubstr: "expression must be non-empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := generateUserValidationRule(tt.rule)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestGenerateUserValidationRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		rules   []configv1.TokenUserValidationRule
		wantErr bool
	}{
		{
			name: "When all rules are valid, it should return rules without error",
			rules: []configv1.TokenUserValidationRule{
				{Expression: "user.username != 'admin'", Message: "admin not allowed"},
				{Expression: "user.username != 'root'", Message: "root not allowed"},
			},
		},
		{
			name: "When a rule has empty expression, it should return an error",
			rules: []configv1.TokenUserValidationRule{
				{Expression: "user.username != 'admin'", Message: "ok"},
				{Expression: "", Message: "should fail"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := generateUserValidationRules(tt.rules...)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestGenerateJWTForProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		provider  configv1.OIDCProvider
		wantErr   bool
		errSubstr string
	}{
		{
			name: "When username claim is empty, it should return a claim mappings error",
			provider: configv1.OIDCProvider{
				Name: "test-provider",
				Issuer: configv1.TokenIssuer{
					URL:       "https://issuer.example.com",
					Audiences: []configv1.TokenAudience{"aud"},
				},
				ClaimMappings: configv1.TokenClaimMappings{
					Username: configv1.UsernameClaimMapping{},
				},
			},
			wantErr:   true,
			errSubstr: "generating claim mappings",
		},
		{
			name: "When claim validation rule has unknown type, it should return a claim validation rules error",
			provider: configv1.OIDCProvider{
				Name: "test-provider",
				Issuer: configv1.TokenIssuer{
					URL:       "https://issuer.example.com",
					Audiences: []configv1.TokenAudience{"aud"},
				},
				ClaimMappings: configv1.TokenClaimMappings{
					Username: configv1.UsernameClaimMapping{
						Claim:        "email",
						PrefixPolicy: configv1.NoPrefix,
					},
				},
				ClaimValidationRules: []configv1.TokenClaimValidationRule{
					{Type: "UnknownType"},
				},
			},
			wantErr:   true,
			errSubstr: "generating claim validation rules",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().Build()
			_, err := generateJWTForProvider(t.Context(), tt.provider, c, "test-namespace")

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestHCPAuthConfigToAPIServerAuthConfig(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	input := &AuthenticationConfiguration{
		JWT: []JWTAuthenticator{
			{
				Issuer: Issuer{
					URL:       "https://issuer.example.com",
					Audiences: []string{"test-audience"},
				},
				ClaimMappings: ClaimMappings{
					Username: PrefixedClaimOrExpression{
						Claim:  "email",
						Prefix: ptr.To(""),
					},
				},
			},
		},
	}

	result, err := HCPAuthConfigToAPIServerAuthConfig(input)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result.JWT).To(HaveLen(1))
	g.Expect(result.JWT[0].Issuer.URL).To(Equal("https://issuer.example.com"))
}
