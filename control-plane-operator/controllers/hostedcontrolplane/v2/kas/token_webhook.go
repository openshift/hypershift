package kas

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/certs"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

func adaptAuthenticationTokenWebhookConfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	if configuration := cpContext.HCP.Spec.Configuration; configuration != nil && usesExternalOIDCAsWebhook(configuration.Authentication) {
		return adaptExternalOIDCTokenWebhookConfigSecret(cpContext, secret)
	}

	return adaptIntegratedOAuthTokenWebhookConfigSecret(cpContext, secret)
}

func adaptExternalOIDCTokenWebhookConfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	webhookCASecret := &corev1.Secret{}
	if err := cpContext.Client.Get(cpContext, client.ObjectKey{
		Namespace: cpContext.HCP.Namespace,
		Name:      "external-oidc-webhook-ca",
	}, webhookCASecret); err != nil {
		return fmt.Errorf("failed to get external OIDC webhook CA secret: %w", err)
	}

	ca, found := webhookCASecret.Data[corev1.TLSCertKey]
	if !found {
		return fmt.Errorf("expected %s key in external OIDC webhook CA secret", corev1.TLSCertKey)
	}

	url := fmt.Sprintf("https://external-oidc-webhook.%s.svc:443/apis/oauth.openshift.io/v1/tokenreviews", secret.GetNamespace())
	kubeConfigBytes, err := generateAuthenticationTokenWebhookKubeconfig(url, nil, nil, ca)
	if err != nil {
		return err
	}

	secret.Data[KubeconfigKey] = kubeConfigBytes
	return nil
}

func adaptIntegratedOAuthTokenWebhookConfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	authenticatorCertSecret := manifests.OpenshiftAuthenticatorCertSecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(authenticatorCertSecret), authenticatorCertSecret); err != nil {
		return fmt.Errorf("failed to get authenticator cert secret: %w", err)
	}
	rootCA := manifests.RootCASecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca cert secret: %w", err)
	}

	var ca, crt, key []byte
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
	kubeConfigBytes, err := generateAuthenticationTokenWebhookKubeconfig(url, crt, key, ca)
	if err != nil {
		return err
	}
	secret.Data[KubeconfigKey] = kubeConfigBytes
	return nil
}
