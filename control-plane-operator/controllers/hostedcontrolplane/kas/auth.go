package kas

import (
	"encoding/json"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hcpconfig "github.com/openshift/hypershift/support/config"
)

const (
	AuthConfigMapKey = "auth.json"
)

func ReconcileAuthConfig(config *corev1.ConfigMap, ownerRef hcpconfig.OwnerRef, p KubeAPIServerConfigParams) error {
	ownerRef.ApplyTo(config)
	if config.Data == nil {
		config.Data = map[string]string{}
	}
	authConfig := generateAuthConfig(p.Authentication)
	serializedConfig, err := json.Marshal(authConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize kube apiserver authentication config: %w", err)
	}
	config.Data[AuthenticationConfigKey] = string(serializedConfig)
	return nil
}

func generateAuthConfig(spec *configv1.AuthenticationSpec) *AuthenticationConfiguration {
	config := &AuthenticationConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AuthenticationConfiguration",
			APIVersion: "apiserver.config.k8s.io/v1alpha1",
		},
		JWT: []JWTAuthenticator{},
	}
	if spec == nil {
		return config
	}
	for _, provider := range spec.OIDCProviders {
		jwt := JWTAuthenticator{
			Issuer: Issuer{
				URL:                  provider.Issuer.URL,
				CertificateAuthority: oidcCAFile(provider.Issuer.CertificateAuthority.Name),
			},
		}
		audience := []string{}
		for _, a := range provider.Issuer.Audiences {
			audience = append(audience, string(a))
		}
		jwt.Issuer.Audiences = audience
		jwt.Issuer.AudienceMatchPolicy = AudienceMatchPolicyMatchAny
		jwt.ClaimMappings.Username.Claim = provider.ClaimMappings.Username.Claim
		if provider.ClaimMappings.Username.PrefixPolicy == configv1.Prefix {
			jwt.ClaimMappings.Username.Prefix = &provider.ClaimMappings.Username.Prefix.PrefixString
		} else {
			noPrefix := ""
			jwt.ClaimMappings.Username.Prefix = &noPrefix
		}
		jwt.ClaimMappings.Groups.Claim = provider.ClaimMappings.Groups.Claim
		jwt.ClaimMappings.Groups.Prefix = &provider.ClaimMappings.Groups.Prefix
		for _, rule := range provider.ClaimValidationRules {
			switch rule.Type {
			case configv1.TokenValidationRuleTypeRequiredClaim:
				jwtRule := ClaimValidationRule{
					Claim:         rule.RequiredClaim.Claim,
					RequiredValue: rule.RequiredClaim.RequiredValue,
				}
				jwt.ClaimValidationRules = append(jwt.ClaimValidationRules, jwtRule)
			}
		}
		config.JWT = append(config.JWT, jwt)
	}
	return config
}
