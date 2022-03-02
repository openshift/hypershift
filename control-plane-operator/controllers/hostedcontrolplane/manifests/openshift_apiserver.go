package manifests

import (
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	// TODO: Switch to k8s.io/api/policy/v1 when all management clusters at 1.21+ OR 4.8_openshift+
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func OpenShiftAPIServerConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-apiserver",
			Namespace: ns,
		},
	}
}

func OpenShiftAPIServerAuditConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-apiserver-audit",
			Namespace: ns,
		},
	}
}

func OpenShiftAPIServerPodDisruptionBudget(ns string) *policyv1beta1.PodDisruptionBudget {
	return &policyv1beta1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-apiserver",
			Namespace: ns,
		},
	}
}

func OpenShiftAPIServerDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-apiserver",
			Namespace: ns,
		},
	}
}

func OpenShiftAPIServerServiceMonitor(ns string) *prometheusoperatorv1.ServiceMonitor {
	return &prometheusoperatorv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-apiserver",
			Namespace: ns,
		},
	}
}
