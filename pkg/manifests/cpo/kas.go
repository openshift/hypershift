package cpo

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	KubeAPIServerServiceName = "kube-apiserver"
)

func KASCustomKubeconfigSecret(controlPlaneNamespace string, ref *hyperv1.KubeconfigSecretRef) *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "custom-admin-kubeconfig",
			Namespace: controlPlaneNamespace,
		},
	}
	if ref != nil {
		s.Name = ref.Name
	}
	return s
}

func KubeAPIServerService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KubeAPIServerServiceName,
			Namespace: hostedClusterNamespace,
		},
	}
}

func KubeAPIServerServiceAzureLB(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KubeAPIServerServiceName + "lb",
			Namespace: hostedClusterNamespace,
		},
	}
}
