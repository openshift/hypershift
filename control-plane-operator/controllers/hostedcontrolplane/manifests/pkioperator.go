package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func PKIOperatorDeployment(controlPlaneOperatorNamespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      "control-plane-pki-operator",
		},
	}
}

func PKIOperatorServiceAccount(controlPlaneOperatorNamespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      "control-plane-pki-operator",
		},
	}
}

func PKIOperatorRole(controlPlaneOperatorNamespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      "control-plane-pki-operator",
		},
	}
}

func PKIOperatorRoleBinding(controlPlaneOperatorNamespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      "control-plane-pki-operator",
		},
	}
}
