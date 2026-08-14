package kas

import (
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGenerateAuthConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		spec      *configv1.AuthenticationSpec
		wantErr   bool
		errSubstr string
	}{
		{
			name: "When spec is nil, it should return empty config without error",
			spec: nil,
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
			wantErr:   true,
			errSubstr: "generating JWT authenticator for provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			c := fake.NewClientBuilder().Build()
			_, err := GenerateAuthConfig(tt.spec, t.Context(), c, "test-namespace")

			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				if tt.errSubstr != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestGenerateIssuer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		issuer    configv1.TokenIssuer
		objects   []crclient.Object
		wantErr   bool
		errSubstr string
	}{
		{
			name: "When issuer has no CA reference, it should return issuer without error",
			issuer: configv1.TokenIssuer{
				URL:       "https://issuer.example.com",
				Audiences: []configv1.TokenAudience{"aud1", "aud2"},
			},
		},
		{
			name: "When issuer has CA reference and ConfigMap exists, it should return issuer with CA",
			issuer: configv1.TokenIssuer{
				URL:       "https://issuer.example.com",
				Audiences: []configv1.TokenAudience{"aud1"},
				CertificateAuthority: configv1.ConfigMapNameReference{
					Name: "test-ca",
				},
			},
			objects: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "test-ca", Namespace: "test-ns"},
					Data:       map[string]string{"ca-bundle.crt": "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----"},
				},
			},
		},
		{
			name: "When issuer has CA reference but ConfigMap is missing, it should return a wrapped error",
			issuer: configv1.TokenIssuer{
				URL:       "https://issuer.example.com",
				Audiences: []configv1.TokenAudience{"aud1"},
				CertificateAuthority: configv1.ConfigMapNameReference{
					Name: "nonexistent",
				},
			},
			wantErr:   true,
			errSubstr: "getting certificate authority for issuer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			c := fake.NewClientBuilder().WithObjects(tt.objects...).Build()
			_, err := generateIssuer(t.Context(), tt.issuer, c, "test-ns")
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestGetCertificateAuthorityFromConfigMap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		caName    string
		objects   []crclient.Object
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "When ConfigMap does not exist, it should return a wrapped error",
			caName:    "nonexistent",
			wantErr:   true,
			errSubstr: "failed to get issuer certificate authority configmap",
		},
		{
			name:   "When ConfigMap exists but lacks ca-bundle.crt key, it should return an error",
			caName: "test-ca",
			objects: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "test-ca", Namespace: "test-ns"},
					Data:       map[string]string{"wrong-key": "data"},
				},
			},
			wantErr:   true,
			errSubstr: "does not contain key",
		},
		{
			name:   "When ConfigMap exists with ca-bundle.crt key, it should return the CA data",
			caName: "test-ca",
			objects: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "test-ca", Namespace: "test-ns"},
					Data:       map[string]string{"ca-bundle.crt": "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			c := fake.NewClientBuilder().WithObjects(tt.objects...).Build()
			_, err := getCertificateAuthorityFromConfigMap(t.Context(), c, tt.caName, "test-ns")
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
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
			name: "When provider has valid issuer and claim mappings, it should return JWT without error",
			provider: configv1.OIDCProvider{
				Name: "test-provider",
				Issuer: configv1.TokenIssuer{
					URL:       "https://issuer.example.com",
					Audiences: []configv1.TokenAudience{"test-audience"},
				},
				ClaimMappings: configv1.TokenClaimMappings{
					Username: configv1.UsernameClaimMapping{
						Claim:        "email",
						PrefixPolicy: configv1.NoPrefix,
					},
				},
			},
		},
		{
			name: "When claim mappings are invalid, it should return a wrapped error",
			provider: configv1.OIDCProvider{
				Name: "test-provider",
				Issuer: configv1.TokenIssuer{
					URL:       "https://issuer.example.com",
					Audiences: []configv1.TokenAudience{"test-audience"},
				},
				ClaimMappings: configv1.TokenClaimMappings{
					Username: configv1.UsernameClaimMapping{
						Claim:        "email",
						PrefixPolicy: configv1.Prefix,
						Prefix:       nil,
					},
				},
			},
			wantErr:   true,
			errSubstr: "generating claim mappings",
		},
		{
			name: "When claim validation rules are invalid, it should return a wrapped error",
			provider: configv1.OIDCProvider{
				Name: "test-provider",
				Issuer: configv1.TokenIssuer{
					URL:       "https://issuer.example.com",
					Audiences: []configv1.TokenAudience{"test-audience"},
				},
				ClaimMappings: configv1.TokenClaimMappings{
					Username: configv1.UsernameClaimMapping{
						Claim:        "email",
						PrefixPolicy: configv1.NoPrefix,
					},
				},
				ClaimValidationRules: []configv1.TokenClaimValidationRule{
					{Type: "InvalidType"},
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

func TestGenerateUsernameClaimMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		username  configv1.UsernameClaimMapping
		issuerURL string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "When prefix policy is NoPrefix, it should return empty prefix",
			username: configv1.UsernameClaimMapping{
				Claim:        "email",
				PrefixPolicy: configv1.NoPrefix,
			},
			issuerURL: "https://issuer.example.com",
		},
		{
			name: "When prefix policy is Prefix but prefix is nil, it should return an error",
			username: configv1.UsernameClaimMapping{
				Claim:        "email",
				PrefixPolicy: configv1.Prefix,
				Prefix:       nil,
			},
			issuerURL: "https://issuer.example.com",
			wantErr:   true,
			errSubstr: "no prefix is specified",
		},
		{
			name: "When prefix policy is Prefix with value, it should use that prefix",
			username: configv1.UsernameClaimMapping{
				Claim:        "email",
				PrefixPolicy: configv1.Prefix,
				Prefix:       &configv1.UsernamePrefix{PrefixString: "myprefix"},
			},
			issuerURL: "https://issuer.example.com",
		},
		{
			name: "When prefix policy is NoOpinion and claim is email, it should use empty prefix",
			username: configv1.UsernameClaimMapping{
				Claim:        "email",
				PrefixPolicy: configv1.NoOpinion,
			},
			issuerURL: "https://issuer.example.com",
		},
		{
			name: "When prefix policy is NoOpinion and claim is not email, it should use issuer URL prefix",
			username: configv1.UsernameClaimMapping{
				Claim:        "sub",
				PrefixPolicy: configv1.NoOpinion,
			},
			issuerURL: "https://issuer.example.com",
		},
		{
			name: "When prefix policy is unknown, it should return an error",
			username: configv1.UsernameClaimMapping{
				Claim:        "email",
				PrefixPolicy: "InvalidPolicy",
			},
			issuerURL: "https://issuer.example.com",
			wantErr:   true,
			errSubstr: "unknown prefix policy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := generateUsernameClaimMapping(tt.username, tt.issuerURL)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
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
			name: "When username mapping has invalid prefix policy, it should return a wrapped error",
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
			name: "When valueExpression is valid, it should return mapping without error",
			extra: configv1.ExtraMapping{
				Key:             "example.com/foo",
				ValueExpression: "claims.groups",
			},
		},
		{
			name: "When valueExpression is invalid CEL, it should return a wrapped error",
			extra: configv1.ExtraMapping{
				Key:             "example.com/foo",
				ValueExpression: "this is not valid CEL !!!",
			},
			wantErr:   true,
			errSubstr: "validating valueExpression",
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

func TestGenerateUIDClaimMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		uid       *configv1.TokenClaimOrExpressionMapping
		wantErr   bool
		errSubstr string
		wantClaim string
	}{
		{
			name:      "When uid is nil, it should default claim to sub",
			uid:       nil,
			wantClaim: "sub",
		},
		{
			name:      "When uid has only a claim, it should use that claim",
			uid:       &configv1.TokenClaimOrExpressionMapping{Claim: "email"},
			wantClaim: "email",
		},
		{
			name: "When uid has only a valid expression, it should use that expression",
			uid:  &configv1.TokenClaimOrExpressionMapping{Expression: "claims.sub"},
		},
		{
			name:      "When uid has an invalid CEL expression, it should return a wrapped error",
			uid:       &configv1.TokenClaimOrExpressionMapping{Expression: "invalid CEL !!!"},
			wantErr:   true,
			errSubstr: "validating CEL expression",
		},
		{
			name:      "When uid has both claim and expression, it should return an error",
			uid:       &configv1.TokenClaimOrExpressionMapping{Claim: "sub", Expression: "claims.sub"},
			wantErr:   true,
			errSubstr: "uid mapping must set either claim or expression, not both",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := generateUIDClaimMapping(tt.uid)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if tt.wantClaim != "" {
					g.Expect(result.Claim).To(Equal(tt.wantClaim))
				}
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
					Claim:         "iss",
					RequiredValue: "https://issuer.example.com",
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
			name: "When type is unknown, it should return an error",
			rule: configv1.TokenClaimValidationRule{
				Type: "InvalidType",
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
		name      string
		rules     []configv1.TokenClaimValidationRule
		wantErr   bool
		wantCount int
	}{
		{
			name: "When all rules are valid, it should return rules without error",
			rules: []configv1.TokenClaimValidationRule{
				{
					Type: configv1.TokenValidationRuleTypeRequiredClaim,
					RequiredClaim: &configv1.TokenRequiredClaim{
						Claim:         "iss",
						RequiredValue: "https://issuer.example.com",
					},
				},
			},
			wantCount: 1,
		},
		{
			name: "When a rule is invalid, it should return an error",
			rules: []configv1.TokenClaimValidationRule{
				{
					Type: "InvalidType",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := generateClaimValidationRules(tt.rules...)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(result).To(HaveLen(tt.wantCount))
			}
		})
	}
}

func TestGenerateExtraClaimMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		extras    []configv1.ExtraMapping
		wantErr   bool
		wantCount int
	}{
		{
			name: "When all mappings are valid, it should return mappings without error",
			extras: []configv1.ExtraMapping{
				{Key: "example.com/foo", ValueExpression: "claims.groups"},
			},
			wantCount: 1,
		},
		{
			name: "When a mapping has empty key, it should return an error",
			extras: []configv1.ExtraMapping{
				{Key: "", ValueExpression: "claims.groups"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result, err := generateExtraClaimMapping(tt.extras...)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(result).To(HaveLen(tt.wantCount))
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
