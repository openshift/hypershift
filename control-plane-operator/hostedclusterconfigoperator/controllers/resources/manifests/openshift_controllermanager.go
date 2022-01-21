package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const OpenShiftControllerManagerOperatorNamespace = "openshift-controller-manager-operator"

func OpenShiftControllerManagerServiceCA() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-service-ca",
			Namespace: "openshift-controller-manager",
		},
	}
}
func OpenShiftControllerManagerConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-controller-manager-config",
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
