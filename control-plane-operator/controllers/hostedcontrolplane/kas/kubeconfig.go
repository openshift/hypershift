package kas

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	KubeconfigKey = util.KubeconfigKey
)

func ReconcileServiceKubeconfigSecret(secret, cert *corev1.Secret, ca *corev1.ConfigMap, ownerRef config.OwnerRef, apiServerPort int32) error {
	svcURL := InClusterKASURL(secret.Namespace, apiServerPort)
	return pki.ReconcileKubeConfig(secret, cert, ca, svcURL, "", "service", ownerRef)
}

func ReconcileServiceCAPIKubeconfigSecret(secret, cert *corev1.Secret, ca *corev1.ConfigMap, ownerRef config.OwnerRef, apiServerPort int32, capiClusterName string) error {
	svcURL := InClusterKASURL(secret.Namespace, apiServerPort)
	// The client used by CAPI machine controller expects the kubeconfig to have this key
	// https://github.com/kubernetes-sigs/cluster-api/blob/5c85a0a01ee44ecf7c8a3c3fdc867a88af87d73c/util/secret/secret.go#L29-L33
	// and to be labeled with cluster.x-k8s.io/cluster-name=<clusterName> so the secret can be cached by the client.
	// https://github.com/kubernetes-sigs/cluster-api/blob/8ba3f47b053da8bbf63cf407c930a2ee10bfd754/main.go#L304
	if secret.Labels == nil {
		secret.Labels = make(map[string]string)
	}
	secret.Labels[capiv1.ClusterLabelName] = capiClusterName

	return pki.ReconcileKubeConfig(secret, cert, ca, svcURL, "value", "capi", ownerRef)
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

func ReconcileLocalhostKubeconfigSecret(secret, cert *corev1.Secret, ca *corev1.ConfigMap, ownerRef config.OwnerRef, apiServerPort int32) error {
	localhostURL := fmt.Sprintf("https://localhost:%d", apiServerPort)
	return pki.ReconcileKubeConfig(secret, cert, ca, localhostURL, "", manifests.KubeconfigScopeLocal, ownerRef)
}

func ReconcileExternalKubeconfigSecret(secret, cert *corev1.Secret, ca *corev1.ConfigMap, ownerRef config.OwnerRef, externalURL, secretKey string) error {
	return pki.ReconcileKubeConfig(secret, cert, ca, externalURL, secretKey, manifests.KubeconfigScopeExternal, ownerRef)
}

func ReconcileBootstrapKubeconfigSecret(secret, cert *corev1.Secret, ca *corev1.ConfigMap, ownerRef config.OwnerRef, externalURL string) error {
	return pki.ReconcileKubeConfig(secret, cert, ca, externalURL, "", manifests.KubeconfigScopeBootstrap, ownerRef)
}
