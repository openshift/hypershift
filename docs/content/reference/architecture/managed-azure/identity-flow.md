# Managed Azure HostedClusters Identity Flow

This document provides a high-level overview of how Azure identities flow through the managed Azure HostedClusters architecture, from HostedCluster configuration to Azure API authentication.

The diagrams below show the key aspects of the identity flow. For detailed information on each component, see the linked documentation at the bottom of this page.

## Sequence Diagram

The following diagram shows the identity flow using the Control Plane Operator (CPO) as an example. Other control plane pods that need to authenticate with Azure follow a similar pattern, using their respective SecretProviderClass and credential file.

!!! note "ARO_HCP_KEY_VAULT_USER_CLIENT_ID"
    The `ARO_HCP_KEY_VAULT_USER_CLIENT_ID` environment variable contains the client ID of a user-assigned managed identity on the management cluster. This identity is authorized to pull secrets from the Azure Key Vault. It is set on the HyperShift Operator during installation and passed to the SecretProviderClass as `userAssignedIdentityID`.

```mermaid
sequenceDiagram
    participant HC as HostedCluster CR
    participant HO as HyperShift Operator
    participant CPO as Control Plane Operator
    participant SPC as SecretProviderClass
    participant CSI as CSI Driver
    participant KV as Azure Key Vault
    participant SDK as Azure SDK
    participant AAD as Azure AD
    participant ARM as Azure ARM API

    Note over HC,ARM: Setup Phase
    HC->>HO: HostedCluster created with ManagedIdentities config
    HO->>SPC: Creates SecretProviderClasses (managed-azure-cpo, etc.)
    HO->>CPO: Deploys Control Plane Operator

    Note over HC,ARM: Pod Startup Phase
    CPO->>CSI: Pod starts with CSI volume mount
    CSI->>SPC: Reads SecretProviderClass configuration
    CSI->>KV: Authenticates with ARO_HCP_KEY_VAULT_USER_CLIENT_ID managed identity
    KV-->>CSI: Returns UserAssignedIdentityCredentials JSON
    CSI->>CPO: Mounts credential file at /mnt/certs

    Note over HC,ARM: Runtime Authentication
    CPO->>SDK: Loads credential file
    SDK->>SDK: Parses JSON, decodes PEM certificate
    SDK->>AAD: Client certificate authentication
    AAD-->>SDK: Returns Azure Access Token
    SDK->>ARM: API call with Bearer token
    ARM-->>CPO: Azure resource operation result
```

## Component Identity Mapping

```mermaid
graph LR
    subgraph HC_CR[HostedCluster CR]
        CP[ControlPlaneManagedIdentities]
    end

    subgraph SPC_RESOURCES[SecretProviderClasses]
        SPC1[managed-azure-cloud-provider]
        SPC2[managed-azure-cpo]
        SPC3[managed-azure-nodepool-management]
        SPC4[managed-azure-ingress]
        SPC5[managed-azure-network]
        SPC6[managed-azure-disk-csi]
        SPC7[managed-azure-file-csi]
        SPC8[managed-azure-image-registry]
        SPC9[managed-azure-kms]
    end

    subgraph CP_PODS[Control Plane Pods]
        CCM[Cloud Controller Manager]
        CPO2[Control Plane Operator]
        CAPZ[CAPZ]
        ING[Ingress Operator]
        CNO[Network Operator]
        DISK[Disk CSI Driver]
        FILE[File CSI Driver]
        REG[Image Registry]
        KAS[Kube API Server]
    end

    CP -->|cloudProvider| SPC1 --> CCM
    CP -->|controlPlaneOperator| SPC2 --> CPO2
    CP -->|nodePoolManagement| SPC3 --> CAPZ
    CP -->|ingress| SPC4 --> ING
    CP -->|network| SPC5 --> CNO
    CP -->|disk| SPC6 --> DISK
    CP -->|file| SPC7 --> FILE
    CP -->|imageRegistry| SPC8 --> REG
    KMS_ID[secretEncryption.kms.azure.kms] -->|kms| SPC9 --> KAS
```

## Key Vault Secret Structure

```mermaid
graph TB
    subgraph KV_VAULT[Azure Key Vault]
        KV[(Key Vault)]
    end

    subgraph SECRET_JSON[Secret Content - JSON]
        JSON[UserAssignedIdentityCredentials]
        JSON --> F1[authentication_endpoint]
        JSON --> F2[client_id]
        JSON --> F3[client_secret]
        JSON --> F4[tenant_id]
        JSON --> F5[not_before / not_after]
    end

    subgraph PEM_BUNDLE[Decoded client_secret - PEM]
        PEM[PEM Bundle]
        PEM --> CERT[X.509 Certificate]
        PEM --> KEY[Private Key]
    end

    KV --> JSON
    F3 -->|base64 decode| PEM
```

## Detailed Documentation

Each stage of the identity flow is documented in detail:

| Stage | Documentation | Description |
|-------|---------------|-------------|
| 1. HostedCluster Configuration | [HostedCluster Identity Configuration](./hostedcluster-identity-configuration.md) | API field reference for `AzureAuthenticationConfiguration`, `ControlPlaneManagedIdentities`, `DataPlaneManagedIdentities`, and `ManagedIdentity` types |
| 2. Azure Key Vault | [Key Vault Secret Structure](./keyvault-secret-structure.md) | Secret naming conventions, `UserAssignedIdentityCredentials` JSON schema, PEM certificate format, and `objectEncoding` options |
| 3-4. SecretProviderClass & CSI | [Secrets CSI Usage](./secrets-csi.md) | How `SecretProviderClass` CRs are created and how the Secrets Store CSI driver mounts credentials into pods |
| 5. Pod Volume Mounts | [Pod Volume Mounts](./pod-volume-mounts.md) | Helper functions, mount paths, environment variables, and example pod specs |
| 6-8. Authentication Flow | [Azure Authentication Flow](./azure-authentication-flow.md) | Complete authentication chain from credential loading through Azure AD to ARM API access |

### Additional References

- [SecretProviderClass Configuration](./secretproviderclass-configuration.md) - Deep dive into `ReconcileManagedAzureSecretProviderClass` function and all SecretProviderClass resources created by HO/CPO
