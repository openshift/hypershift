package gcpnth

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const ComponentName = "gcp-node-termination-handler"

var labels = map[string]string{
	"k8s-app": ComponentName,
}

func ServiceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ComponentName,
			Namespace: metav1.NamespaceSystem,
			Labels:    labels,
		},
	}
}

func ClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   ComponentName,
			Labels: labels,
		},
	}
}

func ClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   ComponentName,
			Labels: labels,
		},
	}
}

func DaemonSet() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ComponentName,
			Namespace: metav1.NamespaceSystem,
			Labels:    labels,
		},
	}
}
