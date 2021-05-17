package manifests

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func KASLocalhostKubeconfigSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "localhost-kubeconfig",
			Namespace: controlPlaneNamespace,
		},
	}
}

func KASServiceKubeconfigSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service-network-admin-kubeconfig",
			Namespace: controlPlaneNamespace,
		},
	}
}

func KASExternalKubeconfigSecret(controlPlaneNamespace string, ref *hyperv1.KubeconfigSecretRef) *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "admin-kubeconfig",
			Namespace: controlPlaneNamespace,
		},
	}
	if ref != nil {
		s.Name = ref.Name
	}
	return s
}

func KASBootstrapKubeconfigSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bootstrap-kubeconfig",
			Namespace: controlPlaneNamespace,
		},
	}
}

func KASDeployment(controlPlaneNamespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: controlPlaneNamespace,
		},
	}
}

func KASAuditConfig(controlPlaneNamespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kas-audit-config",
			Namespace: controlPlaneNamespace,
		},
	}
}

func KASConfig(controlPlaneNamespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kas-config",
			Namespace: controlPlaneNamespace,
		},
	}
}

func KASService(controlPlaneNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: controlPlaneNamespace,
		},
	}
}

func KASOAuthMetadata(controlPlaneNamespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oauth-metadata",
			Namespace: controlPlaneNamespace,
		},
	}
}
