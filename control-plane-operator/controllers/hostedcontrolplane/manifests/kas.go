package manifests

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// KubeconfigScopeLabel is used to indicate the usage scope of the kubeconfig
	KubeconfigScopeLabel = "hypershift.openshift.io/kubeconfig"
)

const (
	// KubeconfigScopeLocal means the kubeconfig is for use by cluster-local
	// clients (e.g. the service network)
	KubeconfigScopeLocal KubeconfigScope = "local"
)

type KubeconfigScope string

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

// The client used by CAPI machine controller expects the kubeconfig to follow this naming convention
// https://github.com/kubernetes-sigs/cluster-api/blob/5c85a0a01ee44ecf7c8a3c3fdc867a88af87d73c/util/secret/secret.go#L29-L33
func KASServiceCAPIKubeconfigSecret(controlPlaneNamespace, infraID string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-kubeconfig", infraID),
			Namespace: controlPlaneNamespace,
		},
	}
}

func HCCOKubeconfigSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcco-kubeconfig",
			Namespace: controlPlaneNamespace,
		},
	}
}

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

func KASAdminKubeconfigSecret(controlPlaneNamespace string, ref *hyperv1.KubeconfigSecretRef) *corev1.Secret {
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

func KASContainerAWSKMSProviderServiceAccount() *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kms-provider",
			Namespace: "kube-system",
		},
	}
}

func KASAuditConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kas-audit-config",
			Namespace: ns,
		},
	}
}
