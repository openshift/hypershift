package etcdrecovery

import (
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func EtcdRecoveryRole(ns string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-recovery",
			Namespace: ns,
		},
	}
}

func EtcdRecoveryRoleBinding(ns string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-recovery",
			Namespace: ns,
		},
	}
}

func EtcdRecoveryServiceAccount(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-recovery-sa",
			Namespace: ns,
		},
	}
}

func EtcdRecoveryJob(ns string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-recovery",
			Namespace: ns,
			Labels: map[string]string{
				"app": "etcd-recovery",
			},
		},
	}
}

func EtcdStatefulSet(ns string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd",
			Namespace: ns,
		},
	}
}
