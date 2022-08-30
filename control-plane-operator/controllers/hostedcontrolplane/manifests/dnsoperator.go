package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DNSOperatorKubeconfig returns a stub secret, with name and namespace, for the
// DNS operator's kubeconfig.
func DNSOperatorKubeconfig(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dns-operator-kubeconfig",
			Namespace: ns,
		},
	}
}

// DNSOperatorDeployment returns a stub deployment, with name and namespace, for
// the DNS operator.
func DNSOperatorDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dns-operator",
			Namespace: ns,
		},
	}
}
