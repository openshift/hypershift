package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

func ConfigOperatorDeployment(ns string) *appsv1.Deployment {
	dep := &appsv1.Deployment{}
	dep.Name = "hosted-cluster-config-operator"
	dep.Namespace = ns
	return dep
}

func ConfigOperatorRole(ns string) *rbacv1.Role {
	r := &rbacv1.Role{}
	r.Name = "hosted-cluster-config-operator"
	r.Namespace = ns
	return r
}

func ConfigOperatorRoleBinding(ns string) *rbacv1.RoleBinding {
	rb := &rbacv1.RoleBinding{}
	rb.Name = "hosted-cluster-config-operator"
	rb.Namespace = ns
	return rb
}

func ConfigOperatorServiceAccount(ns string) *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{}
	sa.Name = "hosted-cluster-config-operator"
	sa.Namespace = ns
	return sa
}
