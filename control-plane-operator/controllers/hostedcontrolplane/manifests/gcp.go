package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GCPWorkloadIdentityFederationWebhookKubeconfig(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gcp-workload-identity-federation-webhook-kubeconfig",
			Namespace: ns,
		},
	}
}
