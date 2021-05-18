package util

import (
	corev1 "k8s.io/api/core/v1"
)

func EnsurePullSecret(serviceAccount *corev1.ServiceAccount, secretName string) {
	for _, secretRef := range serviceAccount.ImagePullSecrets {
		if secretRef.Name == secretName {
			// secret is already part of image pull secrets
			return
		}
	}
	serviceAccount.ImagePullSecrets = append(serviceAccount.ImagePullSecrets, corev1.LocalObjectReference{
		Name: secretName,
	})
}
