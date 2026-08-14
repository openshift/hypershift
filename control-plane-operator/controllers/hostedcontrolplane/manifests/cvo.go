package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const clusterVersionOperator = "cluster-version-operator"

func ClusterVersionOperatorDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterVersionOperator,
			Namespace: ns,
		},
	}
}

func ClusterVersionOperatorRole(ns string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterVersionOperator,
			Namespace: ns,
		},
	}
}

func ClusterVersionOperatorRoleBinding(ns string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterVersionOperator,
			Namespace: ns,
		},
	}
}

func ClusterVersionOperatorServiceAccount(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterVersionOperator,
			Namespace: ns,
		},
	}
}

func ClusterVersionOperatorServiceMonitor(ns string) *prometheusoperatorv1.ServiceMonitor {
	return &prometheusoperatorv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterVersionOperator,
			Namespace: ns,
		},
	}
}

func ClusterVersionOperatorService(controlPlaneNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterVersionOperator,
			Namespace: controlPlaneNamespace,
		},
	}
}
