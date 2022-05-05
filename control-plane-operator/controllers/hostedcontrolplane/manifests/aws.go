package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func AWSProviderConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-cloud-config",
			Namespace: ns,
		},
	}
}

func AWSPodIdentityWebhookKubeconfig(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-pod-identity-webhook-kubeconfig",
			Namespace: ns,
		},
	}
}
