package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	etcdv1 "github.com/openshift/hypershift/control-plane-operator/thirdparty/etcd/v1beta2"
)

func EtcdOperatorServiceAccount(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-operator",
			Namespace: ns,
		},
	}
}

func EtcdOperatorRole(ns string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-operator",
			Namespace: ns,
		},
	}
}

func EtcdOperatorRoleBinding(ns string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-operator",
			Namespace: ns,
		},
	}
}

func EtcdOperatorDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-operator",
			Namespace: ns,
		},
	}
}

func EtcdCluster(ns string) *etcdv1.EtcdCluster {
	return &etcdv1.EtcdCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd",
			Namespace: ns,
		},
	}
}
