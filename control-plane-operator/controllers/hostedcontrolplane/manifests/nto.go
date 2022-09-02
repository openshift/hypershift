package manifests

import (
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Deployment
func ClusterNodeTuningOperatorDeployment(namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-node-tuning-operator",
			Namespace: namespace,
		},
	}
}

// Metrics
func ClusterNodeTuningOperatorMetricsService(namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-tuning-operator",
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
		},
	}
}

func ClusterNodeTuningOperatorServiceMonitor(namespace string) *prometheusoperatorv1.ServiceMonitor {
	return &prometheusoperatorv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "node-tuning-operator",
			Namespace: namespace,
		},
	}
}
