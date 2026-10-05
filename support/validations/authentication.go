package validations

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/kas"
	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/supportedversion"

	configv1 "github.com/openshift/api/config/v1"
	externaloidc "github.com/openshift/library-go/pkg/operator/externaloidc"

	"k8s.io/apimachinery/pkg/util/version"
	"k8s.io/apiserver/pkg/apis/apiserver/validation"
	"k8s.io/apiserver/pkg/authentication/cel"
	"k8s.io/apiserver/pkg/cel/environment"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func ValidateAuthenticationSpec(ctx context.Context, client crclient.Client, authn *configv1.AuthenticationSpec, namespace string, serviceAccountIssuer string) error {
	if authn == nil {
		// nothing to validate
		return nil
	}

	switch authn.Type {
	case configv1.AuthenticationTypeOIDC:
		if featuregates.Gate().Enabled(featuregates.ExternalOIDCAsWebhook) {
			return validateAuthenticationSpecForTypeOIDCAsWebhook(ctx, client, authn, namespace, serviceAccountIssuer)
		}
		return validateAuthenticationSpecForTypeOIDC(ctx, client, authn, namespace, serviceAccountIssuer)
	case configv1.AuthenticationTypeNone, configv1.AuthenticationTypeIntegratedOAuth:
		// TODO: For now, defer any validations of these configurations to the standard reconciliation loop.
		// Ideally, there is any necessary additional validations for each of these types explicitly created,
		// similar to the OIDC type above.
	default:
		return fmt.Errorf("unknown type %q", authn.Type)
	}

	return nil
}

func validateAuthenticationSpecForTypeOIDCAsWebhook(ctx context.Context, client crclient.Client, authn *configv1.AuthenticationSpec, namespace string, serviceAccountIssuer string) error {
	if authn == nil {
		// nothing to validate
		return nil
	}

	celCompiler, err := minimumSupportedCELCompiler()
	if err != nil {
		return err
	}

	gen := externaloidc.NewAuthenticationConfigurationGenerator(
		certs.ConfigMapCABundleResolver(ctx, client, namespace),
		externaloidc.WithCELCompiler(celCompiler),
	)

	// The legacy KAS validator receives the service-account issuer separately as a
	// disallowed issuer. The webhook generator performs the same overlap check from
	// AuthenticationSpec.ServiceAccountIssuer, so populate a copy without mutating
	// the caller's configuration.
	authnForGeneration := authn.DeepCopy()
	authnForGeneration.ServiceAccountIssuer = serviceAccountIssuer

	if _, err := gen.Generate(authnForGeneration); err != nil {
		return fmt.Errorf("generating external OIDC webhook authentication configuration: %w", err)
	}

	return nil
}

func validateAuthenticationSpecForTypeOIDC(ctx context.Context, client crclient.Client, authn *configv1.AuthenticationSpec, namespace string, serviceAccountIssuer string) error {
	if authn == nil {
		// nothing to validate
		return nil
	}

	authConfig, err := kas.GenerateAuthConfig(ctx, authn, client, namespace)
	if err != nil {
		return fmt.Errorf("generating structured authentication configuration: %w", err)
	}

	celCompiler, err := minimumSupportedCELCompiler()
	if err != nil {
		return err
	}

	apiServerAuthConfig, err := kas.HCPAuthConfigToAPIServerAuthConfig(authConfig)
	if err != nil {
		return fmt.Errorf("converting from HCP auth config type to apiserver auth config type: %w", err)
	}

	fieldErrors := validation.ValidateAuthenticationConfiguration(celCompiler, apiServerAuthConfig, []string{serviceAccountIssuer})
	return fieldErrors.ToAggregate()
}

func minimumSupportedCELCompiler() (cel.Compiler, error) {
	// TODO: implement logic for getting the current/desired version for the control plane and get the corresponding kube version based on that.
	// For now, always use the minimum supported OCP version to ensure we are never getting false positives when validating CEL expression compilation.
	// Older versions of Kubernetes are not guaranteed to have the same CEL libraries available as newer ones.
	// Always using the minimum supported OCP version will likely result in false negatives and the workaround is for users to adapt their CEL expressions
	// accordingly.
	// The current line of thinking is that false negatives are better than false positives because false positives could result in invalid configurations
	// attempting to be rolled out.
	kubeVersion, err := supportedversion.GetKubeVersionForSupportedVersion(supportedversion.MinSupportedVersion)
	if err != nil {
		return nil, fmt.Errorf("getting the corresponding kubernetes version for OCP version %q: %w", supportedversion.MinSupportedVersion.String(), err)
	}

	envVersion, err := version.Parse(kubeVersion.String())
	if err != nil {
		return nil, fmt.Errorf("parsing kubernetes version %q: %w", kubeVersion.String(), err)
	}

	return cel.NewCompiler(environment.MustBaseEnvSet(envVersion)), nil
}
