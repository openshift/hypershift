package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func IngressCloudCredsSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-ingress-operator",
			Name:      "cloud-credentials",
		},
	}
}

func ImageRegistryCloudCredsSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-image-registry",
			Name:      "installer-cloud-credentials",
		},
	}
}

func EBSStorageCloudCredsSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-cluster-csi-drivers",
			Name:      "ebs-cloud-credentials",
		},
	}
}

func ClusterNetworkingCloudCredsSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-cloud-network-config-controller",
			Name:      "cloud-credentials",
		},
	}
}

func AzureDiskCloudCredsSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-cluster-csi-drivers",
			Name:      "azure-disk-credentials",
		},
	}
}

func AzureFileCloudCredsSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-cluster-csi-drivers",
			Name:      "azure-file-credentials",
		},
	}
}
