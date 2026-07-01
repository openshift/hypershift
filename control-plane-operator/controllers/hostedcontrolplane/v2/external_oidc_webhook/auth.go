package extoidc

import (
	"encoding/json"
	"fmt"

	oauthapiserver "github.com/openshift/cluster-authentication-operator/pkg/controllers/externaloidc/generation/oauthapiserver"

	"github.com/openshift/hypershift/control-plane-operator/featuregates"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	authConfigDataKey   = "auth-config.json"
	caBundleDataKey     = "ca-bundle.crt"
	clientSecretDataKey = "clientSecret"
)

func adaptAuthConfig(cpContext component.WorkloadContext, config *corev1.ConfigMap) error {
	configuration := cpContext.HCP.Spec.Configuration
	if configuration == nil || configuration.Authentication == nil || len(configuration.Authentication.OIDCProviders) == 0 {
		return nil
	}

	caResolver := func(name string) (string, error) {
		cm := &corev1.ConfigMap{}
		if err := cpContext.Client.Get(cpContext, crclient.ObjectKey{Name: name, Namespace: cpContext.HCP.Namespace}, cm); err != nil {
			return "", fmt.Errorf("failed to get CA configmap %q: %w", name, err)
		}
		return cm.Data[caBundleDataKey], nil
	}

	clientSecretResolver := func(name string) (string, error) {
		secret := &corev1.Secret{}
		if err := cpContext.Client.Get(cpContext, crclient.ObjectKey{Name: name, Namespace: cpContext.HCP.Namespace}, secret); err != nil {
			return "", fmt.Errorf("failed to get client secret %q: %w", name, err)
		}
		return string(secret.Data[clientSecretDataKey]), nil
	}

	gen := oauthapiserver.NewAuthenticationConfigurationGenerator(caResolver, clientSecretResolver).
		WithExternalClaimsSourcing()

	if featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUIDAndExtraClaimMappings) {
		gen.WithAdditionalClaimMappings()
	}

	if featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUpstreamParity) {
		gen.WithUpstreamParity()
	}

	authConfig, err := gen.GenerateAuthenticationConfiguration(cpContext.HCP.Spec.Configuration.Authentication)
	if err != nil {
		return fmt.Errorf("failed to generate authentication config: %w", err)
	}

	// TODO implement validation logic
	// err = validateAuthConfig(authConfig, []string{kas.ServiceAccountIssuerURL(cpContext.HCP)})
	// if err != nil {
	// 	return fmt.Errorf("validating generated authentication config: %w", err)
	// }

	serializedConfig, err := json.Marshal(authConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize external-oidc-webhook authentication config: %w", err)
	}

	if config.Data == nil {
		config.Data = map[string]string{}
	}
	config.Data[authConfigDataKey] = string(serializedConfig)

	return nil
}
