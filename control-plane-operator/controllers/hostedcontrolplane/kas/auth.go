package kas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	hcpconfig "github.com/openshift/hypershift/support/config"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/apis/apiserver"
	"k8s.io/apiserver/pkg/apis/apiserver/validation"
	authenticationcel "k8s.io/apiserver/pkg/authentication/cel"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	AuthConfigMapKey                 = "auth.json"
	certificateAuthorityConfigMapKey = "ca-bundle.crt"
)

func ReconcileAuthConfig(ctx context.Context, c crclient.Client, config *corev1.ConfigMap, ownerRef hcpconfig.OwnerRef, p KubeAPIServerConfigParams) error {
	ownerRef.ApplyTo(config)
	if config.Data == nil {
		config.Data = map[string]string{}
	}

	authConfig, err := GenerateAuthConfig(p.Authentication, ctx, c, config.Namespace)
	if err != nil {
		return fmt.Errorf("failed to generate authentication config: %w", err)
	}

	// TODO: using the default compiler means that we are allowing CEL library usage for the Kube version that maps to our
	// dependency import. This should align with the version of Kubernetes that will be running for a guest cluster instead
	// since that is what will actually load and validate the configuration.
	fieldErrors := validation.ValidateAuthenticationConfiguration(authenticationcel.NewDefaultCompiler(), authConfig, []string{p.ServiceAccountIssuerURL})
	if fieldErrors.ToAggregate() != nil {
		return fmt.Errorf("validating generated authentication config: %w", fieldErrors.ToAggregate())
	}

	serializedConfig, err := json.Marshal(authConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize kube apiserver authentication config: %w", err)
	}

	config.Data[AuthenticationConfigKey] = string(serializedConfig)
	return nil
}

func GenerateAuthConfig(spec *configv1.AuthenticationSpec, ctx context.Context, c crclient.Client, namespace string) (*apiserver.AuthenticationConfiguration, error) {
	config := &apiserver.AuthenticationConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AuthenticationConfiguration",
			APIVersion: "apiserver.config.k8s.io/v1alpha1",
		},
		JWT: []apiserver.JWTAuthenticator{},
	}
	if spec == nil {
		return config, nil
	}
	for _, provider := range spec.OIDCProviders {
		jwt, err := generateJWTForProvider(ctx, provider, c, namespace)
		if err != nil {
			return nil, fmt.Errorf("generating JWT authenticator for provider %q: %v", provider.Name, err)
		}
		config.JWT = append(config.JWT, jwt)
	}
	return config, nil
}

func generateJWTForProvider(ctx context.Context, provider configv1.OIDCProvider, client crclient.Client, namespace string) (apiserver.JWTAuthenticator, error) {
	out := apiserver.JWTAuthenticator{}

	issuer, err := generateIssuer(ctx, provider.Issuer, client, namespace)
	if err != nil {
		return out, fmt.Errorf("generating issuer: %v", err)
	}

	claimMappings, err := generateClaimMappings(provider.ClaimMappings, issuer.URL)
	if err != nil {
		return out, fmt.Errorf("generating claim mappings: %v", err)
	}

	claimValidationRules, err := generateClaimValidationRules(provider.ClaimValidationRules...)
	if err != nil {
		return out, fmt.Errorf("generating claim validation rules: %v", err)
	}

	out.Issuer = issuer
	out.ClaimMappings = claimMappings
	out.ClaimValidationRules = claimValidationRules

	return out, nil
}

func generateIssuer(ctx context.Context, issuer configv1.TokenIssuer, client crclient.Client, namespace string) (apiserver.Issuer, error) {
	out := apiserver.Issuer{}

	out.URL = issuer.URL
	out.AudienceMatchPolicy = apiserver.AudienceMatchPolicyMatchAny

	for _, audience := range issuer.Audiences {
		out.Audiences = append(out.Audiences, string(audience))
	}

	if len(issuer.CertificateAuthority.Name) > 0 {
		ca, err := getCertificateAuthorityFromConfigMap(ctx, client, issuer.CertificateAuthority.Name, namespace)
		if err != nil {
			return out, fmt.Errorf("getting certificate authority for issuer: %v", err)
		}
		out.CertificateAuthority = ca
	}

	return out, nil
}

func getCertificateAuthorityFromConfigMap(ctx context.Context, client crclient.Client, caName, namespace string) (string, error) {
	ca := &corev1.ConfigMap{}
	if err := client.Get(ctx, crclient.ObjectKey{Name: caName, Namespace: namespace}, ca); err != nil {
		return "", fmt.Errorf("failed to get issuer certificate authority configmap: %w", err)
	}

	caData, ok := ca.Data[certificateAuthorityConfigMapKey]
	if !ok {
		return "", fmt.Errorf("issuer certificate authority configmap does not contain key %q", certificateAuthorityConfigMapKey)
	}

	return caData, nil
}

func generateClaimMappings(claimMappings configv1.TokenClaimMappings, issuerURL string) (apiserver.ClaimMappings, error) {
	out := apiserver.ClaimMappings{}

	username, err := generateUsernameClaimMapping(claimMappings.Username, issuerURL)
	if err != nil {
		return out, fmt.Errorf("generating username claim mapping: %v", err)
	}

	groups := generateGroupsClaimMapping(claimMappings.Groups)

	out.Username = username
	out.Groups = groups

	if featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUIDAndExtraClaimMappings) {
		uid, err := generateUIDClaimMapping(claimMappings.UID)
		if err != nil {
			return out, fmt.Errorf("generating uid claim mapping: %v", err)
		}

		extras, err := generateExtraClaimMapping(claimMappings.Extra...)
		if err != nil {
			return out, fmt.Errorf("generating extra claim mapping: %v", err)
		}

		out.UID = uid
		out.Extra = extras
	}

	return out, nil
}

func generateUsernameClaimMapping(username configv1.UsernameClaimMapping, issuerURL string) (apiserver.PrefixedClaimOrExpression, error) {
	out := apiserver.PrefixedClaimOrExpression{}

	// Currently, the authentications.config.openshift.io CRD only allows setting a claim for the mapping
	// and does not allow setting a CEL expression like the upstream. This is likely to change in the future,
	// but for now just set the claim.
	out.Claim = username.Claim

	switch username.PrefixPolicy {
	case configv1.Prefix:
		if username.Prefix == nil {
			return out, fmt.Errorf("prefix policy is set to %q but no prefix is specified", configv1.Prefix)
		}
		out.Prefix = &username.Prefix.PrefixString
	case configv1.NoOpinion:
		prefix := ""
		if username.Claim != "email" {
			prefix = fmt.Sprintf("%s#", issuerURL)
		}
		out.Prefix = &prefix
	case configv1.NoPrefix:
		out.Prefix = ptr.To("")
	default:
		return out, fmt.Errorf("unknown prefix policy %q", username.PrefixPolicy)
	}

	return out, nil
}

func generateGroupsClaimMapping(groups configv1.PrefixedClaimMapping) apiserver.PrefixedClaimOrExpression {
	out := apiserver.PrefixedClaimOrExpression{}

	// Currently, the authentications.config.openshift.io CRD only allows setting a claim for the mapping
	// and does not allow setting a CEL expression like the upstream. This is likely to change in the future,
	// but for now just set the claim.
	out.Claim = groups.Claim
	out.Prefix = &groups.Prefix

	return out
}

func generateUIDClaimMapping(uid *configv1.TokenClaimOrExpressionMapping) (apiserver.ClaimOrExpression, error) {
	out := apiserver.ClaimOrExpression{}

	// UID mapping can only specify either claim or expression, not both.
	// This should be rejected at admission time of the authentications.config.openshift.io CRD.
	// Even though this is the case, we still perform a runtime validation to ensure we never
	// attempt to create an invalid configuration.
	// If neither claim or expression is specified, default the claim to "sub"

	switch {
	case uid == nil:
		out.Claim = "sub"
	case uid.Claim != "" && uid.Expression == "":
		out.Claim = uid.Claim
	case uid.Expression != "" && uid.Claim == "":
		err := validateClaimMappingExpression(uid.Expression)
		if err != nil {
			return out, fmt.Errorf("validating CEL expression: %v", err)
		}
		out.Expression = uid.Expression
	case uid.Claim != "" && uid.Expression != "":
		return out, fmt.Errorf("uid mapping must set either claim or expression, not both: %v", uid)
	default:
		return out, fmt.Errorf("unable to handle uid mapping: %v", uid)
	}

	return out, nil
}

func generateExtraClaimMapping(extras ...configv1.ExtraMapping) ([]apiserver.ExtraMapping, error) {
	out := []apiserver.ExtraMapping{}
	errs := []error{}
	for _, extra := range extras {
		outExtra, err := generateExtraMapping(extra)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, outExtra)
	}

	return out, errors.Join(errs...)
}

func generateExtraMapping(extra configv1.ExtraMapping) (apiserver.ExtraMapping, error) {
	out := apiserver.ExtraMapping{}

	if extra.Key == "" {
		return out, errors.New("extra mapping must specify a key, but none was provided")
	}

	if extra.ValueExpression == "" {
		return out, errors.New("extra mapping must specify a valueExpression, but none was provided")
	}

	err := validateExtraMappingExpression(extra.ValueExpression)
	if err != nil {
		return out, fmt.Errorf("validating valueExpression: %v", err)
	}

	out.Key = extra.Key
	out.ValueExpression = extra.ValueExpression

	return out, nil
}

func generateClaimValidationRules(claimValidationRules ...configv1.TokenClaimValidationRule) ([]apiserver.ClaimValidationRule, error) {
	out := []apiserver.ClaimValidationRule{}
	errs := []error{}
	for _, claimValidationRule := range claimValidationRules {
		outRule, err := generateClaimValidationRule(claimValidationRule)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, outRule)
	}

	return out, errors.Join(errs...)
}

func generateClaimValidationRule(claimValidationRule configv1.TokenClaimValidationRule) (apiserver.ClaimValidationRule, error) {
	out := apiserver.ClaimValidationRule{}

	// Currently, the authentications.config.openshift.io CRD only allows setting a claim and required value for the
	// validation rule and does not allow setting a CEL expression and message like the upstream.
	// This is likely to change in the near future to also allow setting a CEL expression.
	switch claimValidationRule.Type {
	case configv1.TokenValidationRuleTypeRequiredClaim:
		if claimValidationRule.RequiredClaim == nil {
			return out, fmt.Errorf("claimValidationRule.type is %s and requiredClaim is not set", configv1.TokenValidationRuleTypeRequiredClaim)
		}

		out.Claim = claimValidationRule.RequiredClaim.Claim
		out.RequiredValue = claimValidationRule.RequiredClaim.RequiredValue
	default:
		return out, fmt.Errorf("unknown claimValidationRule type %q", claimValidationRule.Type)
	}

	return out, nil
}

func validateClaimMappingExpression(expression string) error {
	_, err := authenticationcel.NewDefaultCompiler().CompileClaimsExpression(&authenticationcel.ClaimMappingExpression{Expression: expression})
	return err
}

func validateExtraMappingExpression(expression string) error {
	_, err := authenticationcel.NewDefaultCompiler().CompileClaimsExpression(&authenticationcel.ExtraMappingExpression{Expression: expression})
	return err
}
