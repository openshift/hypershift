package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AzureProviderConfig is a configMap for Azure cloud config. This is needed for ignition configuration by the
// machine-config-operator (MCO). https://github.com/openshift/machine-config-operator/blob/fe8353e4ea7e72dfd69105069b870a37a87478ec/pkg/operator/bootstrap.go#L124
func AzureProviderConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-cloud-config",
			Namespace: ns,
		},
	}
}

func AzureProviderConfigWithCredentials(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-cloud-config",
			Namespace: ns,
		},
	}
}

func AzureDiskConfigWithCredentials(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-disk-csi-config",
			Namespace: ns,
		},
	}
}

func AzureFileConfigWithCredentials(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-file-csi-config",
			Namespace: ns,
		},
	}
}
