# Azure Workload Identity Setup for Self-Managed Clusters

!!! note "Developer Preview in OCP 4.21"
    
    Self-managed Azure HostedClusters are available as a Developer Preview feature in OpenShift Container Platform 4.21.

This document describes how to set up Azure Workload Identities and OIDC issuer for self-managed Azure HostedClusters.

!!! warning "Persistent Resource Groups"
    
    When setting up workload identities and OIDC issuer for the first time, create them in a **persistent resource group** that will not be deleted when individual clusters are destroyed. This allows you to reuse the same workload identities and OIDC issuer across multiple HostedClusters, reducing setup time and avoiding unnecessary resource recreation.
    
    - Use a persistent resource group like `os4-common`.
    - This resource group should be separate from the cluster-specific resource groups that get created and deleted with each HostedCluster.
    - The OIDC issuer storage account should also be created in this persistent resource group.

## Prerequisites

- Azure CLI (`az`) installed and configured
- `jq` command-line JSON processor
- Cloud Credential Operator (CCO) tool installed
- Appropriate Azure permissions

## Create Azure Workload Identities

You can create the required managed identities and federated credentials using the HyperShift CLI:

```bash
# Set environment variables
PERSISTENT_RG_NAME="os4-common"  # Use persistent resource group
LOCATION="eastus"
CLUSTER_NAME="my-self-managed-cluster"
INFRA_ID="${CLUSTER_NAME}-$(openssl rand -hex 4)"
AZURE_CREDS="/path/to/azure-creds.json"

# Create persistent resource group (if it doesn't exist)
az group create --name $PERSISTENT_RG_NAME --location $LOCATION

# Create workload identities using the HyperShift CLI
# (requires OIDC issuer URL - see next section if you haven't set this up yet)
hypershift create iam azure \
    --name $CLUSTER_NAME \
    --infra-id $INFRA_ID \
    --azure-creds $AZURE_CREDS \
    --location $LOCATION \
    --resource-group-name $PERSISTENT_RG_NAME \
    --oidc-issuer-url $OIDC_ISSUER_URL \
    --output-file workload-identities.json
```

This creates 7 managed identities with federated credentials for:

- Disk CSI driver
- File CSI driver
- Image Registry
- Ingress Operator
- Cloud Provider
- NodePool Management
- Network Operator

For complete documentation on the IAM commands, see [Create Azure IAM Resources Separately](create-iam-separately.md).

## Configure OIDC Issuer

Use the Cloud Credential Operator (CCO) tool to create the OIDC issuer:

```bash
# Set OIDC issuer variables (reusing variables from previous steps)
OIDC_STORAGE_ACCOUNT_NAME="yourstorageaccount"
TENANT_ID="your-tenant-id"
# SUBSCRIPTION_ID and PERSISTENT_RG_NAME already set from previous section
# Create an RSA key pair and save the private and public key
ccoctl azure create-key-pair  

SA_TOKEN_ISSUER_PRIVATE_KEY_PATH="/path/to/serviceaccount-signer.private"
SA_TOKEN_ISSUER_PUBLIC_KEY_PATH="/path/to/serviceaccount-signer.public"

# Create OIDC issuer using CCO tool in os4-common resource group
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

## Verification

Verify the setup:

```bash
# List created managed identities
az identity list --resource-group $PERSISTENT_RG_NAME --output table

# Verify federated credentials for one identity
az identity federated-credential list \
    --identity-name "${AZURE_DISK_MI_NAME}" \
    --resource-group $PERSISTENT_RG_NAME

# Test OIDC issuer accessibility
curl -s "${OIDC_ISSUER_URL}/.well-known/openid-configuration" | jq .
```

## Next Steps

After setting up workload identities, you can proceed to:

- [Setup Azure Management Cluster for HyperShift](setup-management-cluster.md)