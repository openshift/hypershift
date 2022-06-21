package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CCMDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cloud-controller-manager",
			Namespace: ns,
		},
	}
}

func CCMConfigMap(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cloud-config",
			Namespace: ns,
		},
	}
}

func CCMContainer() *corev1.Container {
	return &corev1.Container{
		Name: "cloud-controller-manager",
	}
}
