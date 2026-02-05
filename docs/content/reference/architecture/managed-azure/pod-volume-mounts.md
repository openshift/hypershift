# Pod Volume Mount Implementation Details

This document provides implementation details for how control plane pods mount Azure credentials. For a conceptual overview, see [Secrets CSI Usage](./secrets-csi.md).

## Helper Functions

All helper functions are in `support/azureutil/azureutil.go`:

### CreateVolumeForAzureSecretStoreProviderClass

Creates a CSI volume referencing a SecretProviderClass:

```go
func CreateVolumeForAzureSecretStoreProviderClass(secretStoreVolumeName, secretProviderClassName string) corev1.Volume {
    return corev1.Volume{
        Name: secretStoreVolumeName,
        VolumeSource: corev1.VolumeSource{
            CSI: &corev1.CSIVolumeSource{
                Driver:   "secrets-store.csi.k8s.io",
                ReadOnly: ptr.To(true),
                VolumeAttributes: map[string]string{
                    "secretProviderClass": secretProviderClassName,
                },
            },
        },
    }
}
```

### CreateVolumeMountForAzureSecretStoreProviderClass

Creates a volume mount at `/mnt/certs`:

```go
func CreateVolumeMountForAzureSecretStoreProviderClass(secretStoreVolumeName string) corev1.VolumeMount {
    return corev1.VolumeMount{
        Name:      secretStoreVolumeName,
        MountPath: "/mnt/certs",
        ReadOnly:  true,
    }
}
```

### CreateVolumeMountForKMSAzureSecretStoreProviderClass

Creates a volume mount at `/mnt/kms` (for KMS credentials):

```go
func CreateVolumeMountForKMSAzureSecretStoreProviderClass(secretStoreVolumeName string) corev1.VolumeMount {
    return corev1.VolumeMount{
        Name:      secretStoreVolumeName,
        MountPath: "/mnt/kms",
        ReadOnly:  true,
    }
}
```

### CreateEnvVarsForAzureManagedIdentity

Creates environment variable pointing to the credential file:

```go
func CreateEnvVarsForAzureManagedIdentity(azureCredentialsName string) []corev1.EnvVar {
    return []corev1.EnvVar{
        {
            Name:  "MANAGED_AZURE_HCP_CREDENTIALS_FILE_PATH",
            Value: "/mnt/certs/" + azureCredentialsName,
        },
    }
}
```

### IsAroHCP

Checks if running in a managed Azure HostedClusters environment by checking the MANAGED_SERVICE environment variable:

```go
func IsAroHCP() bool {
    return os.Getenv("MANAGED_SERVICE") == hyperv1.AroHCP
}
```

## Constants

Defined in `support/config/constants.go`:

| Constant | Value | Description |
|----------|-------|-------------|
| `ManagedAzureCredentialsFilePath` | `MANAGED_AZURE_HCP_CREDENTIALS_FILE_PATH` | Environment variable name |
| `ManagedAzureCertificateMountPath` | `/mnt/certs` | Base mount path |
| `ManagedAzureCertificatePath` | `/mnt/certs/` | Path prefix for building file paths |
| `ManagedAzureCredentialsMountPathForKMS` | `/mnt/kms` | KMS-specific mount path |
| `ManagedAzureCredentialsPathForKMS` | `/mnt/kms/` | KMS path prefix |
| `ManagedAzureSecretsStoreCSIDriver` | `secrets-store.csi.k8s.io` | CSI driver name |
| `ManagedAzureSecretProviderClass` | `secretProviderClass` | Volume attribute key |

## Component Volume Names

| Component | Volume Name | SecretProviderClass |
|-----------|-------------|---------------------|
| Control Plane Operator | `cpo-cert` | `managed-azure-cpo` |
| Cloud Provider (CCM) | `cloud-provider-cert` | `managed-azure-cloud-provider` |
| Ingress Operator | `ingress-cert` | `managed-azure-ingress` |
| Image Registry | `image-registry-cert` | `managed-azure-image-registry` |
| KMS | `kms-cert` | `managed-azure-kms` |
| NodePool Management (CAPZ) | `nodepool-management-cert` | `managed-azure-nodepool-management` |

## Credential File Usage

Components read the mounted credential file using the `msi-dataplane` library:

```go
import "github.com/Azure/msi-dataplane/pkg/dataplane"

// Build path from constant + secret name
certPath := config.ManagedAzureCertificatePath + credentialsSecretName

// Create credential with automatic file watching and reload
creds, err := dataplane.NewUserAssignedIdentityCredential(
    ctx,
    certPath,
    dataplane.WithClientOpts(azcore.ClientOptions{Cloud: cloud.AzurePublic}),
)
```

The credential automatically reloads when the file is updated by the CSI driver.

## Configuration File Usage

Some components (CCM, Azure Disk/File CSI) use a cloud config file that references the credential path:

```json
{
  "cloud": "AzurePublicCloud",
  "tenantId": "<tenant-id>",
  "subscriptionId": "<subscription-id>",
  "resourceGroup": "<resource-group>",
  "location": "<location>",
  "useManagedIdentityExtension": true,
  "aadMSIDataPlaneIdentityPath": "/mnt/certs/<credential-secret-name>"
}
```

The `aadMSIDataPlaneIdentityPath` field tells the component where to find the mounted credential file.
