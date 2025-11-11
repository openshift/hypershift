#!/bin/bash
set -o nounset
set -x

# Prerequisites.
PREFIX=${PREFIX:?"Provide prefix"}
PERSISTENT_RG_NAME=${PERSISTENT_RG_NAME:?"Provide persistent resource group name"}

# Local.
AKS_CP_MI_NAME="${PREFIX}-aks-mi"
AKS_KUBELET_MI_NAME="${PREFIX}-aks-kubelet-mi"

az identity create --name $AKS_CP_MI_NAME --resource-group $PERSISTENT_RG_NAME
az identity create --name $AKS_KUBELET_MI_NAME --resource-group $PERSISTENT_RG_NAME

set +x