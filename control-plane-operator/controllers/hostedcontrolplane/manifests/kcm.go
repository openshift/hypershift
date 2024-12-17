package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func KCMConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kcm-config",
			Namespace: ns,
		},
	}
}

func KCMDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-controller-manager",
			Namespace: ns,
		},
	}
}

func ServiceServingCA(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service-serving-ca",
			Namespace: ns,
		},
	}
}

func KCMServiceMonitor(ns string) *prometheusoperatorv1.ServiceMonitor {
	return &prometheusoperatorv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-controller-manager",
			Namespace: ns,
		},
	}
}

func KCMService(controlPlaneNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-controller-manager",
			Namespace: controlPlaneNamespace,
		},
	}
}

func KCMKubeconfigSecret(controlPlaneNamespace string) *corev1.Secret {
	return secretFor(controlPlaneNamespace, "kube-controller-manager-kubeconfig")
}

func RecyclerConfigMap(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "recycler-config",
			Namespace: ns,
		},
	}
}
