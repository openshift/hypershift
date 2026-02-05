# Azure Key Vault Secret Structure

This document describes the structure and format of secrets stored in Azure Key Vault for each managed identity used by managed Azure HostedClusters control plane components.

## Overview

Managed Azure HostedClusters use Azure Key Vault to store credentials for managed identities that authenticate control plane components with Azure APIs. These secrets are mounted into pods via the Secrets Store CSI driver and consumed by components to obtain Azure access tokens.

## Key Vault Reference Configuration

The Key Vault containing managed identity credentials is referenced in the HostedCluster specification under `managedIdentitiesKeyVault`:

```yaml
spec:
  platform:
    azure:
      azureAuthenticationConfig:
        azureAuthenticationConfigType: ManagedIdentities
        managedIdentities:
          controlPlane:
            managedIdentitiesKeyVault:
              name: "<key-vault-name>"
              tenantID: "<tenant-id>"
```

| Field | Description |
|-------|-------------|
| `name` | The name of the Azure Key Vault on the management cluster |
| `tenantID` | The Azure AD tenant ID where the Key Vault exists |

## Secret Naming Convention

Each managed identity has a corresponding secret in Azure Key Vault. The secret name is specified in the `credentialsSecretName` field of each managed identity configuration.

### Naming Pattern

Secret names follow a pattern based on the component name and typically end with `-json` suffix when stored by the setup scripts:

| Component | Typical Secret Name Pattern | Example |
|-----------|---------------------------|---------|
| Cloud Provider | `cloud-provider-<prefix>-json` | `cloud-provider-mycluster-json` |
| Control Plane Operator | `cpo-<prefix>-json` | `cpo-mycluster-json` |
| Node Pool Management | `nodepool-mgmt-<prefix>-json` | `nodepool-mgmt-mycluster-json` |
| Azure Disk CSI | `azure-disk-<prefix>-json` | `azure-disk-mycluster-json` |
| Azure File CSI | `azure-file-<prefix>-json` | `azure-file-mycluster-json` |
| Image Registry | `ciro-<prefix>-json` | `ciro-mycluster-json` |
| Ingress | `ingress-<prefix>-json` | `ingress-mycluster-json` |
| Network (CNCC) | `cncc-<prefix>-json` | `cncc-mycluster-json` |

### Naming Requirements

Secret names must adhere to Azure Key Vault naming requirements:

- Between 1 and 127 characters
- Only alphanumeric characters and hyphens (`[a-zA-Z0-9-]`)
- Must be unique within the Key Vault

## Secret Format and Encoding

### Object Type and Encoding

The secret is stored as an Azure Key Vault **secret** (not a certificate or key). When retrieved via the Secrets Store CSI driver, the following parameters are used:

```yaml
objectType: secret
objectEncoding: utf-8
```

The `objectEncoding` field specifies how the secret content is encoded:

| Encoding | Description |
|----------|-------------|
| `utf-8` | UTF-8 encoded text (default and most common) |
| `hex` | Hexadecimal encoded |
| `base64` | Base64 encoded |

!!! warning "Encoding Mismatch"

    The `objectEncoding` must match the actual encoding format used when the secret was stored in Azure Key Vault. If they don't match, the Secrets Store CSI driver will fail to correctly read the secret, and an error will be visible on the SecretProviderClass custom resource.

## Secret Content Structure

The secret content is a JSON object following the `UserAssignedIdentityCredentials` structure from the [Azure MSI Dataplane library](https://github.com/Azure/msi-dataplane). This JSON is stored as UTF-8 encoded text.

### JSON Schema

```json
{
  "authentication_endpoint": "https://login.microsoftonline.com/",
  "client_id": "<managed-identity-client-id>",
  "client_secret": "<base64-encoded-pem-certificate>",
  "tenant_id": "<azure-ad-tenant-id>",
  "not_before": "<timestamp-rfc3339>",
  "not_after": "<timestamp-rfc3339>",
  "renew_after": "<timestamp-rfc3339>",
  "cannot_renew_after": "<timestamp-rfc3339>"
}
```

### Field Descriptions

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `authentication_endpoint` | string | Yes | The Azure AD authentication endpoint URL (e.g., `https://login.microsoftonline.com/`) |
| `client_id` | string | Yes | The client ID (UUID) of the managed identity |
| `client_secret` | string | Yes | Base64-encoded X.509 certificate with private key in PEM format |
| `tenant_id` | string | Yes | The Azure AD tenant ID (UUID) |
| `not_before` | string | Yes | RFC3339 timestamp when the credential becomes valid |
| `not_after` | string | Yes | RFC3339 timestamp when the credential expires |
| `renew_after` | string | Optional | RFC3339 timestamp after which the credential should be renewed |
| `cannot_renew_after` | string | Optional | RFC3339 timestamp after which the credential cannot be renewed |

### Example Secret Content

```json
{
  "authentication_endpoint": "https://login.microsoftonline.com/",
  "client_id": "12345678-1234-1234-1234-123456789abc",
  "client_secret": "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURxRENDQX...",
  "tenant_id": "87654321-4321-4321-4321-abcdef123456",
  "not_before": "2024-01-15T10:00:00Z",
  "not_after": "2025-01-15T10:00:00Z",
  "renew_after": "2024-07-15T10:00:00Z",
  "cannot_renew_after": "2024-12-15T10:00:00Z"
}
```

## Certificate Content (client_secret Field)

The `client_secret` field contains a **base64-encoded PEM certificate bundle** that includes both the X.509 certificate and private key.

### PEM Structure

When decoded from base64, the `client_secret` contains:

```pem
-----BEGIN CERTIFICATE-----
MIIDqDCCApCgAwIBAgIQExample...
(certificate data in base64)
-----END CERTIFICATE-----
-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASC...
(private key data in base64)
-----END PRIVATE KEY-----
```

The PEM bundle may include:

1. **X.509 Certificate**: The public certificate used for authentication
2. **Private Key**: The RSA or ECDSA private key (PKCS#8 format)
3. **Certificate Chain** (optional): Intermediate certificates if applicable

### How the Certificate is Used

The msi-dataplane library processes this certificate as follows:

1. Base64 decodes the `client_secret` value
2. Parses the PEM-encoded certificate and private key using `azidentity.ParseCertificates()`
3. Creates a `ClientCertificateCredential` for authenticating with Azure AD
4. The credential sends the certificate chain in the x5c header (required for MSI authentication)

!!! note "Certificate Format"

    The Azure SDK's `ParseCertificates` function expects PEM format. PKCS#12 format is not currently supported by the msi-dataplane library.

## Managed Identity Configuration in HostedCluster

Each managed identity in the HostedCluster specification includes the following fields:

```yaml
cloudProvider:
  clientID: "<uuid>"
  credentialsSecretName: "<secret-name>"
  objectEncoding: "utf-8"
```

| Field | Description |
|-------|-------------|
| `clientID` | The Azure AD client ID (UUID) of the managed identity |
| `credentialsSecretName` | Name of the secret in Azure Key Vault containing the credentials |
| `objectEncoding` | Encoding format of the secret content (utf-8, hex, or base64) |

## Key Vault Secret Attributes

When storing secrets in Key Vault, the following attributes are set:

| Attribute | Description |
|-----------|-------------|
| `Enabled` | Set to `true` to allow retrieval |
| `Expires` | Set to the `not_after` timestamp from the credential |
| `NotBefore` | Set to the `not_before` timestamp from the credential |

Additionally, the following tags are added:

| Tag | Description |
|-----|-------------|
| `renew_after` | RFC3339 timestamp for credential renewal |
| `cannot_renew_after` | RFC3339 timestamp after which renewal is not possible |

## Related Documentation

- [Secrets CSI Usage](./secrets-csi.md) - How the Secrets Store CSI driver mounts secrets
- [Azure MSI Dataplane Library](https://github.com/Azure/msi-dataplane) - Library used for credential handling
