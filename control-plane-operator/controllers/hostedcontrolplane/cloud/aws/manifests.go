package aws

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CCMServiceAccount(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-cloud-controller-manager",
			Namespace: ns,
		},
	}
}

func CCMRole(ns string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-cloud-controller-manager",
			Namespace: ns,
		},
	}
}

func CCMRoleBinding(ns string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-cloud-controller-manager",
			Namespace: ns,
		},
	}
}

func CCMDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-cloud-controller-manager",
			Namespace: ns,
		},
	}
}

func CCMCloudConfigMap(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-cloud-config",
			Namespace: ns,
		},
	}
}

func CCMControllerCredsSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cloud-controller-creds",
			Namespace: ns,
		},
	}
}

func CCMContainer() *corev1.Container {
	return &corev1.Container{
		Name: "cloud-controller-manager",
	}
}

func ccmVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func ccmCloudConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "cloud-config",
	}
}

func ccmCloudControllerCreds() *corev1.Volume {
	return &corev1.Volume{
		Name: "cloud-controller-creds",
	}
}
