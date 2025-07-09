#!/bin/bash
set -x

# Prerequisites.
PERSISTENT_RG_NAME=${PERSISTENT_RG_NAME:-}
OIDC_ISSUER_URL=${OIDC_ISSUER_URL:-}

# Local.
DP_OUTPUT_FILE=${DP_OUTPUT_FILE:-}
AZURE_DISK_MI_NAME="azure-disk-MI-${PREFIX}"
AZURE_FILE_MI_NAME="azure-file-MI-${PREFIX}"
IMAGE_REGISTRY_MI_NAME="image-registry-MI-${PREFIX}"
AZURE_DISK_CLIENT_ID=$(az identity create --name $AZURE_DISK_MI_NAME --resource-group $PERSISTENT_RG_NAME --query clientId -o tsv)
AZURE_FILE_CLIENT_ID=$(az identity create --name $AZURE_FILE_MI_NAME --resource-group $PERSISTENT_RG_NAME --query clientId -o tsv)
IMAGE_REGISTRY_CLIENT_ID=$(az identity create --name $IMAGE_REGISTRY_MI_NAME --resource-group $PERSISTENT_RG_NAME --query clientId -o tsv)

cat <<EOF > "${DP_OUTPUT_FILE}"
{
  "imageRegistryMSIClientID": ${IMAGE_REGISTRY_CLIENT_ID},
  "diskMSIClientID": ${AZURE_DISK_CLIENT_ID},
  "fileMSIClientID": ${AZURE_FILE_CLIENT_ID}
}
EOF

# Set up the federated IDs for Workload Identity
az identity federated-credential create --name "${AZURE_DISK_MI_NAME}"-fed-id \
--identity-name "${AZURE_DISK_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-node-sa \
--audience openshift

az identity federated-credential create --name "${AZURE_FILE_MI_NAME}"-fed-id \
--identity-name "${AZURE_FILE_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-node-sa \
--audience openshift

az identity federated-credential create --name "${IMAGE_REGISTRY_MI_NAME}"-fed-id \
--identity-name "${IMAGE_REGISTRY_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-image-registry:registry \
--audience openshift

set +x