package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func ImageRegistryOperatorDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-image-registry-operator",
			Namespace: ns,
		},
	}
}

func ImageRegistryOperatorServingCert(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-image-registry-operator",
			Namespace: ns,
		},
	}
}

func ImageRegistryOperatorPodMonitor(ns string) *prometheusoperatorv1.PodMonitor {
	return &prometheusoperatorv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-image-registry-operator",
			Namespace: ns,
		},
	}
}
