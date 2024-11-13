package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

func OpenShiftControllerManagerConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-controller-manager-config",
			Namespace: ns,
		},
	}
}

func OpenShiftRouteControllerManagerConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-route-controller-manager-config",
			Namespace: ns,
		},
	}
}

func OpenShiftControllerManagerDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-controller-manager",
			Namespace: ns,
		},
	}
}

func OpenShiftControllerService(controlPlaneNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-controller-manager",
			Namespace: controlPlaneNamespace,
		},
	}
}

func OpenShiftControllerServiceMonitor(ns string) *prometheusoperatorv1.ServiceMonitor {
	return &prometheusoperatorv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-controller-manager",
			Namespace: ns,
		},
	}
}
