# SecretProviderClass Implementation Details

This document provides implementation details for how HyperShift creates and manages SecretProviderClass resources. For a conceptual overview, see [Secrets CSI Usage](./secrets-csi.md).

## ReconcileManagedAzureSecretProviderClass Function

The core function that populates SecretProviderClass resources is located in `support/secretproviderclass/secretproviderclass.go`:

```go
func ReconcileManagedAzureSecretProviderClass(
    secretProviderClass *secretsstorev1.SecretProviderClass,
    hcp *hyperv1.HostedControlPlane,
    managedIdentity hyperv1.ManagedIdentity,
) {
    secretProviderClass.Spec = secretsstorev1.SecretProviderClassSpec{
        Provider: "azure",
        Parameters: map[string]string{
            "usePodIdentity":         "false",
            "useVMManagedIdentity":   "true",
            "userAssignedIdentityID": azureutil.GetKeyVaultAuthorizedUser(),
            "keyvaultName":           hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.ManagedIdentitiesKeyVault.Name,
            "tenantId":               hcp.Spec.Platform.Azure.AzureAuthenticationConfig.ManagedIdentities.ControlPlane.ManagedIdentitiesKeyVault.TenantID,
            "objects":                formatSecretProviderClassObject(managedIdentity.CredentialsSecretName, string(managedIdentity.ObjectEncoding)),
        },
    }
}
```

### Parameter Sources

| Parameter | Source | Description |
|-----------|--------|-------------|
| `userAssignedIdentityID` | `azureutil.GetKeyVaultAuthorizedUser()` | Reads from `ARO_HCP_KEY_VAULT_USER_CLIENT_ID` env var, set via `--aro-hcp-key-vault-users-client-id` flag during HO installation |
| `keyvaultName` | `hcp.Spec.Platform.Azure...ManagedIdentitiesKeyVault.Name` | From HostedControlPlane spec |
| `tenantId` | `hcp.Spec.Platform.Azure...ManagedIdentitiesKeyVault.TenantID` | From HostedControlPlane spec |
| `objects` | `formatSecretProviderClassObject(...)` | Generated from `ManagedIdentity.CredentialsSecretName` and `ObjectEncoding` |

## SecretProviderClass Names and Constants

### Created by HyperShift Operator

| Constant | Value | ManagedIdentity Field |
|----------|-------|----------------------|
| `ManagedAzureCPOSecretProviderClassName` | `managed-azure-cpo` | `ControlPlane.ControlPlaneOperator` |
| `ManagedAzureNodePoolMgmtSecretProviderClassName` | `managed-azure-nodepool-management` | `ControlPlane.NodePoolManagement` |
| `ManagedAzureKMSSecretProviderClassName` | `managed-azure-kms` | `SecretEncryption.KMS.Azure.KMS` |

### Created by Control Plane Operator

| Constant | Value | ManagedIdentity Field |
|----------|-------|----------------------|
| `ManagedAzureCloudProviderSecretProviderClassName` | `managed-azure-cloud-provider` | `ControlPlane.CloudProvider` |
| `ManagedAzureIngressSecretStoreProviderClassName` | `managed-azure-ingress` | `ControlPlane.Ingress` |
| `ManagedAzureNetworkSecretStoreProviderClassName` | `managed-azure-network` | `ControlPlane.Network` |
| `ManagedAzureImageRegistrySecretStoreProviderClassName` | `managed-azure-image-registry` | `ControlPlane.ImageRegistry` |
| `ManagedAzureDiskCSISecretStoreProviderClassName` | `managed-azure-disk-csi` | `ControlPlane.Disk` |
| `ManagedAzureFileCSISecretStoreProviderClassName` | `managed-azure-file-csi` | `ControlPlane.File` |

## Source Files

| File | Purpose |
|------|---------|
| `support/secretproviderclass/secretproviderclass.go` | `ReconcileManagedAzureSecretProviderClass` function |
| `support/config/constants.go` | SecretProviderClass name constants |
| `support/azureutil/azureutil.go` | `GetKeyVaultAuthorizedUser()` and volume helpers |
| `control-plane-operator/controllers/hostedcontrolplane/manifests/secretproviderclass.go` | Manifest factory |

## Component Adapter Files

Each component has an adapter that calls `ReconcileManagedAzureSecretProviderClass`:

| Component | Adapter File |
|-----------|--------------|
| Cloud Controller Manager | `control-plane-operator/controllers/hostedcontrolplane/v2/cloud_controller_manager/azure/config.go` |
| Cluster Network Operator | `control-plane-operator/controllers/hostedcontrolplane/v2/cno/azure.go` |
| Ingress Operator | `control-plane-operator/controllers/hostedcontrolplane/v2/ingressoperator/azure.go` |
| Image Registry Operator | `control-plane-operator/controllers/hostedcontrolplane/v2/registryoperator/azure.go` |
| Storage Operator (Disk/File) | `control-plane-operator/controllers/hostedcontrolplane/v2/storage/azure.go` |
| KMS Provider | `control-plane-operator/controllers/hostedcontrolplane/v2/kas/kms/azure.go` |
