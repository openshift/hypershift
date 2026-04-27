package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// ServiceCAKey is the data key in the service-serving-ca ConfigMap
	// that contains the service CA certificate.
	ServiceCAKey = "service-ca.crt"
)

func ServiceServingCA(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service-serving-ca",
			Namespace: ns,
		},
	}
}
