package manifests

import (
	supportmanifests "github.com/openshift/hypershift/support/manifests"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AzureProviderConfig is a re-export for backward compatibility.
// New code should import from support/manifests directly.
var AzureProviderConfig = supportmanifests.AzureProviderConfig

func AzureKMSWithCredentials(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-kms-config",
			Namespace: ns,
		},
	}
}

func AzureWorkloadIdentityWebhookKubeconfig(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-workload-identity-webhook-kubeconfig",
			Namespace: ns,
		},
	}
}
