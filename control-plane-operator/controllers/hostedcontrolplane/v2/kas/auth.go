package kas

import (
	"context"
	"encoding/json"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiserver "k8s.io/apiserver/pkg/apis/apiserver/v1beta1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func adaptAuthConfig(cpContext component.WorkloadContext, config *corev1.ConfigMap) error {
	configuration := cpContext.HCP.Spec.Configuration
	if configuration == nil || configuration.Authentication == nil || len(configuration.Authentication.OIDCProviders) == 0 {
		return nil
	}

	authConfig, err := generateAuthConfig(cpContext, cpContext.Client, cpContext.HCP)
	if err != nil {
		return fmt.Errorf("failed to generate authentication config: %w", err)
	}
	serializedConfig, err := json.Marshal(authConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize kube apiserver authentication config: %w", err)
	}

	if config.Data == nil {
		config.Data = map[string]string{}
	}
	config.Data[AuthenticationConfigKey] = string(serializedConfig)
	return nil
}

func generateAuthConfig(ctx context.Context, c crclient.Reader, hcp *hyperv1.HostedControlPlane) (*apiserver.AuthenticationConfiguration, error) {
	config := &apiserver.AuthenticationConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AuthenticationConfiguration",
			APIVersion: "apiserver.config.k8s.io/v1alpha1",
		},
		JWT: []apiserver.JWTAuthenticator{},
	}

	for _, provider := range hcp.Spec.Configuration.Authentication.OIDCProviders {
		caData := ""
		if provider.Issuer.CertificateAuthority.Name != "" {
			ca := &corev1.ConfigMap{}
			if err := c.Get(ctx, crclient.ObjectKey{Name: provider.Issuer.CertificateAuthority.Name, Namespace: hcp.Namespace}, ca); err != nil {
				return nil, fmt.Errorf("failed to get issuer certificate authority configmap: %w", err)
			}
			var ok bool
			caData, ok = ca.Data["ca-bundle.crt"]
			if !ok {
				return nil, fmt.Errorf("issuer certificate authority configmap does not contain key ca-bundle.crt")
			}
		}
		jwt := apiserver.JWTAuthenticator{
			Issuer: apiserver.Issuer{
				URL:                  provider.Issuer.URL,
				CertificateAuthority: caData,
			},
		}
		audience := []string{}
		for _, a := range provider.Issuer.Audiences {
			audience = append(audience, string(a))
		}
		jwt.Issuer.Audiences = audience
		jwt.Issuer.AudienceMatchPolicy = apiserver.AudienceMatchPolicyMatchAny
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
				jwtRule := apiserver.ClaimValidationRule{
					Claim:         rule.RequiredClaim.Claim,
					RequiredValue: rule.RequiredClaim.RequiredValue,
				}
				jwt.ClaimValidationRules = append(jwt.ClaimValidationRules, jwtRule)
			}
		}
		config.JWT = append(config.JWT, jwt)
	}
	return config, nil
}
