package kas

import (
	"context"
	"encoding/json"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	hcpconfig "github.com/openshift/hypershift/support/config"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileAuthConfig(t *testing.T) {
	type testCase struct {
		name                                string
		client                              crclient.Client
		expectedAuthenticationConfiguration *AuthenticationConfiguration
		ownerRef                            hcpconfig.OwnerRef
		kasParams                           KubeAPIServerConfigParams
		shouldError                         bool
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
			ownerRef: hcpconfig.OwnerRef{
				Reference: nil,
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
			ownerRef: hcpconfig.OwnerRef{
				Reference: nil,
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
			ownerRef: hcpconfig.OwnerRef{
				Reference: nil,
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
							},
						},
					},
				},
			},
			shouldError: true,
		},
		{
			name:   "non-nil authentication spec provided, getting CA configmap succeeds, contains key 'ca-bundle.crt', no error, oidc providers are mapped appropriately",
			client: fake.NewClientBuilder().WithObjects(&corev1.ConfigMap{ObjectMeta: v1.ObjectMeta{Name: "test-provider-ca", Namespace: "test"}, Data: map[string]string{"ca-bundle.crt": "ca-bundle contents"}}).Build(),
			expectedAuthenticationConfiguration: &AuthenticationConfiguration{
				TypeMeta: v1.TypeMeta{
					APIVersion: "apiserver.config.k8s.io/v1alpha1",
					Kind:       "AuthenticationConfiguration",
				},
				JWT: []JWTAuthenticator{
					{
						Issuer: Issuer{
							URL: "test.com",
                            CertificateAuthority: "ca-bundle contents",
                            Audiences: []string{
                                "one",
                                "two",
                            },
                            AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
						},
                        ClaimMappings: ClaimMappings{
                            Username: PrefixedClaimOrExpression{
                                Prefix: ptr.To(""),
                                Claim: "username",
                            },
                            Groups: PrefixedClaimOrExpression{
                                Prefix: ptr.To(""),
                                Claim: "groups",
                            },
                        },
                        ClaimValidationRules: []ClaimValidationRule{
                            {
                                Claim: "foo",
                                RequiredValue: "bar",
                            },
                        },
					},
					{
						Issuer: Issuer{
							URL: "test-two.com",
                            CertificateAuthority: "ca-bundle contents",
                            Audiences: []string{
                                "three",
                            },
                            AudienceMatchPolicy: AudienceMatchPolicyMatchAny,
						},
                        ClaimMappings: ClaimMappings{
                            Username: PrefixedClaimOrExpression{
                                Prefix: ptr.To("oidc-user:"),
                                Claim: "username",
                            },
                            Groups: PrefixedClaimOrExpression{
                                Prefix: ptr.To("oidc-group:"),
                                Claim: "groups",
                            },
                        },
					},
				},
			},
			ownerRef: hcpconfig.OwnerRef{
				Reference: nil,
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
                                        Claim: "foo",
                                        RequiredValue: "bar",
                                    },
                                },
                            },
						},
                        {
							Name: "test-two",
							Issuer: configv1.TokenIssuer{
								URL: "test-two.com",
								CertificateAuthority: configv1.ConfigMapNameReference{
									Name: "test-provider-ca",
								},
                                Audiences: []configv1.TokenAudience{
                                    "three",
                                },
							},
                            ClaimMappings: configv1.TokenClaimMappings{
                                Username: configv1.UsernameClaimMapping{
                                    TokenClaimMapping: configv1.TokenClaimMapping{
                                        Claim: "username",
                                    },
                                    PrefixPolicy: configv1.Prefix,
                                    Prefix: &configv1.UsernamePrefix{
                                        PrefixString: "oidc-user:",
                                    },
                                },
                                Groups: configv1.PrefixedClaimMapping{
                                    TokenClaimMapping: configv1.TokenClaimMapping{
                                        Claim: "groups",
                                    },
                                    Prefix: "oidc-group:",
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
				ObjectMeta: v1.ObjectMeta{
					Namespace: "test",
				},
			}
			ctx := context.TODO()
			err := ReconcileAuthConfig(ctx, tc.client, cm, tc.ownerRef, tc.kasParams)

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

			if string(serializedExpectedAuthConfig) != actualAuthConfig {
				t.Fatalf("actual structured authentication configuration does not match expected. Actual: %s | Expected: %s", actualAuthConfig, string(serializedExpectedAuthConfig))
			}
		})
	}
}
