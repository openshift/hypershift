package kas

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
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

func ReconcileAuthenticationTokenWebhookConfigSecret(secret *corev1.Secret, ownerRef config.OwnerRef, authenticatorSecret *corev1.Secret) error {
	ownerRef.ApplyTo(secret)
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	var ca, crt, key []byte
	var ok bool
	if ca, ok = authenticatorSecret.Data[certs.CASignerCertMapKey]; !ok {
		return fmt.Errorf("expected %s key in authenticator secret", certs.CASignerCertMapKey)
	}
	if crt, ok = authenticatorSecret.Data[corev1.TLSCertKey]; !ok {
		return fmt.Errorf("expected %s key in authenticator secret", corev1.TLSCertKey)
	}
	if key, ok = authenticatorSecret.Data[corev1.TLSPrivateKeyKey]; !ok {
		return fmt.Errorf("expected %s key in authenticator secret", corev1.TLSPrivateKeyKey)
	}
	url := fmt.Sprintf("https://openshift-oauth-apiserver.%s.svc:443/apis/oauth.openshift.io/v1/tokenreviews", secret.GetNamespace())
	kubeConfigBytes, err := generateAuthenticationTokenWebhookConfig(url, crt, key, ca)
	if err != nil {
		return err
	}
	secret.Data[KubeconfigKey] = kubeConfigBytes
	return nil
}

func generateAuthenticationTokenWebhookConfig(url string, crtBytes, keyBytes, caBytes []byte) ([]byte, error) {
	kubeCfg := clientcmdapi.Config{
		Kind:       "Config",
		APIVersion: "v1",
	}
	kubeCfg.Clusters = map[string]*clientcmdapi.Cluster{
		"local-cluster": {
			Server:                   url,
			CertificateAuthorityData: caBytes,
		},
	}
	kubeCfg.AuthInfos = map[string]*clientcmdapi.AuthInfo{
		"openshift-authenticator": {
			ClientCertificateData: crtBytes,
			ClientKeyData:         keyBytes,
		},
	}
	kubeCfg.Contexts = map[string]*clientcmdapi.Context{
		"local-context": {
			Cluster:  "local-cluster",
			AuthInfo: "openshift-authenticator",
		},
	}
	kubeCfg.CurrentContext = "local-context"
	return clientcmd.Write(kubeCfg)
}
