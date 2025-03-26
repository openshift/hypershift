#!/bin/bash
set -x

# Prerequisites:
# 1. This guide assumes you've already created an Azure Key Vault. Here is how to do that:
#    az keyvault create \
#    --name ${KV_NAME} \
#    --resource-group ${AKS_RG} \
#    --location ${LOCATION} \
#    --enable-rbac-authorization
#
#    For Red Hat developers, it is best to create your KV and the managed identities, listed as AKS_MI and
#    AKS_KUBELET_MI, in a resource group that is persistent in your tenant/subscription.

# Constants
PREFIX="<your-prefix-here>"
AKS_RG=${PREFIX}"-aks-rg"
LOCATION="eastus"
AKS_CLUSTER_NAME=${PREFIX}"-aks-cluster"
SUBSCRIPTION_ID="<your-subscription-id-here>"
KV_NAME="<your-key-vault-name-here>"
AKS_MI="<your-persistent-MI-resource-id-for-aks>"
AKS_KUBELET_MI="<your-persistent-MI-resource-id-for-aks-kubelet>"

# I got this from searching my login name in Azure and pulling up my user profile
OBJECT_ID="<your-object-id>"

# Create Azure RG
az group create \
--name ${AKS_RG} \
--location ${LOCATION}

# Create AKS Cluster
az aks create \
--resource-group ${AKS_RG} \
--name ${AKS_CLUSTER_NAME} \
--node-count 2 \
--generate-ssh-keys \
--load-balancer-sku standard \
--os-sku AzureLinux \
--node-vm-size Standard_D4s_v4 \
--enable-fips-image \
--kubernetes-version 1.31.1 \
--enable-addons azure-keyvault-secrets-provider \
--enable-secret-rotation \
--rotation-poll-interval 1m \
--assign-identity ${AKS_MI} \
--assign-kubelet-identity ${AKS_KUBELET_MI}

# Save the KV MI Info
AZURE_KEY_VAULT_AUTHORIZED_USER_ID=$(az aks show -n ${AKS_CLUSTER_NAME} -g ${AKS_RG} | jq .addonProfiles.azureKeyvaultSecretsProvider.identity.clientId -r)
AZURE_KEY_VAULT_AUTHORIZED_OBJECT_ID=$(az aks show -n ${AKS_CLUSTER_NAME} -g ${AKS_RG} | jq .addonProfiles.azureKeyvaultSecretsProvider.identity.objectId -r)

# Give the MI the Key Vault Secrets role
az role assignment create \
--assignee-object-id "${AZURE_KEY_VAULT_AUTHORIZED_OBJECT_ID}" \
--role "Key Vault Secrets User" \
--scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/"${AKS_RG}" \
--assignee-principal-type ServicePrincipal

## Associate your SP with the Key Vault; this is so you can use your SP with the CLI to create certificates in the key vault
az role assignment create \
    --assignee ${OBJECT_ID} \
    --scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/"${AKS_RG}"/providers/Microsoft.KeyVault/vaults/${KV_NAME} \
    --role "Key Vault Administrator"

# Get kubeconfig access
az aks get-credentials \
--resource-group ${AKS_RG} \
--name ${AKS_CLUSTER_NAME} \
--overwrite-existing

set +x
