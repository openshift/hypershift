package kas

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
)

func ReconcileOauthMetadata(cfg *corev1.ConfigMap, ownerRef config.OwnerRef, externalOAuthAddress string, externalOAuthPort int32) error {
	ownerRef.ApplyTo(cfg)
	if cfg.Data == nil {
		cfg.Data = map[string]string{}
	}
	oauthURL := fmt.Sprintf("https://%s:%d", externalOAuthAddress, externalOAuthPort)
	cfg.Data[OauthMetadataConfigKey] = fmt.Sprintf(oauthMetadata, oauthURL)
	return nil
}

const oauthMetadata = `{
"issuer": "%[1]s",
"authorization_endpoint": "%[1]s/oauth/authorize",
"token_endpoint": "%[1]s/oauth/token",
  "scopes_supported": [
    "user:check-access",
    "user:full",
    "user:info",
    "user:list-projects",
    "user:list-scoped-projects"
  ],
  "response_types_supported": [
    "code",
    "token"
  ],
  "grant_types_supported": [
    "authorization_code",
    "implicit"
  ],
  "code_challenge_methods_supported": [
    "plain",
    "S256"
  ]
}
`
