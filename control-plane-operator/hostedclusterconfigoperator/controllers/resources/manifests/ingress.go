package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func IngressDefaultIngressControllerCert() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-ingress-cert",
			Namespace: "openshift-ingress",
		},
	}
}

func IngressDefaultIngressNodePortService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "router-nodeport-default",
			Namespace: "openshift-ingress",
		},
	}
}
