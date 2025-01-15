package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AzureProviderConfig is a configMap for azure config.
// TODO (alberto): can we drop this completely?
// It has some consumers atm: it's reconciled into guest cluster, ignition local provider. Review them and drop it.
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
