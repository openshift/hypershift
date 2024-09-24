package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ClusterStorageOperatorDeployment(ns string) *appsv1.Deployment {
	dep := &appsv1.Deployment{}
	dep.Name = "cluster-storage-operator"
	dep.Namespace = ns
	return dep
}

func ClusterStorageOperatorRole(ns string) *rbacv1.Role {
	role := &rbacv1.Role{}
	role.Name = "cluster-storage-operator"
	role.Namespace = ns
	return role
}

func ClusterStorageOperatorRoleBinding(ns string) *rbacv1.RoleBinding {
	roleBinding := &rbacv1.RoleBinding{}
	roleBinding.Name = "cluster-storage-operator"
	roleBinding.Namespace = ns
	return roleBinding
}

func ClusterStorageOperatorServiceAccount(ns string) *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{}
	sa.Name = "cluster-storage-operator"
	sa.Namespace = ns
	return sa
}

func AWSEBSCSIDriverOperatorKubeconfig(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-ebs-csi-driver-operator-kubeconfig",
			Namespace: ns,
		},
	}
}

func AWSEBSCSIDriverControllerKubeconfig(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-ebs-csi-driver-controller-kubeconfig",
			Namespace: ns,
		},
	}
}
