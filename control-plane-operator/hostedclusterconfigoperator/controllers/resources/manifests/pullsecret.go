package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	GlobalPullSecretDSName    = "global-pull-secret-syncer"
	GlobalPullSecretNamespace = "kube-system"
	NodePullSecretPath        = "/var/lib/kubelet/config.json"
)

func GlobalPullSecretServiceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      GlobalPullSecretDSName,
			Namespace: GlobalPullSecretNamespace,
		},
	}
}

func PullSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: ns,
		},
	}
}

func PullSecretTargetNamespaces() []string {
	return []string{
		"openshift-config",
		"openshift",
	}
}

func CombinedPullSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "combined-pull-secret",
			Namespace: ns,
		},
	}
}
