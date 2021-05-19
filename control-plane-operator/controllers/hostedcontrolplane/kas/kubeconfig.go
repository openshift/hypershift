package kas

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
)

const (
	KubeconfigKey = "kubeconfig"
)

func ReconcileServiceKubeconfigSecret(secret, cert, ca *corev1.Secret, ownerRef config.OwnerRef, apiServerPort int32) error {
	svcURL := fmt.Sprintf("https://%s:%d", manifests.KASService(secret.Namespace).Name, apiServerPort)
	return reconcileKubeconfig(secret, cert, ca, svcURL, "", ownerRef)
}

func ReconcileServiceCAPIKubeconfigSecret(secret, cert, ca *corev1.Secret, ownerRef config.OwnerRef, apiServerPort int32) error {
	svcURL := fmt.Sprintf("https://%s:%d", manifests.KASService(secret.Namespace).Name, apiServerPort)
	// The client used by CAPI machine controller expects the kubeconfig to have this key
	// https://github.com/kubernetes-sigs/cluster-api/blob/5c85a0a01ee44ecf7c8a3c3fdc867a88af87d73c/util/secret/secret.go#L29-L33
	return reconcileKubeconfig(secret, cert, ca, svcURL, "value", ownerRef)
}

func ReconcileLocalhostKubeconfigSecret(secret, cert, ca *corev1.Secret, ownerRef config.OwnerRef, apiServerPort int32) error {
	localhostURL := fmt.Sprintf("https://localhost:%d", apiServerPort)
	return reconcileKubeconfig(secret, cert, ca, localhostURL, "", ownerRef)
}

func ReconcileExternalKubeconfigSecret(secret, cert, ca *corev1.Secret, ownerRef config.OwnerRef, externalURL, secretKey string) error {
	return reconcileKubeconfig(secret, cert, ca, externalURL, secretKey, ownerRef)
}

func ReconcileBootstrapKubeconfigSecret(secret, cert, ca *corev1.Secret, ownerRef config.OwnerRef, externalURL string) error {
	return reconcileKubeconfig(secret, cert, ca, externalURL, "", ownerRef)
}

func reconcileKubeconfig(secret, cert, ca *corev1.Secret, url, key string, ownerRef config.OwnerRef) error {
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
