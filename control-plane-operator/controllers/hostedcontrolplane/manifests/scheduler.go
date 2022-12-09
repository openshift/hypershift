package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func SchedulerConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-scheduler",
			Namespace: ns,
		},
	}
}

func SchedulerDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-scheduler",
			Namespace: ns,
		},
	}
}

func SchedulerKubeconfigSecret(controlPlaneNS string) *corev1.Secret {
	return secretFor(controlPlaneNS, "kube-scheduler-kubeconfig")
}
