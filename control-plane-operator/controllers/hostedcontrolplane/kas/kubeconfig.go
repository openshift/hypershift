package kas

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

const (
	KubeconfigKey = util.KubeconfigKey
)

func ReconcileServiceKubeconfigSecret(secret, cert, ca *corev1.Secret, ownerRef config.OwnerRef, apiServerPort int32) error {
	svcURL := InClusterKASURL(secret.Namespace, apiServerPort)
	return reconcileKubeconfig(secret, cert, ca, svcURL, "", "service", ownerRef)
}

func ReconcileServiceCAPIKubeconfigSecret(secret, cert, ca *corev1.Secret, ownerRef config.OwnerRef, apiServerPort int32) error {
	svcURL := InClusterKASURL(secret.Namespace, apiServerPort)
	// The client used by CAPI machine controller expects the kubeconfig to have this key
	// https://github.com/kubernetes-sigs/cluster-api/blob/5c85a0a01ee44ecf7c8a3c3fdc867a88af87d73c/util/secret/secret.go#L29-L33
	return reconcileKubeconfig(secret, cert, ca, svcURL, "value", "capi", ownerRef)
}

func ReconcileIngressOperatorKubeconfigSecret(secret, ca *corev1.Secret, ownerRef config.OwnerRef, apiServerPort int32) error {
	svcURL := InClusterKASURL(secret.Namespace, apiServerPort)
	// The secret that holds the kubeconfig and the one that holds the certs are the same
	return reconcileKubeconfig(secret, secret, ca, svcURL, "", "ingress-operator", ownerRef)
}

func InClusterKASURL(namespace string, apiServerPort int32) string {
	return fmt.Sprintf("https://%s:%d", manifests.KubeAPIServerServiceName, apiServerPort)
}

func InClusterKASReadyURL(namespace string, securePort *int32) string {
	var apiPort int32
	apiPort = config.DefaultAPIServerPort
	if securePort != nil {
		apiPort = *securePort
	}
	return InClusterKASURL(namespace, apiPort) + "/readyz"
}

func ReconcileLocalhostKubeconfigSecret(secret, cert, ca *corev1.Secret, ownerRef config.OwnerRef, apiServerPort int32) error {
	localhostURL := fmt.Sprintf("https://localhost:%d", apiServerPort)
	return reconcileKubeconfig(secret, cert, ca, localhostURL, "", manifests.KubeconfigScopeLocal, ownerRef)
}

func ReconcileExternalKubeconfigSecret(secret, cert, ca *corev1.Secret, ownerRef config.OwnerRef, externalURL, secretKey string) error {
	return reconcileKubeconfig(secret, cert, ca, externalURL, secretKey, manifests.KubeconfigScopeExternal, ownerRef)
}

func ReconcileBootstrapKubeconfigSecret(secret, cert, ca *corev1.Secret, ownerRef config.OwnerRef, externalURL string) error {
	return reconcileKubeconfig(secret, cert, ca, externalURL, "", manifests.KubeconfigScopeBootstrap, ownerRef)
}

func reconcileKubeconfig(secret, cert, ca *corev1.Secret, url string, key string, scope manifests.KubeconfigScope, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(secret)
	caBytes := ca.Data[pki.CASignerCertMapKey]
	crtBytes, keyBytes := cert.Data[corev1.TLSCertKey], cert.Data[corev1.TLSPrivateKeyKey]
	kubeCfgBytes, err := generateKubeConfig(url, crtBytes, keyBytes, caBytes)
	if err != nil {
		return fmt.Errorf("failed to generate kubeconfig: %w", err)
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	if key == "" {
		key = KubeconfigKey
	}
	if secret.Labels == nil {
		secret.Labels = map[string]string{}
	}
	secret.Labels[manifests.KubeconfigScopeLabel] = string(scope)
	secret.Data[key] = kubeCfgBytes
	return nil
}

func generateKubeConfig(url string, crtBytes, keyBytes, caBytes []byte) ([]byte, error) {
	kubeCfg := clientcmdapi.Config{
		Kind:       "Config",
		APIVersion: "v1",
	}
	kubeCfg.Clusters = map[string]*clientcmdapi.Cluster{
		"cluster": {
			Server:                   url,
			CertificateAuthorityData: caBytes,
		},
	}
	kubeCfg.AuthInfos = map[string]*clientcmdapi.AuthInfo{
		"admin": {
			ClientCertificateData: crtBytes,
			ClientKeyData:         keyBytes,
		},
	}
	kubeCfg.Contexts = map[string]*clientcmdapi.Context{
		"admin": {
			Cluster:   "cluster",
			AuthInfo:  "admin",
			Namespace: "default",
		},
	}
	kubeCfg.CurrentContext = "admin"
	return clientcmd.Write(kubeCfg)
}
