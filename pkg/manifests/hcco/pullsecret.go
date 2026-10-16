package hcco

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	GlobalPullSecretDSName    = "global-pull-secret-syncer"
	GlobalPullSecretNamespace = "kube-system"
)

func AdditionalPullSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "additional-pull-secret",
			Namespace: GlobalPullSecretNamespace,
		},
	}
}

func GlobalPullSecretDaemonSet() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "global-pull-secret-syncer",
			Namespace: GlobalPullSecretNamespace,
		},
	}
}

func OriginalPullSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "original-pull-secret",
			Namespace: GlobalPullSecretNamespace,
		},
	}
}

func GlobalPullSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "global-pull-secret",
			Namespace: GlobalPullSecretNamespace,
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}
}
