package validations

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"k8s.io/apiserver/pkg/apis/apiserver/validation"
	"k8s.io/apiserver/pkg/authentication/cel"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func ValidateAuthenticationSpec(ctx context.Context, client crclient.Client, authn *configv1.AuthenticationSpec, namespace string, disallowIssuers []string) error {
	if authn == nil {
		// nothing to validate
		return nil
	}

	switch authn.Type {
	case configv1.AuthenticationTypeOIDC:
		return ValidateAuthenticationSpecForTypeOIDC(ctx, client, authn, namespace, disallowIssuers)
	case configv1.AuthenticationTypeNone, configv1.AuthenticationTypeIntegratedOAuth:
		// TODO: For now, defer any validations of these configurations to the standard reconciliation loop.
		// Ideally, there is any necessary additional validations for each of these types explicitly created,
		// similar to the OIDC type above.
	default:
		return fmt.Errorf("unknown type %q", authn.Type)
	}

	return nil
}

func ValidateAuthenticationSpecForTypeOIDC(ctx context.Context, client crclient.Client, authn *configv1.AuthenticationSpec, namespace string, disallowIssuers []string) error {
	if authn == nil {
		// nothing to validate
		return nil
	}

	authConfig, err := kas.GenerateAuthConfig(authn, ctx, client, namespace)
	if err != nil {
		return fmt.Errorf("generating structured authentication configuration: %w", err)
	}

    // TODO: using the default compiler means that we are allowing CEL library usage for the Kube version that maps to our
    // dependency import. This should align with the version of Kubernetes that will be running for a guest cluster instead
    // since that is what will actually load and validate the configuration.
    fieldErrors := validation.ValidateAuthenticationConfiguration(cel.NewDefaultCompiler() , authConfig, disallowIssuers)
    return fieldErrors.ToAggregate()
}
