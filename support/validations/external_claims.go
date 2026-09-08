package validations

import (
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strconv"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/cel/environment"

	"github.com/google/cel-go/cel"
)

// These POC checks follow oauth-apiserver's externaloidc authentication validator
// at 9e9722dd2f3f. TODO: use that validator directly once our Kubernetes dependency
// provides the ExtendableCompiler API it requires. Secret and CA contents are
// deliberately left to reference validation; this function performs no I/O.
var (
	externalClaimsHostname = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*(:([1-9]\d{0,4}))?$`)
	externalClaimName      = regexp.MustCompile(`^[a-z_]+$`)
	clientIDCharacters     = regexp.MustCompile(`^[[:print:]]+$`)
	oauthScopeCharacters   = regexp.MustCompile(`^[!#-[\]-~]+$`)
)

func validateExternalClaimsSources(sources []configv1.ExternalClaimsSource, path *field.Path) field.ErrorList {
	if len(sources) == 0 {
		return nil
	}

	// Use the vendored CEL environment for this POC. TODO: check release-specific
	// CEL compatibility separately from the 5.1 feature availability check.
	base, err := environment.MustBaseEnvSet(environment.DefaultCompatibilityVersion()).Env(environment.StoredExpressions)
	if err != nil {
		return field.ErrorList{field.InternalError(path, err)}
	}
	claimsEnv, err := base.Extend(cel.Variable("claims", cel.MapType(cel.StringType, cel.DynType)))
	if err != nil {
		return field.ErrorList{field.InternalError(path, err)}
	}
	responseEnv, err := base.Extend(cel.Variable("response", cel.MapType(cel.StringType, cel.DynType)))
	if err != nil {
		return field.ErrorList{field.InternalError(path, err)}
	}

	var errs field.ErrorList
	if len(sources) > 5 {
		errs = append(errs, field.TooMany(path, len(sources), 5))
	}
	seenNames := sets.New[string]()
	for i, source := range sources {
		sourcePath := path.Index(i)
		hostname := source.URL.Hostname
		u, err := url.Parse("https://" + hostname)
		if !externalClaimsHostname.MatchString(hostname) || err != nil {
			errs = append(errs, field.Invalid(sourcePath.Child("url", "hostname"), hostname, "must be an RFC1123 hostname with an optional non-zero port"))
		} else if port, _ := strconv.Atoi(u.Port()); port > 65535 {
			errs = append(errs, field.Invalid(sourcePath.Child("url", "hostname"), hostname, "port must not exceed 65535"))
		}
		errs = append(errs, validateExternalClaimsExpression(claimsEnv, source.URL.PathExpression, false, sourcePath.Child("url", "pathExpression"))...)

		if len(source.Mappings) == 0 {
			errs = append(errs, field.Required(sourcePath.Child("mappings"), "at least one mapping is required"))
		} else if len(source.Mappings) > 16 {
			errs = append(errs, field.TooMany(sourcePath.Child("mappings"), len(source.Mappings), 16))
		}
		for j, mapping := range source.Mappings {
			mappingPath := sourcePath.Child("mappings").Index(j)
			if !externalClaimName.MatchString(mapping.Name) || len(mapping.Name) > 256 {
				errs = append(errs, field.Invalid(mappingPath.Child("name"), mapping.Name, "must contain 1 to 256 lowercase letters or underscores"))
			}
			if seenNames.Has(mapping.Name) {
				errs = append(errs, field.Duplicate(mappingPath.Child("name"), mapping.Name))
			}
			seenNames.Insert(mapping.Name)
			errs = append(errs, validateExternalClaimsExpression(responseEnv, mapping.Expression, false, mappingPath.Child("expression"))...)
		}

		if len(source.Predicates) > 16 {
			errs = append(errs, field.TooMany(sourcePath.Child("predicates"), len(source.Predicates), 16))
		}
		seenPredicates := sets.New[string]()
		for j, predicate := range source.Predicates {
			predicatePath := sourcePath.Child("predicates").Index(j).Child("expression")
			if seenPredicates.Has(predicate.Expression) {
				errs = append(errs, field.Duplicate(predicatePath, predicate.Expression))
			}
			seenPredicates.Insert(predicate.Expression)
			errs = append(errs, validateExternalClaimsExpression(claimsEnv, predicate.Expression, true, predicatePath)...)
		}
		errs = append(errs, validateExternalClaimsAuthentication(source.Authentication, sourcePath.Child("authentication"))...)
	}
	return errs
}

func validateExternalClaimsExpression(env *cel.Env, expression string, requireBool bool, path *field.Path) field.ErrorList {
	if expression == "" {
		return field.ErrorList{field.Required(path, "expression must not be empty")}
	}
	ast, issues := env.Compile(expression)
	if issues.Err() != nil {
		return field.ErrorList{field.Invalid(path, expression, fmt.Sprintf("error compiling expression: %v", issues.Err()))}
	}
	if requireBool && ast.OutputType() != cel.BoolType {
		return field.ErrorList{field.Invalid(path, expression, "must evaluate to bool")}
	}
	// As in the webhook, path and mapping result types are checked at runtime.
	if _, err := env.Program(ast); err != nil {
		return field.ErrorList{field.Invalid(path, expression, fmt.Sprintf("error creating CEL program: %v", err))}
	}
	return nil
}

func validateExternalClaimsAuthentication(auth configv1.ExternalSourceAuthentication, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	switch auth.Type {
	case "", configv1.ExternalSourceAuthenticationTypeRequestProvidedToken:
		if !reflect.DeepEqual(auth.ClientCredential, configv1.ClientCredentialConfig{}) {
			errs = append(errs, field.Forbidden(path.Child("clientCredential"), "requires type ClientCredential"))
		}
	case configv1.ExternalSourceAuthenticationTypeClientCredential:
		cfg := auth.ClientCredential
		path = path.Child("clientCredential")
		if !clientIDCharacters.MatchString(cfg.ClientID) {
			errs = append(errs, field.Invalid(path.Child("clientID"), cfg.ClientID, "must be non-empty printable ASCII"))
		}
		if cfg.ClientSecret.Name == "" {
			errs = append(errs, field.Required(path.Child("clientSecret", "name"), "a client secret reference is required"))
		}
		u, err := url.Parse(cfg.TokenEndpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.Path == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			errs = append(errs, field.Invalid(path.Child("tokenEndpoint"), cfg.TokenEndpoint, "must be an HTTPS URL with a host and path, without user information, query parameters, or fragment"))
		}
		for i, scope := range cfg.Scopes {
			if !oauthScopeCharacters.MatchString(string(scope)) {
				errs = append(errs, field.Invalid(path.Child("scopes").Index(i), scope, "must be non-empty printable ASCII without spaces, double quotes, or backslashes"))
			}
		}
	default:
		errs = append(errs, field.NotSupported(path.Child("type"), auth.Type, []string{string(configv1.ExternalSourceAuthenticationTypeRequestProvidedToken), string(configv1.ExternalSourceAuthenticationTypeClientCredential)}))
	}
	return errs
}
