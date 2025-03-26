package manifests

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
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

func HCCOKubeconfigSecret(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hcco-kubeconfig",
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

func KASPodDisruptionBudget(ns string) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
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

func AuthConfig(controlPlaneNamespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "auth-config",
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

func KASServiceMonitor(ns string) *prometheusoperatorv1.ServiceMonitor {
	return &prometheusoperatorv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: ns,
		},
	}
}

func ControlPlaneRecordingRules(ns string) *prometheusoperatorv1.PrometheusRule {
	return &prometheusoperatorv1.PrometheusRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "recording-rules",
			Namespace: ns,
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
