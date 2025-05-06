package kas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	hcpconfig "github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/supportedversion"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/version"
	"k8s.io/apiserver/pkg/apis/apiserver"
	"k8s.io/apiserver/pkg/apis/apiserver/validation"
	authenticationcel "k8s.io/apiserver/pkg/authentication/cel"
	"k8s.io/apiserver/pkg/cel/environment"
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

	err = validateAuthConfig(authConfig, []string{p.ServiceAccountIssuerURL})
	if err != nil {
		return fmt.Errorf("validating generated authentication config: %w", err)
	}

	serializedConfig, err := json.Marshal(authConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize kube apiserver authentication config: %w", err)
	}

	config.Data[AuthenticationConfigKey] = string(serializedConfig)
	return nil
}

func GenerateAuthConfig(spec *configv1.AuthenticationSpec, ctx context.Context, c crclient.Client, namespace string) (*AuthenticationConfiguration, error) {
	config := &AuthenticationConfiguration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AuthenticationConfiguration",
			APIVersion: "apiserver.config.k8s.io/v1alpha1",
		},
		JWT: []JWTAuthenticator{},
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

func generateJWTForProvider(ctx context.Context, provider configv1.OIDCProvider, client crclient.Client, namespace string) (JWTAuthenticator, error) {
	out := JWTAuthenticator{}

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

func generateIssuer(ctx context.Context, issuer configv1.TokenIssuer, client crclient.Client, namespace string) (Issuer, error) {
	out := Issuer{}

	out.URL = issuer.URL
	out.AudienceMatchPolicy = AudienceMatchPolicyMatchAny

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

func generateClaimMappings(claimMappings configv1.TokenClaimMappings, issuerURL string) (ClaimMappings, error) {
	out := ClaimMappings{}

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

func generateUsernameClaimMapping(username configv1.UsernameClaimMapping, issuerURL string) (PrefixedClaimOrExpression, error) {
	out := PrefixedClaimOrExpression{}

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

func generateGroupsClaimMapping(groups configv1.PrefixedClaimMapping) PrefixedClaimOrExpression {
	out := PrefixedClaimOrExpression{}

	// Currently, the authentications.config.openshift.io CRD only allows setting a claim for the mapping
	// and does not allow setting a CEL expression like the upstream. This is likely to change in the future,
	// but for now just set the claim.
	out.Claim = groups.Claim
	out.Prefix = &groups.Prefix

	return out
}

func generateUIDClaimMapping(uid *configv1.TokenClaimOrExpressionMapping) (ClaimOrExpression, error) {
	out := ClaimOrExpression{}

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

func generateExtraClaimMapping(extras ...configv1.ExtraMapping) ([]ExtraMapping, error) {
	out := []ExtraMapping{}
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

func generateExtraMapping(extra configv1.ExtraMapping) (ExtraMapping, error) {
	out := ExtraMapping{}

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

func generateClaimValidationRules(claimValidationRules ...configv1.TokenClaimValidationRule) ([]ClaimValidationRule, error) {
	out := []ClaimValidationRule{}
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

func generateClaimValidationRule(claimValidationRule configv1.TokenClaimValidationRule) (ClaimValidationRule, error) {
	out := ClaimValidationRule{}

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

func validateAuthConfig(authConfig *AuthenticationConfiguration, disallowIssuers []string) error {
	if authConfig == nil {
		// nothing to validate
		return nil
	}

	// TODO: implement logic for getting the current/desired version for the control plane and get the corresponding kube version based on that.
	// For now, always use the minimum supported OCP version to ensure we are never getting false positives when validating CEL expression compiliation.
	// Older versions of Kubernetes are not guaranteed to have the same CEL libraries available as newer ones.
	// Always using the minimum supported OCP version will likely result in false negatives and the workaround is for users to adapt their CEL expressions
	// accordingly.
	// The current line of thinking is that false negatives are better than false positives because false positives could result in invalid configurations
	// attempting to be rolled out.
	kubeVersion, err := supportedversion.GetKubeVersionForSupportedVersion(supportedversion.MinSupportedVersion)
	if err != nil {
		return fmt.Errorf("getting the corresponding kubernetes version for OCP version %q", supportedversion.MinSupportedVersion.String())
	}

	envVersion, err := version.Parse(kubeVersion.String())
	if err != nil {
		return fmt.Errorf("parsing kubernetes version %q", kubeVersion.String())
	}
	celCompiler := authenticationcel.NewCompiler(environment.MustBaseEnvSet(envVersion, true))

	apiServerAuthConfig, err := HCPAuthConfigToAPIServerAuthConfig(authConfig)
	if err != nil {
		return fmt.Errorf("converting from HCP auth config type to apiserver auth config type: %v", err)
	}

	fieldErrors := validation.ValidateAuthenticationConfiguration(celCompiler, apiServerAuthConfig, disallowIssuers)
	return fieldErrors.ToAggregate()
}

// HCPAuthConfigToAPIServerAuthConfig converts the HyperShift version of the AuthenticationConfiguration type
// to the upstream AuthenticationConfiguration type. It returns any errors encountered during the conversion process.
// This is a useful utility function to enable us to use the upstream AuthenticationConfiguration validations
// while still producing an AuthenticationConfiguration output that works with an earlier Kubernetes API server.
func HCPAuthConfigToAPIServerAuthConfig(authConfig *AuthenticationConfiguration) (*apiserver.AuthenticationConfiguration, error) {
	outBytes, err := json.Marshal(authConfig)
	if err != nil {
		return nil, fmt.Errorf("marshalling HCP auth config to JSON: %v", err)
	}

	apiserverAuthConfig := &apiserver.AuthenticationConfiguration{}
	err = json.Unmarshal(outBytes, apiserverAuthConfig)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling HCP auth config JSON to apiserver auth config: %v", err)
	}

	return apiserverAuthConfig, nil
}
