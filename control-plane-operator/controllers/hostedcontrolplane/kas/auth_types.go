package kas

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// TODO
// We are currently copying this type from k8s.io/apiserver because it is
// not yet in the kube base we use for 4.16.
// In 4.17, we should switch back to using the type in k8s.io/apiserver
// and remove this copy
// https://github.com/openshift/kubernetes/pull/1881

// AuthenticationConfiguration provides versioned configuration for authentication.
type AuthenticationConfiguration struct {
	metav1.TypeMeta

	// jwt is a list of authenticator to authenticate Kubernetes users using
	// JWT compliant tokens. The authenticator will attempt to parse a raw ID token,
	// verify it's been signed by the configured issuer. The public key to verify the
	// signature is discovered from the issuer's public endpoint using OIDC discovery.
	// For an incoming token, each JWT authenticator will be attempted in
	// the order in which it is specified in this list.  Note however that
	// other authenticators may run before or after the JWT authenticators.
	// The specific position of JWT authenticators in relation to other
	// authenticators is neither defined nor stable across releases.  Since
	// each JWT authenticator must have a unique issuer URL, at most one
	// JWT authenticator will attempt to cryptographically validate the token.
	JWT []JWTAuthenticator `json:"jwt"`
}

// JWTAuthenticator provides the configuration for a single JWT authenticator.
type JWTAuthenticator struct {
	// issuer contains the basic OIDC provider connection options.
	// +required
	Issuer Issuer `json:"issuer"`

	// claimValidationRules are rules that are applied to validate token claims to authenticate users.
	// +optional
	ClaimValidationRules []ClaimValidationRule `json:"claimValidationRules,omitempty"`

	// claimMappings points claims of a token to be treated as user attributes.
	// +required
	ClaimMappings ClaimMappings `json:"claimMappings"`

	// userValidationRules are rules that are applied to final user before completing authentication.
	// These allow invariants to be applied to incoming identities such as preventing the
	// use of the system: prefix that is commonly used by Kubernetes components.
	// The validation rules are logically ANDed together and must all return true for the validation to pass.
	// +optional
	UserValidationRules []UserValidationRule `json:"userValidationRules,omitempty"`
}

// Issuer provides the configuration for a external provider specific settings.
type Issuer struct {
	// url points to the issuer URL in a format https://url or https://url/path.
	// This must match the "iss" claim in the presented JWT, and the issuer returned from discovery.
	// Same value as the --oidc-issuer-url flag.
	// Used to fetch discovery information unless overridden by discoveryURL.
	// Required to be unique.
	// Note that egress selection configuration is not used for this network connection.
	// +required
	URL string `json:"url"`

	// certificateAuthority contains PEM-encoded certificate authority certificates
	// used to validate the connection when fetching discovery information.
	// If unset, the system verifier is used.
	// Same value as the content of the file referenced by the --oidc-ca-file flag.
	// +optional
	CertificateAuthority string `json:"certificateAuthority,omitempty"`

	// audiences is the set of acceptable audiences the JWT must be issued to.
	// At least one of the entries must match the "aud" claim in presented JWTs.
	// Same value as the --oidc-client-id flag (though this field supports an array).
	// Required to be non-empty.
	// +required
	Audiences []string `json:"audiences"`

	// audienceMatchPolicy defines how the "audiences" field is used to match the "aud" claim in the presented JWT.
	// Allowed values are:
	// 1. "MatchAny" when multiple audiences are specified and
	// 2. empty (or unset) or "MatchAny" when a single audience is specified.
	//
	// - MatchAny: the "aud" claim in the presented JWT must match at least one of the entries in the "audiences" field.
	// For example, if "audiences" is ["foo", "bar"], the "aud" claim in the presented JWT must contain either "foo" or "bar" (and may contain both).
	//
	// - "": The match policy can be empty (or unset) when a single audience is specified in the "audiences" field. The "aud" claim in the presented JWT must contain the single audience (and may contain others).
	//
	// For more nuanced audience validation, use claimValidationRules.
	//   example: claimValidationRule[].expression: 'sets.equivalent(claims.aud, ["bar", "foo", "baz"])' to require an exact match.
	// +optional
	AudienceMatchPolicy AudienceMatchPolicyType `json:"audienceMatchPolicy,omitempty"`
}

// AudienceMatchPolicyType is a set of valid values for Issuer.AudienceMatchPolicy
type AudienceMatchPolicyType string

// Valid types for AudienceMatchPolicyType
const (
	// MatchAny means the "aud" claim in the presented JWT must match at least one of the entries in the "audiences" field.
	AudienceMatchPolicyMatchAny AudienceMatchPolicyType = "MatchAny"
)

// ClaimValidationRule provides the configuration for a single claim validation rule.
type ClaimValidationRule struct {
	// claim is the name of a required claim.
	// Same as --oidc-required-claim flag.
	// Only string claim keys are supported.
	// Mutually exclusive with expression and message.
	// +optional
	Claim string `json:"claim,omitempty"`
	// requiredValue is the value of a required claim.
	// Same as --oidc-required-claim flag.
	// Only string claim values are supported.
	// If claim is set and requiredValue is not set, the claim must be present with a value set to the empty string.
	// Mutually exclusive with expression and message.
	// +optional
	RequiredValue string `json:"requiredValue,omitempty"`

	// expression represents the expression which will be evaluated by CEL.
	// Must produce a boolean.
	//
	// CEL expressions have access to the contents of the token claims, organized into CEL variable:
	// - 'claims' is a map of claim names to claim values.
	//   For example, a variable named 'sub' can be accessed as 'claims.sub'.
	//   Nested claims can be accessed using dot notation, e.g. 'claims.email.verified'.
	// Must return true for the validation to pass.
	//
	// Documentation on CEL: https://kubernetes.io/docs/reference/using-api/cel/
	//
	// Mutually exclusive with claim and requiredValue.
	// +optional
	Expression string `json:"expression,omitempty"`
	// message customizes the returned error message when expression returns false.
	// message is a literal string.
	// Mutually exclusive with claim and requiredValue.
	// +optional
	Message string `json:"message,omitempty"`
}

// ClaimMappings provides the configuration for claim mapping
type ClaimMappings struct {
	// username represents an option for the username attribute.
	// The claim's value must be a singular string.
	// Same as the --oidc-username-claim and --oidc-username-prefix flags.
	// If username.expression is set, the expression must produce a string value.
	//
	// In the flag based approach, the --oidc-username-claim and --oidc-username-prefix are optional. If --oidc-username-claim is not set,
	// the default value is "sub". For the authentication config, there is no defaulting for claim or prefix. The claim and prefix must be set explicitly.
	// For claim, if --oidc-username-claim was not set with legacy flag approach, configure username.claim="sub" in the authentication config.
	// For prefix:
	//     (1) --oidc-username-prefix="-", no prefix was added to the username. For the same behavior using authentication config,
	//         set username.prefix=""
	//     (2) --oidc-username-prefix="" and  --oidc-username-claim != "email", prefix was "<value of --oidc-issuer-url>#". For the same
	//         behavior using authentication config, set username.prefix="<value of issuer.url>#"
	//	   (3) --oidc-username-prefix="<value>". For the same behavior using authentication config, set username.prefix="<value>"
	// +required
	Username PrefixedClaimOrExpression `json:"username"`
	// groups represents an option for the groups attribute.
	// The claim's value must be a string or string array claim.
	// If groups.claim is set, the prefix must be specified (and can be the empty string).
	// If groups.expression is set, the expression must produce a string or string array value.
	//  "", [], and null values are treated as the group mapping not being present.
	// +optional
	Groups PrefixedClaimOrExpression `json:"groups,omitempty"`

	// uid represents an option for the uid attribute.
	// Claim must be a singular string claim.
	// If uid.expression is set, the expression must produce a string value.
	// +optional
	UID ClaimOrExpression `json:"uid"`

	// extra represents an option for the extra attribute.
	// expression must produce a string or string array value.
	// If the value is empty, the extra mapping will not be present.
	//
	// hard-coded extra key/value
	// - key: "foo"
	//   valueExpression: "'bar'"
	// This will result in an extra attribute - foo: ["bar"]
	//
	// hard-coded key, value copying claim value
	// - key: "foo"
	//   valueExpression: "claims.some_claim"
	// This will result in an extra attribute - foo: [value of some_claim]
	//
	// hard-coded key, value derived from claim value
	// - key: "admin"
	//   valueExpression: '(has(claims.is_admin) && claims.is_admin) ? "true":""'
	// This will result in:
	//  - if is_admin claim is present and true, extra attribute - admin: ["true"]
	//  - if is_admin claim is present and false or is_admin claim is not present, no extra attribute will be added
	//
	// +optional
	Extra []ExtraMapping `json:"extra,omitempty"`
}

// PrefixedClaimOrExpression provides the configuration for a single prefixed claim or expression.
type PrefixedClaimOrExpression struct {
	// claim is the JWT claim to use.
	// Mutually exclusive with expression.
	// +optional
	Claim string `json:"claim,omitempty"`
	// prefix is prepended to claim's value to prevent clashes with existing names.
	// prefix needs to be set if claim is set and can be the empty string.
	// Mutually exclusive with expression.
	// +optional
	Prefix *string `json:"prefix,omitempty"`

	// expression represents the expression which will be evaluated by CEL.
	//
	// CEL expressions have access to the contents of the token claims, organized into CEL variable:
	// - 'claims' is a map of claim names to claim values.
	//   For example, a variable named 'sub' can be accessed as 'claims.sub'.
	//   Nested claims can be accessed using dot notation, e.g. 'claims.email.verified'.
	//
	// Documentation on CEL: https://kubernetes.io/docs/reference/using-api/cel/
	//
	// Mutually exclusive with claim and prefix.
	// +optional
	Expression string `json:"expression,omitempty"`
}

// ClaimOrExpression provides the configuration for a single claim or expression.
type ClaimOrExpression struct {
	// claim is the JWT claim to use.
	// Either claim or expression must be set.
	// Mutually exclusive with expression.
	// +optional
	Claim string `json:"claim,omitempty"`

	// expression represents the expression which will be evaluated by CEL.
	//
	// CEL expressions have access to the contents of the token claims, organized into CEL variable:
	// - 'claims' is a map of claim names to claim values.
	//   For example, a variable named 'sub' can be accessed as 'claims.sub'.
	//   Nested claims can be accessed using dot notation, e.g. 'claims.email.verified'.
	//
	// Documentation on CEL: https://kubernetes.io/docs/reference/using-api/cel/
	//
	// Mutually exclusive with claim.
	// +optional
	Expression string `json:"expression,omitempty"`
}

// ExtraMapping provides the configuration for a single extra mapping.
type ExtraMapping struct {
	// key is a string to use as the extra attribute key.
	// key must be a domain-prefix path (e.g. example.org/foo). All characters before the first "/" must be a valid
	// subdomain as defined by RFC 1123. All characters trailing the first "/" must
	// be valid HTTP Path characters as defined by RFC 3986.
	// key must be lowercase.
	// +required
	Key string `json:"key"`

	// valueExpression is a CEL expression to extract extra attribute value.
	// valueExpression must produce a string or string array value.
	// "", [], and null values are treated as the extra mapping not being present.
	// Empty string values contained within a string array are filtered out.
	//
	// CEL expressions have access to the contents of the token claims, organized into CEL variable:
	// - 'claims' is a map of claim names to claim values.
	//   For example, a variable named 'sub' can be accessed as 'claims.sub'.
	//   Nested claims can be accessed using dot notation, e.g. 'claims.email.verified'.
	//
	// Documentation on CEL: https://kubernetes.io/docs/reference/using-api/cel/
	//
	// +required
	ValueExpression string `json:"valueExpression"`
}

// UserValidationRule provides the configuration for a single user info validation rule.
type UserValidationRule struct {
	// expression represents the expression which will be evaluated by CEL.
	// Must return true for the validation to pass.
	//
	// CEL expressions have access to the contents of UserInfo, organized into CEL variable:
	// - 'user' - authentication.k8s.io/v1, Kind=UserInfo object
	//    Refer to https://github.com/kubernetes/api/blob/release-1.28/authentication/v1/types.go#L105-L122 for the definition.
	//    API documentation: https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.28/#userinfo-v1-authentication-k8s-io
	//
	// Documentation on CEL: https://kubernetes.io/docs/reference/using-api/cel/
	//
	// +required
	Expression string `json:"expression"`

	// message customizes the returned error message when rule returns false.
	// message is a literal string.
	// +optional
	Message string `json:"message,omitempty"`
}
