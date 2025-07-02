#!/bin/bash
set -x

# Constants
PREFIX="user"
AKS_RG=${PREFIX}"-aks-rg"
AKS_CLUSTER_NAME=${PREFIX}"-aks-cluster"
SUBSCRIPTION_ID="<your-subscription-id>"
PERSISTENT_RG_NAME="<your-persistent-rg-name>"

# AZURE_KEY_VAULT_AUTHORIZED_OBJECT_ID is from the setup_aks_cluster.sh script
ROLE_ASSIGNMENT_ID=$(az role assignment list \
  --assignee "${AZURE_KEY_VAULT_AUTHORIZED_OBJECT_ID}" \
  --role "Key Vault Secrets User" \
  --scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/"${PERSISTENT_RG_NAME}" \
  --output tsv \
  --query "[].id")

az role assignment delete --ids "$ROLE_ASSIGNMENT_ID"

az aks delete -g $AKS_RG -n $AKS_CLUSTER_NAME --yes

az group delete -n ${AKS_RG} --yes

set +x