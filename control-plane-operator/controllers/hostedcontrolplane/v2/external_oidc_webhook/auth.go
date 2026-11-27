package extoidc

import (
	"encoding/json"
	"fmt"

	certs "github.com/openshift/hypershift/support/certs"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/proxy"

	externaloidc "github.com/openshift/library-go/pkg/operator/externaloidc"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	authConfigDataKey = "auth-config.json"
)

func adaptAuthConfig(cpContext component.WorkloadContext, authCfgMap *corev1.ConfigMap) error {
	configuration := cpContext.HCP.Spec.Configuration
	if configuration == nil || configuration.Authentication == nil {
		return nil
	}

	gen := externaloidc.NewAuthenticationConfigurationGenerator(
		certs.ConfigMapCABundleResolver(cpContext, cpContext.Client, cpContext.HCP.Namespace),
		externaloidc.WithTrustedCAFilePath(authProxyTrustedCAFilePathResolver(cpContext)),
	)

	// The service-account issuer is stored separately on the HCP. Supply it to the
	// generator so it can reject conflicting external OIDC issuers.
	authn := configuration.Authentication.DeepCopy()
	authn.ServiceAccountIssuer = cpContext.HCP.Spec.IssuerURL
	authConfig, err := gen.Generate(authn)
	if err != nil {
		return fmt.Errorf("failed to generate authentication config: %w", err)
	}

	serializedConfig, err := json.Marshal(authConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize external-oidc-webhook authentication config: %w", err)
	}

	if authCfgMap.Data == nil {
		authCfgMap.Data = map[string]string{}
	}
	authCfgMap.Data[authConfigDataKey] = string(serializedConfig)

	return nil
}

// authProxyTrustedCAFilePathResolver returns the resolver for the
// trustedCAFilePath of the generated authentication configuration: the mounted
// auth proxy CA bundle path when the auth-scoped proxy override carries a
// trusted CA, and an empty string (system trust roots only) otherwise.
//
// The path is deferred until the CA bundle ConfigMap actually exists in the
// HCP namespace: the HyperShift operator writes it there under the fixed
// destination name, and the generated configuration, the CA mount, and the
// running replica are reconciled by three independent asynchronous paths.
// Publishing the path before the bundle is synchronized would point the
// webhook at a file that is not mounted yet. Returning an empty string until
// then keeps the webhook on system trust; the next reconciliation after the
// synchronization emits the path.
func authProxyTrustedCAFilePathResolver(cpContext component.WorkloadContext) externaloidc.TrustedCAFilePathResolver {
	return func() (string, error) {
		authProxy := proxy.AuthProxyOverride(cpContext.HCP)
		if authProxy == nil || authProxy.TrustedCA.Name == "" {
			return "", nil
		}

		cm := &corev1.ConfigMap{}
		key := client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: proxy.AuthProxyTrustedCAConfigMapName}
		if err := cpContext.Client.Get(cpContext, key, cm); err != nil {
			if apierrors.IsNotFound(err) {
				return "", nil
			}
			return "", fmt.Errorf("failed to get auth proxy trusted CA configmap %s: %w", key, err)
		}

		return proxy.AuthProxyTrustedCAFilePath, nil
	}
}
