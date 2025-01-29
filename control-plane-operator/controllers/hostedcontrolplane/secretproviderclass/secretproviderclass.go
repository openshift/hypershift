package secretproviderclass

import (
	"fmt"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"

	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

const (
	objectFormat = `
array:
  - |
    objectName: %s
    objectEncoding: %s
    objectType: secret
`
)

// ReconcileManagedAzureSecretProviderClass reconciles the Spec of a SecretProviderClass completed with its name, Azure
// Key Vault setup, and the certificate name it needs to pull from the Key Vault.
//
// https://learn.microsoft.com/en-us/azure/aks/csi-secrets-store-identity-access?tabs=azure-portal&pivots=access-with-a-user-assigned-managed-identity
func ReconcileManagedAzureSecretProviderClass(secretProviderClass *secretsstorev1.SecretProviderClass, hcp *hyperv1.HostedControlPlane, certName, objectEncoding string) {
	secretProviderClass.Spec = secretsstorev1.SecretProviderClassSpec{
		Provider: "azure",
		Parameters: map[string]string{
			"usePodIdentity":         "false",
			"useVMManagedIdentity":   "true",
			"userAssignedIdentityID": azureutil.GetKeyVaultAuthorizedUser(),
			"keyvaultName":           hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlane.ManagedIdentitiesKeyVault.Name,
			"tenantId":               hcp.Spec.Platform.Azure.ManagedIdentities.ControlPlane.ManagedIdentitiesKeyVault.TenantID,
			"objects":                formatSecretProviderClassObject(certName, objectEncoding),
		},
	}
}

// formatSecretProviderClassObject places the certificate name in the appropriate string structure the
// SecretProviderClass expects for an object and specified the objectEncoding. More details here:
// - https://learn.microsoft.com/en-us/azure/aks/csi-secrets-store-identity-access?tabs=azure-portal&pivots=access-with-a-user-assigned-managed-identity#configure-managed-identity
// - https://secrets-store-csi-driver.sigs.k8s.io/concepts.html?highlight=object#custom-resource-definitions-crds
func formatSecretProviderClassObject(certName, objectEncoding string) string {
	return fmt.Sprintf(objectFormat, certName, objectEncoding)
}
