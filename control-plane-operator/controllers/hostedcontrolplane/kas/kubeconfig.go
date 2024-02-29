package kas

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

const (
	KubeconfigKey = util.KubeconfigKey
)

func ReconcileServiceKubeconfigSecret(secret, cert *corev1.Secret, ca *corev1.ConfigMap, ownerRef config.OwnerRef, platformType hyperv1.PlatformType) error {
	svcURL := InClusterKASURL(platformType)
	return pki.ReconcileKubeConfig(secret, cert, ca, svcURL, "", "service", ownerRef)
}

func ReconcileServiceCAPIKubeconfigSecret(secret, cert *corev1.Secret, ca *corev1.ConfigMap, ownerRef config.OwnerRef, platformType hyperv1.PlatformType) error {
	svcURL := InClusterKASURL(platformType)
	// The client used by CAPI machine controller expects the kubeconfig to have this key
	// https://github.com/kubernetes-sigs/cluster-api/blob/5c85a0a01ee44ecf7c8a3c3fdc867a88af87d73c/util/secret/secret.go#L29-L33
	return pki.ReconcileKubeConfig(secret, cert, ca, svcURL, "value", "capi", ownerRef)
}

func InClusterKASURL(platformType hyperv1.PlatformType) string {
	if platformType == hyperv1.IBMCloudPlatform {
		return fmt.Sprintf("https://%s:%d", manifests.KubeAPIServerServiceName, config.KASSVCIBMCloudPort)
	}
	return fmt.Sprintf("https://%s:%d", manifests.KubeAPIServerServiceName, config.KASSVCPort)
}

func InClusterKASReadyURL(platformType hyperv1.PlatformType) string {
	return InClusterKASURL(platformType) + "/readyz"
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
