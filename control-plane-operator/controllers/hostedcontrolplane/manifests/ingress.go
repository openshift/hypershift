package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
)

func IngressDefaultIngressControllerWorkerManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-default-ingress-controller",
			Namespace: ns,
		},
	}
}

func IngressDefaultIngressController() *operatorv1.IngressController {
	return &operatorv1.IngressController{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: "openshift-ingress-operator",
		},
	}
}

func IngressPrivateIngressController(name string) *operatorv1.IngressController {
	return &operatorv1.IngressController{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "openshift-ingress-operator",
		},
	}
}

func DNSConfig() *configv1.DNS {
	return &configv1.DNS{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func IngressDefaultIngressControllerCertWorkerManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-default-ingress-controller-cert",
			Namespace: ns,
		},
	}
}

func IngressDefaultIngressControllerCert() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-ingress-cert",
			Namespace: "openshift-ingress",
		},
	}
}
