package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	GlobalPullSecretNamespace = "kube-system"
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

func GlobalPullSecretRole() *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "global-pull-secret-syncer",
			Namespace: GlobalPullSecretNamespace,
		},
	}
}

func GlobalPullSecretRoleBinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "global-pull-secret-syncer",
			Namespace: GlobalPullSecretNamespace,
		},
	}
}
