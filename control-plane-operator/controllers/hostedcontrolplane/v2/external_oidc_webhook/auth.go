package extoidc

import (
	"encoding/json"
	"fmt"

	component "github.com/openshift/hypershift/support/controlplane-component"

	externaloidc "github.com/openshift/library-go/pkg/operator/externaloidc"

	corev1 "k8s.io/api/core/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	authConfigDataKey = "auth-config.json"
	caBundleDataKey   = "ca-bundle.crt"
)

func adaptAuthConfig(cpContext component.WorkloadContext, authCfgMap *corev1.ConfigMap) error {
	configuration := cpContext.HCP.Spec.Configuration
	if configuration == nil || configuration.Authentication == nil {
		return nil
	}

	caResolver := func(name string) (string, error) {
		cm := &corev1.ConfigMap{}
		if err := cpContext.Client.Get(cpContext, crclient.ObjectKey{Name: name, Namespace: cpContext.HCP.Namespace}, cm); err != nil {
			return "", fmt.Errorf("failed to get CA configmap %q: %w", name, err)
		}

		if value, keyFound := cm.Data[caBundleDataKey]; !keyFound {
			return "", fmt.Errorf("CA configmap %q key %q is missing", name, caBundleDataKey)
		} else if len(value) == 0 {
			return "", fmt.Errorf("CA configmap %q key %q is empty", name, caBundleDataKey)
		}

		return cm.Data[caBundleDataKey], nil
	}

	gen := externaloidc.NewAuthenticationConfigurationGenerator(caResolver)
	authConfig, err := gen.Generate(configuration.Authentication)
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
