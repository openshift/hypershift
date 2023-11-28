package manifests

import (
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Cloud Credential Operator

func CloudCredentialOperatorKubeconfig(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cloud-credential-operator-kubeconfig",
			Namespace: ns,
		},
	}
}

func CloudCredentialOperatorDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cloud-credential-operator",
			Namespace: ns,
		},
	}
}

func CloudCredentialOperatorPodMonitor(ns string) *prometheusoperatorv1.PodMonitor {
	return &prometheusoperatorv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cloud-credential-operator",
			Namespace: ns,
		},
	}
}

func CloudCredentialOperatorServingCertSecret(ns string) *corev1.Secret {
	return secretFor(ns, "cloud-credential-operator-serving-cert")
}
