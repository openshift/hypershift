package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
