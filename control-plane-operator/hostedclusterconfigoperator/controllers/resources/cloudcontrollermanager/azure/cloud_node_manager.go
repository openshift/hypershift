package azure

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const CloudNodeManagerName = "azure-cloud-node-manager"

var labels = map[string]string{
	"k8s-app": CloudNodeManagerName,
}

func CloudNodeManagerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   CloudNodeManagerName,
			Labels: labels,
		},
	}
}

func CloudNodeManagerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   CloudNodeManagerName,
			Labels: labels,
		},
	}
}

func CloudNodeManagerServiceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CloudNodeManagerName,
			Namespace: "kube-system",
			Labels:    labels,
		},
	}
}

func CloudNodeManagerDaemonSet() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CloudNodeManagerName,
			Namespace: "kube-system",
			Labels:    labels,
		},
	}
}
