package manifests

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

func ManagedAzureSecretProviderClass(name, namespace string) *secretsstorev1.SecretProviderClass {
	return &secretsstorev1.SecretProviderClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}
