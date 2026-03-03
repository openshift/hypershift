package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func KubeAPIServerServiceMonitor() *prometheusoperatorv1.ServiceMonitor {
	return &prometheusoperatorv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-kube-apiserver",
			Namespace: "openshift-kube-apiserver",
		},
	}
}

func MetricsForwarderDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "control-plane-metrics-forwarder",
			Namespace: "openshift-monitoring",
		},
	}
}

func MetricsForwarderConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metrics-forwarder-config",
			Namespace: "openshift-monitoring",
		},
	}
}

func MetricsForwarderCASecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metrics-forwarder-ca",
			Namespace: "openshift-monitoring",
		},
	}
}

func MetricsForwarderBearerTokenSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "metrics-forwarder-prometheus-token",
			Namespace: "openshift-monitoring",
		},
	}
}

func MetricsForwarderPodMonitor() *prometheusoperatorv1.PodMonitor {
	return &prometheusoperatorv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "control-plane-metrics-forwarder",
			Namespace: "openshift-monitoring",
		},
	}
}
