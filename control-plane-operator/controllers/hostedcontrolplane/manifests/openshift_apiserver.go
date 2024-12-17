package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
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

func OpenShiftAPIServerPodDisruptionBudget(ns string) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
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
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: ptr.To[int64](120),
				},
			},
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
