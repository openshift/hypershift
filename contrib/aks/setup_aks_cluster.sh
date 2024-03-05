#!/bin/bash
set -x

# Constants
RG="aks-test"
LOCATION="eastus"
AKS_CLUSTER_NAME="test"

# Clear out existing Azure RG
az group delete -n ${RG} --yes

# Create Azure RG
az group create \
--name ${RG} \
--location ${LOCATION}

# Create AKS Cluster
az aks create \
--resource-group ${RG} \
--name ${AKS_CLUSTER_NAME} \
--node-count 2 \
--generate-ssh-keys \
--load-balancer-sku standard \
--os-sku AzureLinux

# Get kubeconfig access
az aks get-credentials \
--resource-group ${RG} \
--name ${AKS_CLUSTER_NAME} \
--overwrite-existing

set +x