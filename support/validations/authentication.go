package validations

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/support/supportedversion"
	"k8s.io/apimachinery/pkg/util/version"
	"k8s.io/apiserver/pkg/apis/apiserver/validation"
	"k8s.io/apiserver/pkg/authentication/cel"
	"k8s.io/apiserver/pkg/cel/environment"
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

	// TODO: implement logic for getting the current/desired version for the control plane and get the corresponding ube version based on that.
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
	celCompiler := cel.NewCompiler(environment.MustBaseEnvSet(envVersion, true))

	fieldErrors := validation.ValidateAuthenticationConfiguration(celCompiler, authConfig, disallowIssuers)
	return fieldErrors.ToAggregate()
}
