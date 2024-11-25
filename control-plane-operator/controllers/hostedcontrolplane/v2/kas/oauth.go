package kas

import (
	"encoding/json"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/certs"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"

	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

	var oauthMetadata map[string]interface{}
	if err := json.Unmarshal([]byte(cfg.Data[OauthMetadataConfigKey]), &oauthMetadata); err != nil {
		return fmt.Errorf("failed to unmarshal oauth metadata: %v", err)
	}

	oauthURL := fmt.Sprintf("https://%s:%d", cpContext.InfraStatus.OAuthHost, cpContext.InfraStatus.OAuthPort)
	oauthMetadata["issuer"] = oauthURL
	oauthMetadata["authorization_endpoint"] = fmt.Sprintf("%s/oauth/authorize", oauthURL)
	oauthMetadata["token_endpoint"] = fmt.Sprintf("%s/oauth/token", oauthURL)

	data, err := json.Marshal(oauthMetadata)
	if err != nil {
		return fmt.Errorf("failed to marshal oauth metadata: %v", err)
	}
	cfg.Data[OauthMetadataConfigKey] = string(data)
	return nil
}

func adaptAuthenticationTokenWebhookConfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	authenticatorCertSecret := manifests.OpenshiftAuthenticatorCertSecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(authenticatorCertSecret), authenticatorCertSecret); err != nil {
		return fmt.Errorf("failed to get authenticator cert secret: %w", err)
	}
	rootCA := manifests.RootCAConfigMap(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca cert secret: %w", err)
	}

	var ca string
	var crt, key []byte
	var ok bool
	if ca, ok = rootCA.Data[certs.CASignerCertMapKey]; !ok {
		return fmt.Errorf("expected %s key in the root CA configMap", certs.CASignerCertMapKey)
	}
	if crt, ok = authenticatorCertSecret.Data[corev1.TLSCertKey]; !ok {
		return fmt.Errorf("expected %s key in authenticator secret", corev1.TLSCertKey)
	}
	if key, ok = authenticatorCertSecret.Data[corev1.TLSPrivateKeyKey]; !ok {
		return fmt.Errorf("expected %s key in authenticator secret", corev1.TLSPrivateKeyKey)
	}
	url := fmt.Sprintf("https://openshift-oauth-apiserver.%s.svc:443/apis/oauth.openshift.io/v1/tokenreviews", secret.GetNamespace())
	kubeConfigBytes, err := generateAuthenticationTokenWebhookConfig(url, crt, key, []byte(ca))
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
