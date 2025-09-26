# Azure Workload Identity Setup for Self-Managed Clusters

!!! note "Developer Preview in OCP 4.21"
    
    Self-managed Azure HostedClusters are available as a Developer Preview feature in OpenShift Container Platform 4.21.

This document describes how to set up Azure Workload Identities and OIDC issuer for self-managed Azure HostedClusters.

!!! warning "Persistent Resource Groups"
    
    When setting up workload identities and OIDC issuer for the first time, create them in a **persistent resource group** that will not be deleted when individual clusters are destroyed. This allows you to reuse the same workload identities and OIDC issuer across multiple HostedClusters, reducing setup time and avoiding unnecessary resource recreation.
    
    - Use a persistant resource group like `os4-common`.
    - This resource group should be separate from the cluster-specific resource groups that get created and deleted with each HostedCluster.
    - The OIDC issuer storage account should also be created in this persistent resource group.

## Prerequisites

- Azure CLI (`az`) installed and configured
- `jq` command-line JSON processor
- Cloud Credential Operator (CCO) tool installed
- Appropriate Azure permissions

## Create Azure Workload Identities

Create managed identities for each OpenShift component that needs Azure access:

```bash
# Set environment variables
PERSISTENT_RG_NAME="os4-common"  # Use persistent resource group
LOCATION="eastus"
CLUSTER_NAME="my-self-managed-cluster"
SUBSCRIPTION_ID=$(az account show --query id -o tsv)

# Create persistent resource group (if it doesn't exist)
az group create --name $PERSISTENT_RG_NAME --location $LOCATION

# Create managed identities for each component
declare -A COMPONENTS=(
    ["image-registry"]="cluster-image-registry-operator"
    ["ingress"]="cluster-ingress-operator"  
    ["file-csi"]="cluster-storage-operator-file"
    ["disk-csi"]="cluster-storage-operator-disk"
    ["nodepool-mgmt"]="cluster-api-provider-azure"
    ["cloud-provider"]="azure-cloud-provider"
    ["network"]="cluster-network-operator"
)

# Create managed identities and capture client IDs
declare -A CLIENT_IDS
for component in "${!COMPONENTS[@]}"; do
    echo "Creating managed identity for $component..."
    CLIENT_ID=$(az identity create \
        --name "${CLUSTER_NAME}-${component}" \
        --resource-group $PERSISTENT_RG_NAME \
        --query clientId -o tsv)
    CLIENT_IDS[$component]=$CLIENT_ID
    echo "Created identity ${CLUSTER_NAME}-${component} with client ID: $CLIENT_ID"
done
```

## Create Workload Identities Configuration File

Create a JSON file with all the workload identity client IDs:

```bash
cat <<EOF > workload-identities.json
{
  "imageRegistry": {
    "clientID": "${CLIENT_IDS[image-registry]}"
  },
  "ingress": {
    "clientID": "${CLIENT_IDS[ingress]}"
  },
  "file": {
    "clientID": "${CLIENT_IDS[file-csi]}"
  },
  "disk": {
    "clientID": "${CLIENT_IDS[disk-csi]}"
  },
  "nodePoolManagement": {
    "clientID": "${CLIENT_IDS[nodepool-mgmt]}"
  },
  "cloudProvider": {
    "clientID": "${CLIENT_IDS[cloud-provider]}"
  },
  "network": {
    "clientID": "${CLIENT_IDS[network]}"
  }
}
EOF
```

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

## Set Up Federated Identity Credentials

Configure federated identity credentials for each workload identity. These establish trust between Azure Entra ID and the specific service accounts in your HostedCluster:

```bash
# Define workload identity names (matching those created earlier)
# These should match the managed identities created in the previous section
AZURE_DISK_MI_NAME="${CLUSTER_NAME}-disk-csi"
AZURE_FILE_MI_NAME="${CLUSTER_NAME}-file-csi"
IMAGE_REGISTRY_MI_NAME="${CLUSTER_NAME}-image-registry"
INGRESS_MI_NAME="${CLUSTER_NAME}-ingress"
CLOUD_PROVIDER_MI_NAME="${CLUSTER_NAME}-cloud-provider"
NODE_POOL_MANAGEMENT_MI_NAME="${CLUSTER_NAME}-nodepool-mgmt"
NETWORK_MI_NAME="${CLUSTER_NAME}-network"

# Azure Disk CSI Driver federated credentials
az identity federated-credential create \
    --name "${AZURE_DISK_MI_NAME}-fed-id-node" \
    --identity-name "${AZURE_DISK_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --issuer "${OIDC_ISSUER_URL}" \
    --subject system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-node-sa \
    --audience openshift

az identity federated-credential create \
    --name "${AZURE_DISK_MI_NAME}-fed-id-operator" \
    --identity-name "${AZURE_DISK_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --issuer "${OIDC_ISSUER_URL}" \
    --subject system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-operator \
    --audience openshift

az identity federated-credential create \
    --name "${AZURE_DISK_MI_NAME}-fed-id-controller" \
    --identity-name "${AZURE_DISK_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --issuer "${OIDC_ISSUER_URL}" \
    --subject system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-controller-sa \
    --audience openshift

# Azure File CSI Driver federated credentials
az identity federated-credential create \
    --name "${AZURE_FILE_MI_NAME}-fed-id-node" \
    --identity-name "${AZURE_FILE_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --issuer "${OIDC_ISSUER_URL}" \
    --subject system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-node-sa \
    --audience openshift

az identity federated-credential create \
    --name "${AZURE_FILE_MI_NAME}-fed-id-operator" \
    --identity-name "${AZURE_FILE_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --issuer "${OIDC_ISSUER_URL}" \
    --subject system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-operator \
    --audience openshift

az identity federated-credential create \
    --name "${AZURE_FILE_MI_NAME}-fed-id-controller" \
    --identity-name "${AZURE_FILE_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --issuer "${OIDC_ISSUER_URL}" \
    --subject system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-controller-sa \
    --audience openshift

# Image Registry federated credentials
az identity federated-credential create \
    --name "${IMAGE_REGISTRY_MI_NAME}-fed-id-registry" \
    --identity-name "${IMAGE_REGISTRY_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --issuer "${OIDC_ISSUER_URL}" \
    --subject system:serviceaccount:openshift-image-registry:registry \
    --audience openshift

az identity federated-credential create \
    --name "${IMAGE_REGISTRY_MI_NAME}-fed-id-operator" \
    --identity-name "${IMAGE_REGISTRY_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --issuer "${OIDC_ISSUER_URL}" \
    --subject system:serviceaccount:openshift-image-registry:cluster-image-registry-operator \
    --audience openshift

# Ingress Operator federated credential
az identity federated-credential create \
    --name "${INGRESS_MI_NAME}-fed-id" \
    --identity-name "${INGRESS_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --issuer "${OIDC_ISSUER_URL}" \
    --subject system:serviceaccount:openshift-ingress-operator:ingress-operator \
    --audience openshift

# Cloud Provider federated credential
az identity federated-credential create \
    --name "${CLOUD_PROVIDER_MI_NAME}-fed-id" \
    --identity-name "${CLOUD_PROVIDER_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --issuer "${OIDC_ISSUER_URL}" \
    --subject system:serviceaccount:kube-system:azure-cloud-provider \
    --audience openshift

# Node Pool Management federated credential
az identity federated-credential create \
    --name "${NODE_POOL_MANAGEMENT_MI_NAME}-fed-id" \
    --identity-name "${NODE_POOL_MANAGEMENT_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --issuer "${OIDC_ISSUER_URL}" \
    --subject system:serviceaccount:kube-system:capi-provider \
    --audience openshift

# Network Operator federated credential
az identity federated-credential create \
    --name "${NETWORK_MI_NAME}-fed-id" \
    --identity-name "${NETWORK_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --issuer "${OIDC_ISSUER_URL}" \
    --subject system:serviceaccount:openshift-cloud-network-config-controller:cloud-network-config-controller \
    --audience openshift
```

!!! note "Service Account Mapping"
    
    Each federated identity credential maps a specific Azure managed identity to an OpenShift service account. The service accounts listed above are the default service accounts used by various OpenShift components for Azure integration.

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