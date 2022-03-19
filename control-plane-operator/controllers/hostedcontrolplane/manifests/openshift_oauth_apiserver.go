package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	// TODO: Switch to k8s.io/api/policy/v1 when all management clusters at 1.21+ OR 4.8_openshift+
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func OpenShiftOAuthAPIServerAuditConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-oauth-apiserver-audit",
			Namespace: ns,
		},
	}
}

func OpenShiftOAuthAPIServerDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-oauth-apiserver",
			Namespace: ns,
		},
	}
}

func OpenShiftOAuthAPIServerDisruptionBudget(ns string) *policyv1beta1.PodDisruptionBudget {
	return &policyv1beta1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-oauth-apiserver",
			Namespace: ns,
		},
	}
}

func OpenShiftOAuthAPIServerServiceServingCA(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "openshift-oauth-apiserver-serving-ca",
			Namespace: ns,
		},
	}
}
