package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func secretFor(ns, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	}
}

func CustomerSystemAdminSigner(ns string) *corev1.Secret {
	return secretFor(ns, "customer-system-admin-signer")
}

func CustomerSystemAdminSignerCA(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "customer-system-admin-signer-ca",
			Namespace: ns,
		},
	}
}

func CustomerSystemAdminClientCertSecret(ns string) *corev1.Secret {
	return secretFor(ns, "customer-system-admin-client-cert-key")
}

func SRESystemAdminSigner(ns string) *corev1.Secret {
	return secretFor(ns, "sre-system-admin-signer")
}

func SRESystemAdminSignerCA(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sre-system-admin-signer-ca",
			Namespace: ns,
		},
	}
}

func SRESystemAdminClientCertSecret(ns string) *corev1.Secret {
	return secretFor(ns, "sre-system-admin-client-cert-key")
}

func TotalKASClientCABundle(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "total-client-ca",
			Namespace: ns,
		},
	}
}
