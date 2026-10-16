package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func PKIOperatorServiceAccount(controlPlaneOperatorNamespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneOperatorNamespace,
			Name:      "control-plane-pki-operator",
		},
	}
}
