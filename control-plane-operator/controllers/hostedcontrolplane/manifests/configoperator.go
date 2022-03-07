package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
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

func ConfigOperatorPodMonitor(ns string) *prometheusoperatorv1.PodMonitor {
	return &prometheusoperatorv1.PodMonitor{ObjectMeta: metav1.ObjectMeta{
		Namespace: ns,
		Name:      "hosted-cluster-config-operator",
	}}
}
