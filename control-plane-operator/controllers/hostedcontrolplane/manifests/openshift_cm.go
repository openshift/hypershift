package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

func OpenShiftControllerManagerServiceCAWorkerManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-openshift-controller-manager-service-ca",
			Namespace: ns,
		},
	}
}

func OpenShiftControllerManagerNamespaceWorkerManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-openshift-controller-manager-namespace",
			Namespace: ns,
		},
	}
}

func OpenShiftControllerManagerServiceCA() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-service-ca",
			Namespace: "openshift-controller-manager",
		},
	}
}

func OpenShiftControllerManagerNamespace() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-controller-manager",
		},
	}
}
