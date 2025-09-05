package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Azure credential secrets for control plane operators

// AzureCloudNetworkConfigCredentials creates a secret for the cloud network config controller
func AzureCloudNetworkConfigCredentials(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "cloud-network-config-controller-creds",
		},
	}
}

// AzureCloudProviderCredentials creates a secret for the cloud controller manager
func AzureCloudProviderCredentials(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "azure-cloud-config",
		},
	}
}

// AzureIngressCredentials creates a secret for the ingress operator
func AzureIngressCredentials(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "azure-ingress-credentials",
		},
	}
}

// AzureImageRegistryCredentials creates a secret for the image registry operator
func AzureImageRegistryCredentials(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "azure-image-registry-credentials",
		},
	}
}

// AzureDiskCSICredentials creates a secret for the Azure disk CSI driver
func AzureDiskCSICredentials(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "azure-disk-csi-config",
		},
	}
}

// AzureFileCSICredentials creates a secret for the Azure file CSI driver
func AzureFileCSICredentials(controlPlaneNamespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "azure-file-csi-config",
		},
	}
}
