# Create a Self-Managed Azure HostedCluster

!!! note "Developer Preview in OCP 4.21"
    
    Self-managed Azure HostedClusters are available as a Developer Preview feature in OpenShift Container Platform 4.21.

This document describes how to create a self-managed Azure HostedCluster using workload identities for authentication.

## Overview

Self-managed Azure HostedClusters use [Azure Workload Identity](https://azure.github.io/azure-workload-identity/docs/) for authentication.

## Prerequisites

Before creating a self-managed Azure HostedCluster, ensure you have:

- Azure CLI (`az`) installed and configured
- HyperShift CLI binary
- OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`)
- `jq` command-line JSON processor
- An Azure OpenShift management cluster with HyperShift operator installed
- Azure workload identities and OIDC issuer configured
- Appropriate Azure permissions (see [Permission Requirements](#permission-requirements))
- **Optional**: External DNS configured (only if you want automatic DNS management)

!!! note "Setup Requirements"

    This guide assumes you have already completed the workload identity configuration and management cluster setup. Follow these guides in order:

    1. [Create Azure IAM Resources](create-iam-separately.md) - Workload identities and OIDC issuer configuration
    2. [Setup Azure Management Cluster for HyperShift](setup-management-cluster.md) - HyperShift operator installation (with or without External DNS)

### Permission Requirements

Your Azure service principal must have the following permissions:

- **Subscription Level**:
    - `Contributor` role
    - `User Access Administrator` role
- **Microsoft Graph API**:
    - `Application.ReadWrite.OwnedBy` permission (requires DPTP request in most cases)

## Creating the Self-Managed Azure HostedCluster

### Infrastructure Setup

Create the Azure infrastructure using the HyperShift CLI:

```bash
# Set cluster configuration variables
CLUSTER_NAME="my-self-managed-cluster"
INFRA_ID="${CLUSTER_NAME}-$(openssl rand -hex 4)"
LOCATION="eastus"
BASE_DOMAIN="example.com"
AZURE_CREDS="/path/to/azure-creds.json"
PULL_SECRET="/path/to/pull-secret.json"
RELEASE_IMAGE="quay.io/openshift-release-dev/ocp-release:XYZ"
PERSISTENT_RG_NAME="os4-common"
OIDC_ISSUER_URL="https://yourstorageaccount.blob.core.windows.net/yourstorageaccount"
SA_TOKEN_ISSUER_PRIVATE_KEY_PATH="/path/to/serviceaccount-signer.private"

# Create infrastructure
hypershift create infra azure \
    --name ${CLUSTER_NAME} \
    --infra-id ${INFRA_ID} \
    --azure-creds ${AZURE_CREDS} \
    --base-domain ${BASE_DOMAIN} \
    --location ${LOCATION} \
    --workload-identities-file workload-identities.json \
    --assign-identity-roles \
    --dns-zone-rg-name ${PERSISTENT_RG_NAME} \
    --output-file infra-output.yaml
```

This creates the resource groups, VNet, subnet, NSG, Private DNS zone, and load balancer for your cluster. For advanced options like using existing network resources, see [Create Azure Infrastructure Separately](create-infra-separately.md).

### Create the HostedCluster

!!! note "Azure Marketplace Images"

    For OpenShift 4.20 and later, HyperShift automatically selects the appropriate Azure Marketplace image from the release payload. You no longer need to specify `--marketplace-*` flags unless you want to use a specific custom image. See [Configuring Azure Marketplace Images](#configuring-azure-marketplace-images) for more details.

Create the HostedCluster using the infrastructure output:

```bash
hypershift create cluster azure \
    --name "$CLUSTER_NAME" \
    --infra-id "$INFRA_ID" \
    --azure-creds $AZURE_CREDS \
    --location ${LOCATION} \
    --node-pool-replicas 2 \
    --base-domain $BASE_DOMAIN \
    --pull-secret $PULL_SECRET \
    --generate-ssh \
    --release-image ${RELEASE_IMAGE} \
    --sa-token-issuer-private-key-path "${SA_TOKEN_ISSUER_PRIVATE_KEY_PATH}" \
    --oidc-issuer-url "${OIDC_ISSUER_URL}" \
    --dns-zone-rg-name ${PERSISTENT_RG_NAME} \
    --assign-service-principal-roles \
    --infra-json infra-output.yaml \
    --diagnostics-storage-account-type Managed
```

!!! tip "External DNS"

    If using External DNS for automatic DNS management, also pass
    `--external-dns-domain <your-dns-zone-name>` to the cluster creation command.
    See [Setup Azure Management Cluster](setup-management-cluster.md) for DNS configuration.

!!! tip "Private Clusters"

    To create a private cluster with Azure Private Link, see
    [Deploy Azure Private Clusters](deploy-azure-private-clusters.md).
    Private clusters require additional setup: a NAT subnet in the management
    cluster's VNet, `--endpoint-access Private` flag, and HyperShift operator
    installation with `--private-platform Azure`.

### Configuring Azure Marketplace Images

HyperShift supports multiple approaches for configuring Azure Marketplace images for your cluster nodes. The recommended approach varies based on your OpenShift version and requirements.

#### For OpenShift 4.20 and Later (Recommended)

**Pattern 1: Use Release Payload Defaults (Simplest)**

For OpenShift 4.20+, HyperShift automatically selects the appropriate Azure Marketplace image from the release payload. Simply omit all marketplace-related flags:

```bash
# No marketplace flags needed - HyperShift will auto-select the image
# Gen2 VM generation is used by default
hypershift create cluster azure \
    --name "$CLUSTER_NAME" \
    # ... other flags ...
```

This is the **recommended approach** as it ensures your nodes use the officially tested and supported image for your OpenShift version.

**Pattern 2: Specify VM Generation Only**

If you need to use a specific VM generation (Gen1 or Gen2), you can specify only the `--image-generation` flag:

```bash
hypershift create cluster azure \
    --name "$CLUSTER_NAME" \
    --image-generation Gen2 \  # Or Gen1 (case-sensitive)
    # ... other flags ...
```

!!! note "VM Generation"

    - Valid values: `Gen1` or `Gen2` (case-sensitive)
    - Default: `Gen2` (recommended for new clusters)
    - Gen2 VMs offer better performance and support for newer Azure features

**Pattern 3: Use Custom Marketplace Image**

If you need to use a specific custom marketplace image, provide all marketplace details:

```bash
hypershift create cluster azure \
    --name "$CLUSTER_NAME" \
    --marketplace-publisher azureopenshift \
    --marketplace-offer aro4 \
    --marketplace-sku aro_419 \
    --marketplace-version 419.6.20250523 \
    --image-generation Gen2 \  # Optional, defaults to Gen2
    # ... other flags ...
```

!!! important "Marketplace Flag Requirements"

    When specifying marketplace details, you must provide **all four** flags (`--marketplace-publisher`, `--marketplace-offer`, `--marketplace-sku`, `--marketplace-version`) together. Partial specification is not allowed.

#### For OpenShift Versions Before 4.20

For OpenShift versions prior to 4.20, you must explicitly specify marketplace image details (Pattern 3 above) or provide a custom image ID. The automatic image selection from release payload is not available.

#### Adding NodePools with Custom Images

When creating additional NodePools, you can specify image configuration in the same way:

```bash
# Use default from release payload (OCP 4.20+)
hypershift create nodepool azure \
    --cluster-name "$CLUSTER_NAME" \
    # ... other flags ...

# Or specify generation
hypershift create nodepool azure \
    --cluster-name "$CLUSTER_NAME" \
    --image-generation Gen1 \
    # ... other flags ...

# Or use custom marketplace image
hypershift create nodepool azure \
    --cluster-name "$CLUSTER_NAME" \
    --marketplace-publisher azureopenshift \
    --marketplace-offer aro4 \
    --marketplace-sku aro_419 \
    --marketplace-version 419.6.20250523 \
    # ... other flags ...
```

!!! important "Key Configuration Options"
    
    - `--infra-json`: Path to infrastructure output from `hypershift create infra azure` (includes workload identities)
    - `--assign-service-principal-roles`: Automatically assigns required Azure roles to workload identities
    - `--sa-token-issuer-private-key-path`: Path to the private key for service account token signing
    - `--oidc-issuer-url`: URL of the OIDC issuer created in the IAM setup
    - `--image-generation`: (Optional) VM generation (`Gen1` or `Gen2`, defaults to `Gen2`). For OCP 4.20+, omit to use release payload defaults. See [Configuring Azure Marketplace Images](#configuring-azure-marketplace-images)
    - `--marketplace-publisher/offer/sku/version`: (Optional) Explicit Azure Marketplace image. Must specify all four flags together, or omit all to use defaults (OCP 4.20+)
    - `--dns-zone-rg-name`: Resource group containing the DNS zone (os4-common)
    - `--diagnostics-storage-account-type Managed`: Use Azure managed storage for diagnostics

## Enabling KMS Encryption (etcd Encryption at Rest)

Self-managed Azure HostedClusters support encrypting etcd data at rest using [Azure Key Vault](https://learn.microsoft.com/en-us/azure/key-vault/general/overview) with the KMSv2 protocol. This requires:

1. An Azure Key Vault with a cryptographic key
2. A workload identity with `Key Vault Crypto User` role on the Key Vault

### Prerequisites

Ensure the `kms` workload identity is included in your `workload-identities.json` file. When using `hypershift create iam azure`, pass the `--enable-kms` flag to create the KMS identity. See [Enabling KMS Identity](create-iam-separately.md#enabling-kms-identity) for details.

### Create a Key Vault and Key

!!! note "RBAC Key Vault Permissions"

    The Key Vault is created with `--enable-rbac-authorization`, which means the creator does **not** automatically get data plane access. You must have the `Key Vault Crypto Officer` role (or equivalent) on the Key Vault to create and manage keys. If the key creation step fails with a `Forbidden` error, assign yourself the role:

    ```bash
    MY_OBJECT_ID=$(az ad signed-in-user show --query id -o tsv)
    KV_ID=$(az keyvault show --name "${KV_NAME}" --query id -o tsv)
    az role assignment create \
        --assignee-object-id "${MY_OBJECT_ID}" \
        --assignee-principal-type User \
        --role "Key Vault Crypto Officer" \
        --scope "${KV_ID}"
    ```

```bash
# Create Key Vault
KV_NAME="${CLUSTER_NAME}-kv"
MANAGED_RG_NAME="${CLUSTER_NAME}-managed-rg"
az keyvault create \
    --name "${KV_NAME}" \
    --resource-group "${MANAGED_RG_NAME}" \
    --location "${LOCATION}" \
    --enable-rbac-authorization

# Create encryption key
KEY_NAME="${CLUSTER_NAME}-etcd-key"
az keyvault key create \
    --vault-name "${KV_NAME}" \
    --name "${KEY_NAME}" \
    --kty RSA \
    --size 2048

# Get the key ID (used as --encryption-key-id)
ENCRYPTION_KEY_ID=$(az keyvault key show \
    --vault-name "${KV_NAME}" \
    --name "${KEY_NAME}" \
    --query key.kid -o tsv)
```

### Assign Key Vault Crypto User Role to the KMS Identity

!!! warning "Manual Step Required"

    The `--auto-assign-roles` / `--assign-service-principal-roles` flag does **not** assign the Key Vault role because the Key Vault scope is user-provided and not known to the CLI at role-assignment time. You must perform this role assignment manually.

Grant the KMS workload identity the `Key Vault Crypto User` role on your Key Vault so it can encrypt and decrypt etcd data:

```bash
# Get the principal ID of the KMS managed identity
# The identity name follows the pattern: {clusterName}-kms-{infraID}
# List identities in the resource group to find the exact name:
#   az identity list --resource-group "${PERSISTENT_RG_NAME}" --query "[?contains(name, 'kms')]" -o table
KMS_MI_NAME=$(az identity list \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --query "[?contains(name, '${CLUSTER_NAME}-kms')].name" -o tsv)
KMS_PRINCIPAL_ID=$(az identity show \
    --name "${KMS_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --query principalId -o tsv)

# Get the Key Vault resource ID
KV_ID=$(az keyvault show --name "${KV_NAME}" --query id -o tsv)

# Assign Key Vault Crypto User role to the KMS identity
az role assignment create \
    --assignee-object-id "${KMS_PRINCIPAL_ID}" \
    --assignee-principal-type ServicePrincipal \
    --role "Key Vault Crypto User" \
    --scope "${KV_ID}"
```

### Create the Cluster with KMS

Add the `--encryption-key-id` flag to your cluster creation command:

```bash
hypershift create cluster azure \
    --name "$CLUSTER_NAME" \
    --infra-id "$INFRA_ID" \
    --azure-creds $AZURE_CREDS \
    --location ${LOCATION} \
    --node-pool-replicas 2 \
    --base-domain $BASE_DOMAIN \
    --pull-secret $PULL_SECRET \
    --generate-ssh \
    --release-image ${RELEASE_IMAGE} \
    --sa-token-issuer-private-key-path "${SA_TOKEN_ISSUER_PRIVATE_KEY_PATH}" \
    --oidc-issuer-url "${OIDC_ISSUER_URL}" \
    --dns-zone-rg-name ${PERSISTENT_RG_NAME} \
    --assign-service-principal-roles \
    --infra-json infra-output.yaml \
    --encryption-key-id "${ENCRYPTION_KEY_ID}" \
    --diagnostics-storage-account-type Managed
```

!!! note "KMS Authentication"

    For self-managed Azure, the KMS provider authenticates using the `kms` workload identity specified in your `workload-identities.json`. This is different from managed Azure (ARO HCP), which uses managed identities with CSI secret store volumes. The `--kms-credentials-secret-name` flag is not needed for self-managed clusters.

## Verification

Check the cluster status and access:

```bash
# Check HostedCluster status
oc get hostedcluster $CLUSTER_NAME -n clusters

# Wait for cluster to be available
oc wait --for=condition=Available hostedcluster/$CLUSTER_NAME -n clusters --timeout=30m

# Get kubeconfig and access the cluster
hypershift create kubeconfig --name $CLUSTER_NAME > $CLUSTER_NAME-kubeconfig
export KUBECONFIG=$CLUSTER_NAME-kubeconfig
oc get nodes
oc get clusterversion
```

## Cleanup

To delete the HostedCluster and its infrastructure:

```bash
# Delete the HostedCluster
hypershift destroy cluster azure \
    --name $CLUSTER_NAME \
    --azure-creds $AZURE_CREDS \
    --dns-zone-rg-name $PERSISTENT_RG_NAME

# Destroy infrastructure
hypershift destroy infra azure \
    --name $CLUSTER_NAME \
    --infra-id $INFRA_ID \
    --azure-creds $AZURE_CREDS
```

!!! note "Resource Cleanup"
    
    The HyperShift destroy commands clean up the cluster and infrastructure resources. Workload identities and OIDC issuer created during setup can be reused for other clusters or cleaned up separately if no longer needed. See [Destroying Workload Identities](create-iam-separately.md#destroying-workload-identities).

## Related Documentation

1. [Create Azure IAM Resources](create-iam-separately.md) - Workload identities and OIDC issuer setup
2. [Create Azure Infrastructure Separately](create-infra-separately.md) - Advanced infrastructure options
3. [Setup Azure Management Cluster for HyperShift](setup-management-cluster.md) - DNS and HyperShift operator setup
