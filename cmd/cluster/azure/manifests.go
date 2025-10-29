package azure

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Package azure provides Azure-specific manifest generation for HyperShift clusters.

// credentialSecret creates a Secret resource for Azure cloud credentials.
// The secret name is constructed by appending "-cloud-credentials" to the provided name parameter.
func credentialSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-cloud-credentials",
			Namespace: namespace,
		},
	}
}

// serviceAccountTokenIssuerSecret creates a Secret resource for the service account token issuer.
// The secret is used to store the OIDC issuer configuration for service account token validation.
func serviceAccountTokenIssuerSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}
