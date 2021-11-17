package manifests

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	//TODO: Switch to k8s.io/api/policy/v1 when all management clusters at 1.21+ OR 4.8_openshift+
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

const (
	// KubeconfigScopeLabel is used to indicate the usage scope of the kubeconfig
	KubeconfigScopeLabel = "hypershift.openshift.io/kubeconfig"
)

const (
	// KubeconfigScopeExternal means the kubeconfig is for use by cluster-external
	// clients
	KubeconfigScopeExternal KubeconfigScope = "external"

	// KubeconfigScopeLocal means the kubeconfig is for use by cluster-local
	// clients (e.g. the service network)
	KubeconfigScopeLocal KubeconfigScope = "local"

	// KubeconfigScopeBootstrap means the kubeconfig is passed via ignition to
	// worker nodes so they can bootstrap
	KubeconfigScopeBootstrap KubeconfigScope = "bootstrap"
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

func KASPodDisruptionBudget(ns string) *policyv1beta1.PodDisruptionBudget {
	return &policyv1beta1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: ns,
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

func KASEgressSelectorConfig(controlPlaneNamespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kas-egress-selector-config",
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

func KASAuthenticationTokenWebhookConfigSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kas-authentication-token-webhook-config",
			Namespace: controlPlaneNamespace,
		},
	}
}
