#!/bin/bash
set -x

# Prerequisites.
#  setup_MIv3_kv.sh
PREFIX="${PREFIX:-}"
SUBSCRIPTION_ID=${SUBSCRIPTION_ID:-}
LOCATION="${LOCATION:-}"
PERSISTENT_RG_NAME="${PERSISTENT_RG_NAME:-}"

# Local.
AKS_RG="${AKS_RG:-}"
AKS_CLUSTER_NAME="${AKS_CLUSTER_NAME:-}"
KV_NAME="${PREFIX}"
AKS_CP_MI_NAME="${PREFIX}-aks-mi"
AKS_KUBELET_MI_NAME="${PREFIX}-aks-kubelet-mi"

# Create Azure RG
az group create \
--name ${AKS_RG} \
--location ${LOCATION}

az identity create --name $AKS_CP_MI_NAME --resource-group $PERSISTENT_RG_NAME
az identity create --name $AKS_KUBELET_MI_NAME --resource-group $PERSISTENT_RG_NAME
export AKS_CP_MI_ID=$(az identity show --name $AKS_CP_MI_NAME --resource-group $PERSISTENT_RG_NAME --query id -o tsv)
export AKS_KUBELET_MI_ID=$(az identity show --name $AKS_KUBELET_MI_NAME --resource-group $PERSISTENT_RG_NAME --query id -o tsv)

# Create AKS Cluster
az aks create \
--resource-group ${AKS_RG} \
--name ${AKS_CLUSTER_NAME} \
--node-count 3 \
--generate-ssh-keys \
--load-balancer-sku standard \
--os-sku AzureLinux \
--node-vm-size Standard_D4s_v4 \
--enable-fips-image \
--kubernetes-version 1.31.1 \
--enable-addons azure-keyvault-secrets-provider \
--enable-secret-rotation \
--rotation-poll-interval 1m \
--assign-identity ${AKS_CP_MI_ID} \
--assign-kubelet-identity ${AKS_KUBELET_MI_ID} \
--network-plugin azure \
--network-policy azure

# Save the KV MI Info
AZURE_KEY_VAULT_AUTHORIZED_OBJECT_ID=$(az aks show -n ${AKS_CLUSTER_NAME} -g ${AKS_RG} | jq .addonProfiles.azureKeyvaultSecretsProvider.identity.objectId -r)

# Give the MI the Key Vault Secrets role
az role assignment create \
--assignee-object-id "${AZURE_KEY_VAULT_AUTHORIZED_OBJECT_ID}" \
--role "Key Vault Secrets User" \
--scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/"${PERSISTENT_RG_NAME}" \
--assignee-principal-type ServicePrincipal

# Get kubeconfig access
az aks get-credentials \
--resource-group ${AKS_RG} \
--name ${AKS_CLUSTER_NAME} \
--overwrite-existing

set +x
