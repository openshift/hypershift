# Azure Authentication Flow

This document explains the authentication flow from control plane pods to Azure APIs for managed Azure HostedClusters.

For CSI-based credential mounting details, see [Secrets CSI Usage](./secrets-csi.md).

## Authentication Overview

Managed Azure HostedClusters use certificate-based authentication with credentials stored in Azure Key Vault:

| Aspect | Description |
|--------|-------------|
| **Credential Type** | X.509 Certificate |
| **Credential Source** | Azure Key Vault |
| **Credential Delivery** | Secrets Store CSI Driver |
| **Azure SDK Library** | `msi-dataplane` |
| **Credential Rotation** | Automatic via Key Vault |

## Authentication Flow

```
Pod Startup → CSI Driver → Azure Key Vault → Credential File
                                                    ↓
Azure ARM API ← Access Token ← Azure AD ← Azure SDK reads file
```

1. **CSI Driver** mounts credential from Azure Key Vault (see [Secrets CSI Usage](./secrets-csi.md))
2. **Credential File** contains `UserAssignedIdentityCredentials` JSON with PEM certificate
3. **Azure SDK** uses `msi-dataplane.NewUserAssignedIdentityCredential()` to load credentials
4. **Azure AD** validates the client certificate and issues an access token
5. **ARM API** accepts the Bearer token for resource operations

## Credential Loading Code

```go
import "github.com/Azure/msi-dataplane/pkg/dataplane"

certPath := "/mnt/certs/" + credentialsSecretName
creds, err := dataplane.NewUserAssignedIdentityCredential(
    ctx,
    certPath,
    dataplane.WithClientOpts(azcore.ClientOptions{Cloud: cloud.AzurePublic}),
)

// Use with Azure SDK clients
client, err := armresources.NewResourceGroupsClient(subscriptionID, creds, nil)
```

The credential automatically watches the file for changes and reloads when the CSI driver refreshes secrets.

## Cloud Configuration File

```json
{
  "cloud": "AzurePublicCloud",
  "tenantId": "<tenant-id>",
  "subscriptionId": "<subscription-id>",
  "resourceGroup": "<resource-group>",
  "useManagedIdentityExtension": true,
  "aadMSIDataPlaneIdentityPath": "/mnt/certs/<credential-secret-name>"
}
```

## Credential Refresh

The `msi-dataplane` library handles credential refresh:

1. File watcher monitors credential file for changes
2. When CSI driver refreshes secrets, new credentials are loaded
3. Backstop timer reloads every 6 hours
4. Compares `notBefore` timestamps to detect newer credentials

## Troubleshooting

### Verify CSI Mount

```bash
kubectl exec -it <pod> -- ls -la /mnt/certs
kubectl exec -it <pod> -- cat /mnt/certs/<secret-name>
```

### Check SecretProviderClass

```bash
kubectl get secretproviderclass -n <namespace>
kubectl describe secretproviderclasspodstatus -n <namespace>
```

## Related Documentation

- [Secrets CSI Usage](./secrets-csi.md) - CSI driver overview
