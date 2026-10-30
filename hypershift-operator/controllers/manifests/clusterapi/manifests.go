package clusterapi

import (
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CAPIManagerServiceAccount(controlPlaneNamespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "cluster-api",
		},
	}
}

func CAPIManagerClusterRole(controlPlaneNamespace string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-cluster-api",
		},
	}
}

func CAPIManagerClusterRoleBinding(controlPlaneNamespace string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-cluster-api-" + controlPlaneNamespace,
		},
	}
}

func CAPIProviderServiceAccount(controlPlaneNamespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "capi-provider",
		},
	}
}
