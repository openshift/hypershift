#!/bin/bash
set -o nounset
set -x

# Prerequisites.
#  setup_MIv3_kv.sh
PREFIX=${PREFIX:?"Provide prefix"}
SUBSCRIPTION_ID=${SUBSCRIPTION_ID:?"Provide subscription ID"}
LOCATION=${LOCATION:?"Provide location"}
PERSISTENT_RG_NAME=${PERSISTENT_RG_NAME:?"Provide persistent resource group name"}

# Local.
AKS_RG=${AKS_RG:?"Provide AKS resource group name"}
AKS_CLUSTER_NAME=${AKS_CLUSTER_NAME:?"Provide AKS cluster name"}
AKS_CP_MI_NAME="${PREFIX}-aks-mi"
AKS_KUBELET_MI_NAME="${PREFIX}-aks-kubelet-mi"

# Create Azure RG
az group create \
--name ${AKS_RG} \
--location ${LOCATION}

export AKS_CP_MI_ID=$(az identity show --name $AKS_CP_MI_NAME --resource-group $PERSISTENT_RG_NAME --query id -o tsv)
export AKS_KUBELET_MI_ID=$(az identity show --name $AKS_KUBELET_MI_NAME --resource-group $PERSISTENT_RG_NAME --query id -o tsv)

# Node VM size for the AKS cluster.
NODE_VM_SIZE="Standard_D4s_v5"

# `az aks list-vm-skus` lives in the aks-preview extension, not the core CLI.
# Ensure it is installed (and current) so this script does not rely on Azure
# CLI's dynamic-install behavior, which is disabled in some environments/CI.
az extension add --name aks-preview --upgrade

# Discover which availability zones support the node VM size in this region,
# using the AKS-specific SKU list (authoritative for AKS node pools; general
# `az vm list-skus` may report zones that AKS won't accept). The nodes are then
# spread across those zones.
#
# This matters because the hypershift-sharedingress router runs 2 replicas with a
# required pod anti-affinity on topology.kubernetes.io/zone: it needs at least 2
# zones, or the second replica stays Pending ("didn't match pod anti-affinity
# rules"). Zone and SKU availability varies by region and subscription, so we
# fail fast with a clear message rather than create a cluster the router cannot
# schedule on.
AVAILABLE_ZONES=$(az aks list-vm-skus \
--location "${LOCATION}" \
--size "${NODE_VM_SIZE}" \
--zone \
--query "[?name=='${NODE_VM_SIZE}'].locationInfo[0].zones[]" \
-o tsv | sort -u | tr '\n' ' ')

ZONE_COUNT=$(echo ${AVAILABLE_ZONES} | wc -w | tr -d ' ')
if [[ "${ZONE_COUNT}" -lt 2 ]]; then
	echo "ERROR: ${NODE_VM_SIZE} in ${LOCATION} exposes ${ZONE_COUNT} availability zone(s) for this subscription; the hypershift-sharedingress router requires at least 2. Pick a region/VM size with multiple zones (e.g. eastus)." >&2
	exit 1
fi

# Create AKS Cluster.
# ${AVAILABLE_ZONES} is intentionally unquoted so it expands to one value per
# zone after --zones (e.g. "--zones 1 2 3").
az aks create \
--resource-group ${AKS_RG} \
--name ${AKS_CLUSTER_NAME} \
--node-count 3 \
--zones ${AVAILABLE_ZONES} \
--generate-ssh-keys \
--load-balancer-sku standard \
--os-sku AzureLinux \
--node-vm-size ${NODE_VM_SIZE} \
--enable-fips-image \
--enable-addons azure-keyvault-secrets-provider \
--enable-secret-rotation \
--rotation-poll-interval 1m \
--assign-identity ${AKS_CP_MI_ID} \
--assign-kubelet-identity ${AKS_KUBELET_MI_ID} \
--network-plugin azure \
--network-policy azure \
--max-pods 250

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
