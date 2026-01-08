---
title: Create Azure IAM resources separately
---

# Create Azure IAM resources separately

The `hypershift create iam azure` command creates Azure workload identities separately from infrastructure,
following the same pattern as AWS and GCP. This enables you to manage IAM resources independently from
your cluster infrastructure lifecycle.

## Overview

For self-managed Azure HyperShift clusters, workload identities authenticate cluster components to Azure
services using OIDC federation. You can either:

1. Create identities automatically during infrastructure creation (using `--oidc-issuer-url` with `create infra azure`)
2. **Create identities separately** using `create iam azure` and consume them during infrastructure or cluster creation

This guide covers option 2, which provides more control over the IAM lifecycle.

## Prerequisites

Before creating Azure IAM resources, ensure you have:

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
- An OIDC issuer URL for workload identity federation

## Creating Workload Identities

Use the `hypershift create iam azure` command:

```bash
hypershift create iam azure \
    --name CLUSTER_NAME \
    --infra-id INFRA_ID \
    --azure-creds AZURE_CREDENTIALS_FILE \
    --location LOCATION \
    --resource-group-name RESOURCE_GROUP \
    --oidc-issuer-url OIDC_ISSUER_URL \
    --output-file workload-identities.json
```

where:

* `CLUSTER_NAME` is the name of the hosted cluster you intend to create.
* `INFRA_ID` is a unique identifier used to name Azure resources. Typically this is the cluster name
    with a random suffix appended.
* `AZURE_CREDENTIALS_FILE` points to an Azure credentials file with permission to create
    managed identities and federated credentials.
* `LOCATION` is the Azure region for the managed identities (e.g., `eastus`, `westus2`).
* `RESOURCE_GROUP` is the name of an existing resource group where identities will be created.
* `OIDC_ISSUER_URL` is the URL of the OIDC identity provider used for workload identity federation.

Running this command creates:

* 7 User-Assigned Managed Identities (one per cluster component):
    - Disk CSI driver
    - File CSI driver
    - Image Registry
    - Ingress Operator
    - Cloud Provider
    - NodePool Management
    - Network Operator
* Federated Identity Credentials for each identity, configured with the OIDC issuer

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
  "network": { ... }
}
```

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

## Destroying Workload Identities

To destroy the workload identities that were created:

```bash
hypershift destroy iam azure \
    --azure-creds AZURE_CREDENTIALS_FILE \
    --workload-identities-file workload-identities.json
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
| `--location` | Azure region for identities |
| `--oidc-issuer-url` | OIDC issuer URL for federation |

### Optional Flags for `create iam azure`

| Flag | Description | Default |
|------|-------------|---------|
| `--resource-group-name` | Resource group for identities | `{name}-{infra-id}` |
| `--output-file` | Output file path | `{name}-iam-output.json` |
| `--cloud` | Azure cloud environment | `AzurePublicCloud` |

### Required Flags for `destroy iam azure`

| Flag | Description |
|------|-------------|
| `--azure-creds` | Path to Azure credentials JSON file |
| `--workload-identities-file` | Path to workload identities JSON file |

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
export OIDC_ISSUER_URL="https://my-oidc-issuer.com"

# 2. Create a resource group for identities
az group create --name ${NAME}-rg --location ${LOCATION}

# 3. Create workload identities
hypershift create iam azure \
    --name ${NAME} \
    --infra-id ${INFRA_ID} \
    --azure-creds ${AZURE_CREDS} \
    --location ${LOCATION} \
    --resource-group-name ${NAME}-rg \
    --oidc-issuer-url ${OIDC_ISSUER_URL} \
    --output-file workload-identities.json

# 4. Create infrastructure using pre-created identities
hypershift create infra azure \
    --name ${NAME} \
    --infra-id ${INFRA_ID} \
    --azure-creds ${AZURE_CREDS} \
    --base-domain ${BASE_DOMAIN} \
    --location ${LOCATION} \
    --workload-identities-file workload-identities.json \
    --output-file infra-output.yaml

# 5. Create the cluster
hypershift create cluster azure \
    --name ${NAME} \
    --infra-id ${INFRA_ID} \
    --azure-creds ${AZURE_CREDS} \
    --base-domain ${BASE_DOMAIN} \
    --location ${LOCATION} \
    --pull-secret /path/to/pull-secret \
    --infra-json infra-output.yaml

# --- Cleanup ---

# 6. Destroy the cluster
hypershift destroy cluster azure --name ${NAME}

# 7. Destroy infrastructure
hypershift destroy infra azure \
    --name ${NAME} \
    --infra-id ${INFRA_ID} \
    --azure-creds ${AZURE_CREDS}

# 8. Destroy IAM resources
hypershift destroy iam azure \
    --azure-creds ${AZURE_CREDS} \
    --workload-identities-file workload-identities.json
```

## See Also

- [Create Azure Infrastructure Separately](create-infra-separately.md)
- [Azure Workload Identity Setup](azure-workload-identity-setup.md)
- [Self-Managed Azure Overview](self-managed-azure-index.md)
