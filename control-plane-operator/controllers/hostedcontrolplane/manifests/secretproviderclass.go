package manifests

import (
	"fmt"
	"github.com/openshift/hypershift/support/azureutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

const (
	objectFormat = `
array:
  - |
    objectName: %s
    objectType: secret
`
)

// ManagedAzureKeyVaultSecretProviderClass returns an instance of a SecretProviderClass completed with its name, Azure Key Vault set
// up, and the certificate name it needs to pull from the Key Vault.
//
// https://learn.microsoft.com/en-us/azure/aks/csi-secrets-store-identity-access?tabs=azure-portal&pivots=access-with-a-user-assigned-managed-identity
func ManagedAzureKeyVaultSecretProviderClass(secretProviderClassName, namespace, keyVaultName, KeyVaultTenantID, certificateName string) *secretsstorev1.SecretProviderClass {
	return &secretsstorev1.SecretProviderClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretProviderClassName,
			Namespace: namespace,
		},
		Spec: secretsstorev1.SecretProviderClassSpec{
			Provider: "azure",
			Parameters: map[string]string{
				"usePodIdentity":         "false",
				"useVMManagedIdentity":   "true",
				"userAssignedIdentityID": azureutil.GetKeyVaultAuthorizedUser(),
				"keyvaultName":           keyVaultName,
				"tenantId":               KeyVaultTenantID,
				"objects":                formatSecretProviderClassObject(certificateName),
			},
		},
	}
}

// formatSecretProviderClassObject places the certificate name in the appropriate string structure the
// SecretProviderClass expects for an object. More details here:
// - https://learn.microsoft.com/en-us/azure/aks/csi-secrets-store-identity-access?tabs=azure-portal&pivots=access-with-a-user-assigned-managed-identity#configure-managed-identity
// - https://secrets-store-csi-driver.sigs.k8s.io/concepts.html?highlight=object#custom-resource-definitions-crds
func formatSecretProviderClassObject(certName string) string {
	return fmt.Sprintf(objectFormat, certName)
}
