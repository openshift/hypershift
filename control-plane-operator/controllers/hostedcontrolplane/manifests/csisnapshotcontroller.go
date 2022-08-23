package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

func CSISnapshotControllerOperatorDeployment(ns string) *appsv1.Deployment {
	dep := &appsv1.Deployment{}
	dep.Name = "csi-snapshot-controller-operator"
	dep.Namespace = ns
	return dep
}

func CSISnapshotControllerOperatorRole(ns string) *rbacv1.Role {
	role := &rbacv1.Role{}
	role.Name = "csi-snapshot-controller-operator-role"
	role.Namespace = ns
	return role
}

func CSISnapshotControllerOperatorRoleBinding(ns string) *rbacv1.RoleBinding {
	roleBinding := &rbacv1.RoleBinding{}
	roleBinding.Name = "csi-snapshot-controller-operator-role"
	roleBinding.Namespace = ns
	return roleBinding
}

func CSISnapshotControllerOperatorServiceAccount(ns string) *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{}
	sa.Name = "csi-snapshot-controller-operator"
	sa.Namespace = ns
	return sa
}
