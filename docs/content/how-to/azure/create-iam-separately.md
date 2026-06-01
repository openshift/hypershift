---
title: Create Azure IAM resources separately
---

# Create Azure IAM resources separately

!!! note "Developer Preview in OCP 4.21"

    Self-managed Azure HostedClusters are available as a Developer Preview feature in OpenShift Container Platform 4.21.

The `hypershift create iam azure` command creates Azure workload identities separately from infrastructure,
following the same pattern as AWS and GCP. This enables you to manage IAM resources independently from
your cluster infrastructure lifecycle.

## Overview

For self-managed Azure HyperShift clusters, workload identities authenticate cluster components to Azure
services using OIDC federation. The setup consists of two steps:

1. **Configure an OIDC Issuer** using the Cloud Credential Operator (CCO) tool
2. **Create workload identities** using `hypershift create iam azure`

You must create identities separately using `create iam azure` and then
consume them during infrastructure or cluster creation via the `--workload-identities-file` flag.

!!! warning "Persistent Resource Groups"

    When setting up workload identities and OIDC issuer for the first time, create them in a **persistent resource group** that will not be deleted when individual clusters are destroyed. This allows you to reuse the same workload identities and OIDC issuer across multiple HostedClusters, reducing setup time and avoiding unnecessary resource recreation.

    - Use a persistent resource group like `os4-common`.
    - This resource group should be separate from the cluster-specific resource groups that get created and deleted with each HostedCluster.
    - The OIDC issuer storage account should also be created in this persistent resource group.

## Prerequisites

Before creating Azure IAM resources, ensure you have:

- Azure CLI (`az`) installed and configured
- Cloud Credential Operator (CCO) tool (`ccoctl`) installed
- `jq` command-line JSON processor
- Appropriate Azure permissions
- An Azure credentials file with the following format:
    ```json
    {
      "subscriptionId": "your-subscription-id",
      "tenantId": "your-tenant-id",
      "clientId": "your-client-id",
      "clientSecret": "your-client-secret"
    }
    ```
- An existing resource group where the managed identities will be created

## Configure OIDC Issuer

Before creating workload identities, you need an OIDC issuer URL. Use the Cloud Credential Operator (CCO) tool to create one:

```bash
# Set OIDC issuer variables
PERSISTENT_RG_NAME="os4-common"  # Use persistent resource group
LOCATION="eastus"
OIDC_STORAGE_ACCOUNT_NAME="yourstorageaccount"
TENANT_ID="your-tenant-id"
SUBSCRIPTION_ID="your-subscription-id"

# Create persistent resource group (if it doesn't exist)
az group create --name $PERSISTENT_RG_NAME --location $LOCATION

# Create an RSA key pair and save the private and public key
ccoctl azure create-key-pair

SA_TOKEN_ISSUER_PRIVATE_KEY_PATH="/path/to/serviceaccount-signer.private"
SA_TOKEN_ISSUER_PUBLIC_KEY_PATH="/path/to/serviceaccount-signer.public"

# Create OIDC issuer using CCO tool in persistent resource group
ccoctl azure create-oidc-issuer \
    --oidc-resource-group-name ${PERSISTENT_RG_NAME} \
    --tenant-id ${TENANT_ID} \
    --region ${LOCATION} \
    --name ${OIDC_STORAGE_ACCOUNT_NAME} \
    --subscription-id ${SUBSCRIPTION_ID} \
    --public-key-file ${SA_TOKEN_ISSUER_PUBLIC_KEY_PATH}

# Set OIDC issuer URL
OIDC_ISSUER_URL="https://${OIDC_STORAGE_ACCOUNT_NAME}.blob.core.windows.net/${OIDC_STORAGE_ACCOUNT_NAME}"
```

## Creating Workload Identities

Use the `hypershift create iam azure` command:

```bash
CLUSTER_NAME="my-self-managed-cluster"
INFRA_ID="${CLUSTER_NAME}-$(openssl rand -hex 4)"
AZURE_CREDS="/path/to/azure-creds.json"

hypershift create iam azure \
    --name $CLUSTER_NAME \
    --infra-id $INFRA_ID \
    --azure-creds $AZURE_CREDS \
    --location $LOCATION \
    --resource-group-name $PERSISTENT_RG_NAME \
    --oidc-issuer-url $OIDC_ISSUER_URL \
    --output-file workload-identities.json
```

where:

* `CLUSTER_NAME` is the name of the hosted cluster you intend to create.
* `INFRA_ID` is a unique identifier used to name Azure resources. Typically this is the cluster name
    with a random suffix appended.
* `AZURE_CREDS` points to an Azure credentials file with permission to create
    managed identities and federated credentials.
* `LOCATION` is the Azure region for the managed identities (e.g., `eastus`, `westus2`).
* `PERSISTENT_RG_NAME` is the name of an existing resource group where identities will be created.
* `OIDC_ISSUER_URL` is the URL of the OIDC identity provider created in the previous step.

Running this command creates:

* 8 User-Assigned Managed Identities (one per cluster component):
    - Disk CSI driver
    - File CSI driver
    - Image Registry
    - Ingress Operator
    - Cloud Provider
    - NodePool Management
    - Network Operator
    - Control Plane Operator
* Federated Identity Credentials for each identity, configured with the OIDC issuer

### Enabling KMS Identity

To also create a KMS identity for Azure Key Vault etcd encryption at rest, add the `--enable-kms` flag:

```bash
hypershift create iam azure \
    --name $CLUSTER_NAME \
    --infra-id $INFRA_ID \
    --azure-creds $AZURE_CREDS \
    --location $LOCATION \
    --resource-group-name $PERSISTENT_RG_NAME \
    --oidc-issuer-url $OIDC_ISSUER_URL \
    --output-file workload-identities.json \
    --enable-kms
```

!!! warning "KMS Key Vault Role Assignment"

    If you use `--enable-kms`, you must **manually** assign the `Key Vault Crypto User` role to the KMS identity on your Key Vault. The `--auto-assign-roles` flag does not cover this because the Key Vault scope is user-provided. See [Enabling KMS Encryption](create-self-managed-azure-cluster.md#enabling-kms-encryption-etcd-encryption-at-rest) for the role assignment commands.

## Private Endpoint Access

The **Control Plane Operator** identity is always created by `create iam azure`. For private
clusters, this identity is used to manage Private Endpoints, Private DNS zones, VNet links,
and DNS A records in the guest subscription.

The CPO identity is assigned the **Contributor** role by default, scoped to the managed
resource group, NSG resource group, and VNet resource group. When using
`--assign-custom-hcp-roles`, a more restrictive custom role is used instead.

!!! note

    The private endpoint access topology is configured during cluster creation using
    `--endpoint-access Private` on the `hypershift create cluster azure` command.
    See [Deploy Azure Private Clusters](deploy-azure-private-clusters.md) for details.

## Output Format

The output file contains the workload identities in JSON format, directly consumable by the
`--workload-identities-file` flag in `create cluster azure` and `create infra azure` commands:

```json
{
  "disk": {
    "tenantID": "...",
    "clientID": "...",
    "resourceID": "/subscriptions/.../providers/Microsoft.ManagedIdentity/userAssignedIdentities/my-cluster-abc123-disk"
  },
  "file": {
    "tenantID": "...",
    "clientID": "...",
    "resourceID": "..."
  },
  "imageRegistry": { ... },
  "ingress": { ... },
  "cloudProvider": { ... },
  "nodePoolManagement": { ... },
  "network": { ... },
  "controlPlaneOperator": { ... }
}
```

!!! note

    The `controlPlaneOperator` entry is always present. For public clusters, this identity
    is created but not used by the control plane operator.

## Using Pre-created Identities

### With Infrastructure Creation

Pass the output file to `create infra azure`:

```bash
hypershift create infra azure \
    --name CLUSTER_NAME \
    --infra-id INFRA_ID \
    --azure-creds AZURE_CREDENTIALS_FILE \
    --base-domain BASE_DOMAIN \
    --location LOCATION \
    --workload-identities-file workload-identities.json \
    --output-file infra-output.yaml
```

### With Cluster Creation

Or pass directly to `create cluster azure`:

```bash
hypershift create cluster azure \
    --name CLUSTER_NAME \
    --infra-id INFRA_ID \
    --azure-creds AZURE_CREDENTIALS_FILE \
    --base-domain BASE_DOMAIN \
    --location LOCATION \
    --pull-secret PULL_SECRET_FILE \
    --workload-identities-file workload-identities.json
```

## Verification

Verify the setup:

```bash
# List created managed identities
az identity list --resource-group $PERSISTENT_RG_NAME --output table

# Verify federated credentials for one identity
az identity federated-credential list \
    --identity-name "${CLUSTER_NAME}-disk-${INFRA_ID}" \
    --resource-group $PERSISTENT_RG_NAME

# Test OIDC issuer accessibility
curl -s "${OIDC_ISSUER_URL}/.well-known/openid-configuration" | jq .
```

## Destroying Workload Identities

To destroy the workload identities that were created:

```bash
hypershift destroy iam azure \
    --azure-creds AZURE_CREDENTIALS_FILE \
    --workload-identities-file workload-identities.json \
    --resource-group-name RESOURCE_GROUP \
    --name CLUSTER_NAME \
    --infra-id INFRA_ID \
    --dns-zone-rg-name DNS_ZONE_RG
```

The destroy command reads the output file from create to identify which identities to delete.
Both the managed identities and their federated credentials are removed.

!!! warning "Destroy Order"

    If you created infrastructure using these identities, destroy the infrastructure first
    before destroying the IAM resources.

## Command Reference

### Required Flags for `create iam azure`

| Flag | Description |
|------|-------------|
| `--name` | Name of the HostedCluster |
| `--infra-id` | Unique infrastructure identifier |
| `--azure-creds` | Path to Azure credentials JSON file |
| `--resource-group-name` | Resource group for identities |
| `--oidc-issuer-url` | OIDC issuer URL for federation |
| `--output-file` | Output file path |

### Optional Flags for `create iam azure`

| Flag | Description | Default |
|------|-------------|---------|
| `--location` | Azure region for identities | `eastus` |
| `--cloud` | Azure cloud environment | `AzurePublicCloud` |
| `--enable-kms` | Create KMS identity for etcd encryption | `false` |

### Required Flags for `destroy iam azure`

| Flag | Description |
|------|-------------|
| `--azure-creds` | Path to Azure credentials JSON file |
| `--workload-identities-file` | Path to workload identities JSON file |
| `--resource-group-name` | Resource group containing the identities |
| `--name` | Name of the HostedCluster |
| `--infra-id` | Unique infrastructure identifier |
| `--dns-zone-rg-name` | Resource group containing the Azure DNS zone |

### Optional Flags for `destroy iam azure`

| Flag | Description | Default |
|------|-------------|---------|
| `--cloud` | Azure cloud environment | `AzurePublicCloud` |

## Workflow Example

Here's a complete workflow for creating a self-managed Azure cluster with separate IAM management:

```bash
# 1. Set variables
export NAME="my-cluster"
export INFRA_ID="${NAME}-$(openssl rand -hex 4)"
export LOCATION="eastus"
export BASE_DOMAIN="example.com"
export AZURE_CREDS="/path/to/azure-creds.json"
export PERSISTENT_RG_NAME="os4-common"
export TENANT_ID="your-tenant-id"
export SUBSCRIPTION_ID="your-subscription-id"
export OIDC_STORAGE_ACCOUNT_NAME="yourstorageaccount"
export RELEASE_IMAGE="quay.io/openshift-release-dev/ocp-release:XYZ"

# 2. Create persistent resource group (if it doesn't exist)
az group create --name ${PERSISTENT_RG_NAME} --location ${LOCATION}

# 3. Create OIDC issuer
ccoctl azure create-key-pair
ccoctl azure create-oidc-issuer \
    --oidc-resource-group-name ${PERSISTENT_RG_NAME} \
    --tenant-id ${TENANT_ID} \
    --region ${LOCATION} \
    --name ${OIDC_STORAGE_ACCOUNT_NAME} \
    --subscription-id ${SUBSCRIPTION_ID} \
    --public-key-file /path/to/serviceaccount-signer.public
export OIDC_ISSUER_URL="https://${OIDC_STORAGE_ACCOUNT_NAME}.blob.core.windows.net/${OIDC_STORAGE_ACCOUNT_NAME}"

# 4. Create workload identities
hypershift create iam azure \
    --name ${NAME} \
    --infra-id ${INFRA_ID} \
    --azure-creds ${AZURE_CREDS} \
    --location ${LOCATION} \
    --resource-group-name ${PERSISTENT_RG_NAME} \
    --oidc-issuer-url ${OIDC_ISSUER_URL} \
    --output-file workload-identities.json

# 5. Create infrastructure using pre-created identities
hypershift create infra azure \
    --name ${NAME} \
    --infra-id ${INFRA_ID} \
    --azure-creds ${AZURE_CREDS} \
    --base-domain ${BASE_DOMAIN} \
    --location ${LOCATION} \
    --workload-identities-file workload-identities.json \
    --assign-identity-roles \
    --dns-zone-rg-name ${PERSISTENT_RG_NAME} \
    --output-file infra-output.yaml

# 6. Create the cluster
hypershift create cluster azure \
    --name ${NAME} \
    --infra-id ${INFRA_ID} \
    --azure-creds ${AZURE_CREDS} \
    --base-domain ${BASE_DOMAIN} \
    --location ${LOCATION} \
    --pull-secret /path/to/pull-secret \
    --generate-ssh \
    --release-image ${RELEASE_IMAGE} \
    --sa-token-issuer-private-key-path /path/to/serviceaccount-signer.private \
    --oidc-issuer-url ${OIDC_ISSUER_URL} \
    --dns-zone-rg-name ${PERSISTENT_RG_NAME} \
    --assign-service-principal-roles \
    --infra-json infra-output.yaml \
    --diagnostics-storage-account-type Managed

# --- Cleanup ---

# 7. Destroy the cluster
hypershift destroy cluster azure \
    --name ${NAME} \
    --azure-creds ${AZURE_CREDS} \
    --dns-zone-rg-name ${PERSISTENT_RG_NAME}

# 8. Destroy infrastructure
hypershift destroy infra azure \
    --name ${NAME} \
    --infra-id ${INFRA_ID} \
    --azure-creds ${AZURE_CREDS}

# 9. Destroy IAM resources (only if no longer needed)
hypershift destroy iam azure \
    --azure-creds ${AZURE_CREDS} \
    --workload-identities-file workload-identities.json \
    --resource-group-name ${PERSISTENT_RG_NAME} \
    --name ${NAME} \
    --infra-id ${INFRA_ID} \
    --dns-zone-rg-name ${PERSISTENT_RG_NAME}
```

## See Also

- [Create Azure Infrastructure Separately](create-infra-separately.md)
- [Self-Managed Azure Overview](self-managed-azure-index.md)
- [Deploy Azure Private Clusters](deploy-azure-private-clusters.md) — End-to-end guide for private endpoint access
