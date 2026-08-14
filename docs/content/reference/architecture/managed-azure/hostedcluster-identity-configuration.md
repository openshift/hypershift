# HostedCluster CR Identity Configuration

This document provides a comprehensive reference for configuring Azure identity authentication in the HostedCluster custom resource for managed Azure HostedClusters.

## Overview

The `azureAuthenticationConfig` field in `spec.platform.azure` is a discriminated union that determines how the hosted cluster authenticates with Azure APIs. The authentication type is selected via the `azureAuthenticationConfigType` discriminator field.

## AzureAuthenticationConfiguration Union

The `AzureAuthenticationConfiguration` type controls which authentication mechanism is used for Azure API access.

### Type Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `azureAuthenticationConfigType` | `string` | Yes | Discriminator that specifies the authentication type. Set to `ManagedIdentities` for managed Azure HostedClusters. |
| `managedIdentities` | `AzureResourceManagedIdentities` | Yes | Contains managed identity configuration for managed Azure HostedClusters. |

### Authentication Model

Managed Azure HostedClusters use:

- **Control Plane**: Certificate-based authentication with credentials stored in Azure Key Vault
- **Data Plane**: Federated identity credentials with OIDC for worker node components

Control plane components authenticate using certificates retrieved from Azure Key Vault via the Secrets Store CSI Driver.

## ControlPlaneManagedIdentities

The `ControlPlaneManagedIdentities` struct contains all managed identities required for control plane components in managed Azure HostedClusters. Each identity corresponds to a specific OpenShift operator or controller that needs Azure API access.

### Type Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `managedIdentitiesKeyVault` | `ManagedAzureKeyVault` | Yes | Configuration for the Azure Key Vault containing managed identity certificates. |
| `cloudProvider` | `ManagedIdentity` | Yes | Identity for the Azure cloud controller manager (cloud provider). |
| `nodePoolManagement` | `ManagedIdentity` | Yes | Identity for the Cluster API Provider Azure (CAPZ) operator managing NodePools. |
| `controlPlaneOperator` | `ManagedIdentity` | Yes | Identity for the control plane operator. |
| `imageRegistry` | `ManagedIdentity` | No | Identity for the cluster-image-registry-operator. |
| `ingress` | `ManagedIdentity` | Yes | Identity for the cluster-ingress-operator. |
| `network` | `ManagedIdentity` | Yes | Identity for the cluster-network-operator. |
| `disk` | `ManagedIdentity` | Yes | Identity for the Azure Disk CSI driver controller. |
| `file` | `ManagedIdentity` | Yes | Identity for the Azure File CSI driver controller. |

### Component Identity Mapping

| Control Plane Identity | OpenShift Component | Purpose |
|------------------------|---------------------|---------|
| `cloudProvider` | azure-cloud-provider | Manages Azure load balancers, routes, and cloud infrastructure |
| `nodePoolManagement` | cluster-api-provider-azure | Creates and manages Azure VMs for worker nodes |
| `controlPlaneOperator` | control-plane-operator | Manages control plane component lifecycle |
| `imageRegistry` | cluster-image-registry-operator | Manages container image storage in Azure Blob |
| `ingress` | cluster-ingress-operator | Manages Azure DNS records for ingress |
| `network` | cluster-network-operator | Manages Azure network configuration |
| `disk` | azure-disk-csi-driver-controller | Manages Azure Disk persistent volumes |
| `file` | azure-file-csi-driver-controller | Manages Azure File persistent volumes |

### ManagedAzureKeyVault Type

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `string` | Yes | The name of the Azure Key Vault on the management cluster containing managed identity certificates. |
| `tenantID` | `string` | Yes | The tenant ID of the Azure Key Vault. |

## DataPlaneManagedIdentities

The `DataPlaneManagedIdentities` struct contains client IDs for managed identities used by data plane (worker node) components. These identities use federated credentials with OIDC for authentication.

### Type Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `imageRegistryMSIClientID` | `string` | Yes | Client ID of the managed identity for the image registry controller on worker nodes. |
| `diskMSIClientID` | `string` | Yes | Client ID of the managed identity for the Azure Disk CSI driver on worker nodes. |
| `fileMSIClientID` | `string` | Yes | Client ID of the managed identity for the Azure File CSI driver on worker nodes. |

### Data Plane vs Control Plane Identities

Data plane identities differ from control plane identities in several ways:

- **Location**: Data plane components run on worker nodes, not in the control plane namespace
- **Authentication**: Use OIDC federation with projected ServiceAccount tokens
- **Configuration**: Only require client IDs (no Key Vault secret references)
- **Scope**: Limited to storage and registry operations on worker nodes

## ManagedIdentity Type

The `ManagedIdentity` type represents a single managed identity used by a control plane component.

### Type Reference

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `clientID` | `string` | No | - | The client ID (UUID) of the managed identity. Optional, primarily used for CI purposes. Must be a valid UUID in the format `8-4-4-4-12` (e.g., `12345678-1234-5678-9012-123456789012`). |
| `credentialsSecretName` | `string` | Yes | - | The name of the Azure Key Vault secret containing the managed identity credentials. Must be 1-127 characters using only alphanumeric characters and hyphens. |
| `objectEncoding` | `string` | Yes | `utf-8` | The encoding format of the certificate in Key Vault. Valid values are `utf-8`, `hex`, or `base64`. Must match the encoding used when storing the certificate. |

### Credentials Secret Format

The `credentialsSecretName` references a secret in Azure Key Vault that contains JSON-formatted credentials with the following structure:

```json
{
  "clientId": "<managed-identity-client-id>",
  "clientSecret": "<certificate-or-token>",
  "authenticationEndpoint": "<azure-auth-endpoint>",
  "tenantId": "<azure-tenant-id>",
  "notBefore": "<validity-start-time>",
  "notAfter": "<validity-end-time>"
}
```

The Secrets Store CSI Driver retrieves these secrets from Key Vault and mounts them into control plane pods.

### Object Encoding

The `objectEncoding` field specifies how the certificate is encoded in the Key Vault secret:

| Value | Description |
|-------|-------------|
| `utf-8` | Default encoding. Certificate stored as UTF-8 text. |
| `hex` | Certificate stored as hexadecimal string. |
| `base64` | Certificate stored as base64-encoded string. |

!!! warning "Encoding Mismatch"

    If `objectEncoding` does not match the actual encoding of the stored certificate, the Secrets Store CSI Driver will fail to read the secret. Errors will appear on the SecretProviderClass custom resource.

## AzureKMSSpec (Secret Encryption)

The `AzureKMSSpec` type configures Azure Key Vault-based secret encryption for etcd. This is configured separately from the control plane identities under `spec.secretEncryption.kms.azure`.

### Type Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `activeKey` | `AzureKMSKey` | Yes | The active key used to encrypt new secrets. |
| `backupKey` | `AzureKMSKey` | No | The previous key used during rotation so existing secrets can still be decrypted. |
| `kms` | `ManagedIdentity` | Yes | The managed identity used to authenticate with Azure Key Vault for KMS operations. |

### AzureKMSKey Type

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `keyVaultName` | `string` | Yes | The name of the Azure Key Vault containing the encryption key. |
| `keyName` | `string` | Yes | The name of the key in the Key Vault. |
| `keyVersion` | `string` | Yes | The version of the key to use. |

### KMS Identity Mapping

| Identity | OpenShift Component | Purpose |
|----------|---------------------|---------|
| `kms` | kube-apiserver | Encrypts/decrypts etcd secrets using Azure Key Vault keys |

The KMS identity uses the same `ManagedIdentity` type as control plane identities and its credentials are mounted via the `managed-azure-kms` SecretProviderClass.

## Example: Managed Azure HostedCluster

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: my-aro-hcp-cluster
  namespace: clusters
spec:
  platform:
    type: Azure
    azure:
      location: eastus
      resourceGroup: my-managed-rg
      subscriptionID: "12345678-1234-5678-9012-123456789012"
      tenantID: "87654321-4321-8765-2109-876543210987"
      vnetID: /subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/my-network-rg/providers/Microsoft.Network/virtualNetworks/my-vnet
      subnetID: /subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/my-network-rg/providers/Microsoft.Network/virtualNetworks/my-vnet/subnets/my-subnet
      securityGroupID: /subscriptions/12345678-1234-5678-9012-123456789012/resourceGroups/my-network-rg/providers/Microsoft.Network/networkSecurityGroups/my-nsg

      # Identity configuration for managed Azure HostedClusters
      azureAuthenticationConfig:
        azureAuthenticationConfigType: ManagedIdentities
        managedIdentities:
          # Control plane managed identities (8 components)
          controlPlane:
            managedIdentitiesKeyVault:
              name: my-keyvault
              tenantID: "87654321-4321-8765-2109-876543210987"

            cloudProvider:
              clientID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
              credentialsSecretName: cloud-provider-cert
              objectEncoding: utf-8

            nodePoolManagement:
              clientID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
              credentialsSecretName: nodepool-mgmt-cert
              objectEncoding: utf-8

            controlPlaneOperator:
              clientID: "cccccccc-cccc-cccc-cccc-cccccccccccc"
              credentialsSecretName: cpo-cert
              objectEncoding: utf-8

            imageRegistry:
              clientID: "dddddddd-dddd-dddd-dddd-dddddddddddd"
              credentialsSecretName: image-registry-cert
              objectEncoding: utf-8

            ingress:
              clientID: "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"
              credentialsSecretName: ingress-cert
              objectEncoding: utf-8

            network:
              clientID: "ffffffff-ffff-ffff-ffff-ffffffffffff"
              credentialsSecretName: network-cert
              objectEncoding: utf-8

            disk:
              clientID: "11111111-1111-1111-1111-111111111111"
              credentialsSecretName: disk-csi-cert
              objectEncoding: utf-8

            file:
              clientID: "22222222-2222-2222-2222-222222222222"
              credentialsSecretName: file-csi-cert
              objectEncoding: utf-8

          # Data plane managed identities (worker nodes)
          dataPlane:
            imageRegistryMSIClientID: "33333333-3333-3333-3333-333333333333"
            diskMSIClientID: "44444444-4444-4444-4444-444444444444"
            fileMSIClientID: "55555555-5555-5555-5555-555555555555"

  # Secret encryption with Azure KMS
  secretEncryption:
    type: kms
    kms:
      provider: Azure
      azure:
        activeKey:
          keyVaultName: my-kms-keyvault
          keyName: my-encryption-key
          keyVersion: "1234567890abcdef"
        kms:
          clientID: "66666666-6666-6666-6666-666666666666"
          credentialsSecretName: kms-cert
          objectEncoding: utf-8

  # ... other HostedCluster configuration
  release:
    image: quay.io/openshift-release-dev/ocp-release:4.19.0-multi
```

## Mapping to Azure Resources

The identity configuration maps to these Azure resources:

| Configuration Field | Azure Resource Type | Resource Location |
|---------------------|---------------------|-------------------|
| `managedIdentitiesKeyVault.name` | Azure Key Vault | Management cluster resource group |
| `*.credentialsSecretName` | Key Vault Secret | Inside the configured Key Vault |
| `*.clientID` | User Assigned Managed Identity | Customer subscription |
| `dataPlane.*MSIClientID` | User Assigned Managed Identity | Customer subscription (federated) |

## Validation Rules

The API enforces the following validation rules:

1. **Discriminator Validation**: The `azureAuthenticationConfigType` must be `ManagedIdentities` for managed Azure HostedClusters.

2. **Client ID Format**: All `clientID` fields must be valid UUIDs in the format `8-4-4-4-12` (e.g., `12345678-1234-5678-9012-123456789012`).

3. **Credentials Secret Name**: Must be 1-127 characters using only alphanumeric characters and hyphens (`^[a-zA-Z0-9-]+$`).

4. **Object Encoding**: Must be one of `utf-8`, `hex`, or `base64`.
