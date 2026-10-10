package kas

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"

	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/reconcilerpolicy"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func enableOAuthMetadata(cpContext component.WorkloadContext) bool {
	configuration := cpContext.HCP.Spec.Configuration
	return configuration == nil || reconcilerpolicy.ConfigOAuthEnabled(configuration.Authentication)
}

func adaptOauthMetadata(cpContext component.WorkloadContext, cfg *corev1.ConfigMap) error {
	configuration := cpContext.HCP.Spec.Configuration
	if configuration != nil && configuration.Authentication != nil && len(configuration.Authentication.OAuthMetadata.Name) > 0 {
		var userOauthMetadataConfigMap corev1.ConfigMap
		key := client.ObjectKey{Namespace: cpContext.HCP.Namespace, Name: configuration.Authentication.OAuthMetadata.Name}
		if err := cpContext.Client.Get(cpContext, key, &userOauthMetadataConfigMap); err != nil {
			return fmt.Errorf("failed to get user oauth metadata configmap: %w", err)
		}
		if len(userOauthMetadataConfigMap.Data) == 0 {
			return fmt.Errorf("user oauth metadata configmap %s has no data", userOauthMetadataConfigMap.Name)
		}
		if _, ok := userOauthMetadataConfigMap.Data["oauthMetadata"]; !ok {
			return fmt.Errorf("user oauth metadata configmap %s has no 'oauthMetadata' key", userOauthMetadataConfigMap.Name)
		}

		cfg.Data[OauthMetadataConfigKey] = userOauthMetadataConfigMap.Data["oauthMetadata"]
		return nil
	}

	var oauthMetadata map[string]any
	if err := json.Unmarshal([]byte(cfg.Data[OauthMetadataConfigKey]), &oauthMetadata); err != nil {
		return fmt.Errorf("failed to unmarshal oauth metadata: %w", err)
	}

	// Use net.JoinHostPort so IPv6 NodePort addresses are bracketed (RFC 3986).
	oauthURL := (&url.URL{
		Scheme: "https",
		Host:   net.JoinHostPort(cpContext.InfraStatus.OAuthHost, strconv.Itoa(int(cpContext.InfraStatus.OAuthPort))),
	}).String()
	oauthMetadata["issuer"] = oauthURL
	oauthMetadata["authorization_endpoint"] = fmt.Sprintf("%s/oauth/authorize", oauthURL)
	oauthMetadata["token_endpoint"] = fmt.Sprintf("%s/oauth/token", oauthURL)

	data, err := json.Marshal(oauthMetadata)
	if err != nil {
		return fmt.Errorf("failed to marshal oauth metadata: %w", err)
	}
	cfg.Data[OauthMetadataConfigKey] = string(data)
	return nil
}
