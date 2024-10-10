package manifests

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

func IngressOperatorKubeconfig(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress-operator-kubeconfig",
			Namespace: ns,
		},
	}
}

func IngressOperatorDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress-operator",
			Namespace: ns,
		},
	}
}

func IngressOperatorPodMonitor(ns string) *prometheusoperatorv1.PodMonitor {
	return &prometheusoperatorv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ingress-operator",
			Namespace: ns,
		},
	}
}

// https://learn.microsoft.com/en-us/azure/aks/csi-secrets-store-identity-access?tabs=azure-portal&pivots=access-with-a-user-assigned-managed-identity
func IngressSecretProviderClass(hcp *hyperv1.HostedControlPlane) *secretsstorev1.SecretProviderClass {
	return &secretsstorev1.SecretProviderClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aro-hcp-ingress",
			Namespace: hcp.Namespace,
		},
		Spec: secretsstorev1.SecretProviderClassSpec{
			Provider: "azure",
			Parameters: map[string]string{
				"usePodIdentity":         "false",
				"useVMManagedIdentity":   "true", // Set to true for using managed identity
				"userAssignedIdentityID": hcp.Spec.Platform.Azure.ManagementKeyVault.AuthorizedClientID,
				"keyvaultName":           hcp.Spec.Platform.Azure.ManagementKeyVault.Name,
				"tenantId":               hcp.Spec.Platform.Azure.ManagementKeyVault.TenantID,
				"objects":                getObject(hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlane.Ingress.CertificateName),
			},
		},
	}
}

func getObject(certName string) string {
	return `
array:
  - |
    objectName: ` + certName + `
    objectType: secret
`
}
