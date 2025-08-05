#!/bin/bash
set -x

# Prerequisites.
PERSISTENT_RG_NAME=${PERSISTENT_RG_NAME:-}
OIDC_ISSUER_URL=${OIDC_ISSUER_URL:-}

# Local.
WORKLOAD_IDENTITIES_FILE=${WORKLOAD_IDENTITIES_FILE:-}
AZURE_DISK_MI_NAME="workload-azure-disk-MI-${PREFIX}"
AZURE_FILE_MI_NAME="workload-azure-file-MI-${PREFIX}"
IMAGE_REGISTRY_MI_NAME="workload-image-registry-MI-${PREFIX}"
INGRESS_MI_NAME="workload-ingress-MI-${PREFIX}"
CLOUD_PROVIDER_MI_NAME="workload-cloud-provider-MI-${PREFIX}"
NODE_POOL_MANAGEMENT_MI_NAME="workload-nodepool-mgmt-MI-${PREFIX}"
NETWORK_MI_NAME="workload-network-MI-${PREFIX}"

AZURE_DISK_CLIENT_ID=$(az identity create --name $AZURE_DISK_MI_NAME --resource-group $PERSISTENT_RG_NAME --query clientId -o tsv)
AZURE_FILE_CLIENT_ID=$(az identity create --name $AZURE_FILE_MI_NAME --resource-group $PERSISTENT_RG_NAME --query clientId -o tsv)
IMAGE_REGISTRY_CLIENT_ID=$(az identity create --name $IMAGE_REGISTRY_MI_NAME --resource-group $PERSISTENT_RG_NAME --query clientId -o tsv)
INGRESS_CLIENT_ID=$(az identity create --name $INGRESS_MI_NAME --resource-group $PERSISTENT_RG_NAME --query clientId -o tsv)
CLOUD_PROVIDER_CLIENT_ID=$(az identity create --name $CLOUD_PROVIDER_MI_NAME --resource-group $PERSISTENT_RG_NAME --query clientId -o tsv)
NODE_POOL_MANAGEMENT_CLIENT_ID=$(az identity create --name $NODE_POOL_MANAGEMENT_MI_NAME --resource-group $PERSISTENT_RG_NAME --query clientId -o tsv)
NETWORK_CLIENT_ID=$(az identity create --name $NETWORK_MI_NAME --resource-group $PERSISTENT_RG_NAME --query clientId -o tsv)

cat <<EOF > "${WORKLOAD_IDENTITIES_FILE}"
{
  "imageRegistry": {
    "clientID": "${IMAGE_REGISTRY_CLIENT_ID}"
  },
  "ingress": {
    "clientID": "${INGRESS_CLIENT_ID}"
  },
  "file": {
    "clientID": "${AZURE_FILE_CLIENT_ID}"
  },
  "disk": {
    "clientID": "${AZURE_DISK_CLIENT_ID}"
  },
  "nodePoolManagement": {
    "clientID": "${NODE_POOL_MANAGEMENT_CLIENT_ID}"
  },
  "cloudProvider": {
    "clientID": "${CLOUD_PROVIDER_CLIENT_ID}"
  },
  "network": {
    "clientID": "${NETWORK_CLIENT_ID}"
  }
}
EOF

# Set up the federated IDs for Workload Identity
az identity federated-credential create --name "${AZURE_DISK_MI_NAME}"-fed-id-node \
--identity-name "${AZURE_DISK_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-node-sa \
--audience openshift

az identity federated-credential create --name "${AZURE_DISK_MI_NAME}"-fed-id-operator \
--identity-name "${AZURE_DISK_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-operator \
--audience openshift

az identity federated-credential create --name "${AZURE_DISK_MI_NAME}"-fed-id-controller \
--identity-name "${AZURE_DISK_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-controller-sa \
--audience openshift

az identity federated-credential create --name "${AZURE_FILE_MI_NAME}"-fed-id-node \
--identity-name "${AZURE_FILE_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-node-sa \
--audience openshift

az identity federated-credential create --name "${AZURE_FILE_MI_NAME}"-fed-id-operator \
--identity-name "${AZURE_FILE_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-operator \
--audience openshift

az identity federated-credential create --name "${AZURE_FILE_MI_NAME}"-fed-id-controller \
--identity-name "${AZURE_FILE_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-controller-sa \
--audience openshift

az identity federated-credential create --name "${IMAGE_REGISTRY_MI_NAME}"-fed-id-registry \
--identity-name "${IMAGE_REGISTRY_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-image-registry:registry \
--audience openshift

az identity federated-credential create --name "${IMAGE_REGISTRY_MI_NAME}"-fed-id-operator \
--identity-name "${IMAGE_REGISTRY_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-image-registry:cluster-image-registry-operator \
--audience openshift

az identity federated-credential create --name "${INGRESS_MI_NAME}"-fed-id \
--identity-name "${INGRESS_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-ingress-operator:ingress-operator \
--audience openshift

az identity federated-credential create --name "${CLOUD_PROVIDER_MI_NAME}"-fed-id \
--identity-name "${CLOUD_PROVIDER_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cloud-controller-manager:cloud-controller-manager \
--audience openshift

az identity federated-credential create --name "${NODE_POOL_MANAGEMENT_MI_NAME}"-fed-id \
--identity-name "${NODE_POOL_MANAGEMENT_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cluster-api:capi-provider \
--audience openshift

az identity federated-credential create --name "${NETWORK_MI_NAME}"-fed-id \
--identity-name "${NETWORK_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cloud-network-config-controller:cloud-network-config-controller \
--audience openshift

set +x