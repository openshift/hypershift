package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	GlobalPullSecretDSName       = "global-pull-secret-syncer"
	GlobalPullSecretNamespace    = "kube-system"
	NodePullSecretPath           = "/var/lib/kubelet/config.json"
	GlobalPullSecretFinalizer    = "hypershift.openshift.io/global-pull-secret-finalizer"
	OriginalPullSecretAnnotation = "hypershift.openshift.io/gps-original-pull-secret"
)

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

func GlobalPullSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "global-pull-secret",
			Namespace: GlobalPullSecretNamespace,
		},
		Type: corev1.SecretTypeDockerConfigJson,
	}
}

func GlobalPullSecretServiceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "global-pull-secret-syncer",
			Namespace: GlobalPullSecretNamespace,
		},
	}
}

func GlobalPullSecretRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "global-pull-secret-syncer",
		},
	}
}

func GlobalPullSecretRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "global-pull-secret-syncer",
		},
	}
}
