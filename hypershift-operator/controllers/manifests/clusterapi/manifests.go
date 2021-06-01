package clusterapi

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ClusterAPIManagerDeployment(controlPlaneNamespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "cluster-api",
		},
	}
}

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
			Name: "cluster-api",
		},
	}
}

func CAPIManagerClusterRoleBinding(controlPlaneNamespace string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster-api-" + controlPlaneNamespace,
		},
	}
}

func CAPIManagerRole(controlPlaneNamespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "cluster-api",
		},
	}
}

func CAPIManagerRoleBinding(controlPlaneNamespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "cluster-api",
		},
	}
}

func CAPIAWSProviderDeployment(controlPlaneNamespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "capa-controller-manager",
		},
	}
}

func CAPIAWSProviderServiceAccount(controlPlaneNamespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "capa-controller-manager",
		},
	}
}

func CAPIAWSProviderRole(controlPlaneNamespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "capa-manager",
		},
	}
}

func CAPIAWSProviderRoleBinding(controlPlaneNamespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "capa-manager",
		},
	}
}

func CAPIWebhooksTLSSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "capi-webhooks-tls",
		},
	}
}
