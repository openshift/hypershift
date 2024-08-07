package etcdrecovery

import (
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func EtcdRecoveryServiceAccount(hcpNamespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "etcd-recovery-sa",
			Namespace: hcpNamespace,
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
