#!/bin/bash
set -x

# This step sets up your Azure environment to give access to the SecretProviderClasses to mount the certificates
# onto the pods through the nested credentials object files in the file system of the pods (MIv3), as opposed to
# giving the SecretProviderClasses direct access to the certificates in the key vault (MIv2).
# For Red Hat developers, it is best to create your KV and the managed identities in a resource group that
# is persistent in your tenant/subscription.

# Prerequisites.
PREFIX="${PREFIX:-}"
SUBSCRIPTION_ID=${SUBSCRIPTION_ID:-}
PERSISTENT_RG_NAME=${PERSISTENT_RG_NAME:-}
LOCATION=${LOCATION:-}

# Local.
KV_NAME="${PREFIX}"
AZURE_DISK_SP_NAME="azure-disk-$PREFIX"
AZURE_FILE_SP_NAME="azure-file-$PREFIX"

CLOUD_PROVIDER_SP_NAME="cloud-provider-$PREFIX"
CONTROL_PLANE_SP_NAME="cpo-$PREFIX"
IMAGE_REGISTRY_SP_NAME="ciro-$PREFIX"
INGRESS_SP_NAME="ingress-$PREFIX"
CNCC_SP_NAME="cncc-$PREFIX"

NODEPOOL_MGMT="nodepool-mgmt-$PREFIX"
CP_OUTPUT_FILE=${CP_OUTPUT_FILE:-}

# Create Key Vault
USER_ACCOUNT_ID=$(az ad signed-in-user show | jq -r .id)
az keyvault create --name $KV_NAME --resource-group $PERSISTENT_RG_NAME --location $LOCATION --enable-rbac-authorization
az role assignment create --assignee ${USER_ACCOUNT_ID} --scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${PERSISTENT_RG_NAME}/providers/Microsoft.KeyVault/vaults/${KV_NAME} --role "Key Vault Administrator"

# Create Service Principals for the Control Plane Components
cloudProvider=$(az ad sp create-for-rbac --name "${CLOUD_PROVIDER_SP_NAME}" --create-cert --cert "${CLOUD_PROVIDER_SP_NAME}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${CLOUD_PROVIDER_SP_NAME}'}" -o json)
controlPlaneOperator=$(az ad sp create-for-rbac --name "${CONTROL_PLANE_SP_NAME}" --create-cert --cert "${CONTROL_PLANE_SP_NAME}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${CONTROL_PLANE_SP_NAME}'}" -o json)
disk=$(az ad sp create-for-rbac --name "${AZURE_DISK_SP_NAME}" --create-cert --cert "${AZURE_DISK_SP_NAME}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${AZURE_DISK_SP_NAME}'}" -o json)
file=$(az ad sp create-for-rbac --name "${AZURE_FILE_SP_NAME}" --create-cert --cert "${AZURE_FILE_SP_NAME}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${AZURE_FILE_SP_NAME}'}" -o json)
imageRegistry=$(az ad sp create-for-rbac --name "${IMAGE_REGISTRY_SP_NAME}" --create-cert --cert "${IMAGE_REGISTRY_SP_NAME}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${IMAGE_REGISTRY_SP_NAME}'}" -o json)
ingress=$(az ad sp create-for-rbac --name "${INGRESS_SP_NAME}" --create-cert --cert "${INGRESS_SP_NAME}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${INGRESS_SP_NAME}'}" -o json)
network=$(az ad sp create-for-rbac --name "${CNCC_SP_NAME}" --create-cert --cert "${CNCC_SP_NAME}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${CNCC_SP_NAME}'}" -o json)
nodePoolManagement=$(az ad sp create-for-rbac --name "${NODEPOOL_MGMT}" --create-cert --cert "${NODEPOOL_MGMT}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${NODEPOOL_MGMT}'}" -o json)

# Set Names
CERT_NAMES=(
    "${AZURE_DISK_SP_NAME}"
    "${AZURE_FILE_SP_NAME}"
    "${IMAGE_REGISTRY_SP_NAME}"
    "${CLOUD_PROVIDER_SP_NAME}"
    "${CNCC_SP_NAME}"
    "${CONTROL_PLANE_SP_NAME}"
    "${INGRESS_SP_NAME}"
    "${NODEPOOL_MGMT}"
)

# Create Secret JSON Files
for CERT_NAME in "${CERT_NAMES[@]}"; do
    echo "Processing certificate: $CERT_NAME"
    
    CERT_DETAILS=$(az keyvault secret show --vault-name $KV_NAME --name $CERT_NAME --query "{value: value, notBefore: attributes.notBefore, expires: attributes.expires}" -o json)
    CLIENT_SECRET=$(echo $CERT_DETAILS | jq -r '.value')
    NOT_BEFORE=$(echo $CERT_DETAILS | jq -r '.notBefore')
    NOT_AFTER=$(echo $CERT_DETAILS | jq -r '.expires')
    SP_DETAILS=$(az ad sp list --display-name $CERT_NAME --query "[0].{client_id: appId, tenant_id: appOwnerOrganizationId}" -o json)
    CLIENT_ID=$(echo $SP_DETAILS | jq -r '.client_id')
    TENANT_ID=$(echo $SP_DETAILS | jq -r '.tenant_id')

    if [[ -z "$CLIENT_ID" || -z "$TENANT_ID" ]]; then
        echo "Error: Could not retrieve client ID or tenant ID for certificate: $CERT_NAME"
        continue
    fi

    JSON_FILE="${CERT_NAME}.json"
    echo "{
        \"authentication_endpoint\": \"https://login.microsoftonline.com/\",
        \"client_id\": \"$CLIENT_ID\",
        \"client_secret\": \"$CLIENT_SECRET\",
        \"tenant_id\": \"$TENANT_ID\",
        \"not_before\": \"$NOT_BEFORE\",
        \"not_after\": \"$NOT_AFTER\"
    }" > $JSON_FILE

    echo "Created JSON file: $JSON_FILE"
done

# Add Secrets to Key Vault
for CERT_NAME in "${CERT_NAMES[@]}"; do
    echo "Processing certificate: $CERT_NAME"
    JSON_FILE="${CERT_NAME}.json"

    az keyvault secret set --name "${CERT_NAME}-json" --vault-name $KV_NAME --file $JSON_FILE
done

# Create Managed Identities File
cat <<EOF > "${CP_OUTPUT_FILE}"
{
    "cloudProvider": {
        "certificateName": "${CLOUD_PROVIDER_SP_NAME}",
        "clientID": "$(echo "$cloudProvider" | jq -r '.clientID')",
        "credentialsSecretName": "${CLOUD_PROVIDER_SP_NAME}-json",
        "objectEncoding": "utf-8"
    },
    "controlPlaneOperator": {
        "certificateName": "${CONTROL_PLANE_SP_NAME}",
        "clientID": "$(echo "$controlPlaneOperator" | jq -r '.clientID')",
        "credentialsSecretName": "${CONTROL_PLANE_SP_NAME}-json",
        "objectEncoding": "utf-8"
    },
    "disk": {
        "certificateName": "${AZURE_DISK_SP_NAME}",
        "clientID": "$(echo "$disk" | jq -r '.clientID')",
        "credentialsSecretName": "${AZURE_DISK_SP_NAME}-json",
        "objectEncoding": "utf-8"
    },
    "file": {
        "certificateName": "${AZURE_FILE_SP_NAME}",
        "clientID": "$(echo "$file" | jq -r '.clientID')",
        "credentialsSecretName": "${AZURE_FILE_SP_NAME}-json",
        "objectEncoding": "utf-8"
    },
    "imageRegistry": {
        "certificateName": "${IMAGE_REGISTRY_SP_NAME}",
        "clientID": "$(echo "$imageRegistry" | jq -r '.clientID')",
        "credentialsSecretName": "${IMAGE_REGISTRY_SP_NAME}-json",
        "objectEncoding": "utf-8"
    },
    "ingress": {
        "certificateName": "${INGRESS_SP_NAME}",
        "clientID": "$(echo "$ingress" | jq -r '.clientID')",
        "credentialsSecretName": "${INGRESS_SP_NAME}-json",
        "objectEncoding": "utf-8"
    },
    "managedIdentitiesKeyVault": {
        "name": "${KV_NAME}",
        "tenantID": "$(az account show --query tenantId -o tsv)"
    },
    "network": {
        "certificateName": "${CNCC_SP_NAME}",
        "clientID": "$(echo "$network" | jq -r '.clientID')",
        "credentialsSecretName": "${CNCC_SP_NAME}-json",
        "objectEncoding": "utf-8"
    },
    "nodePoolManagement": {
        "certificateName": "${NODEPOOL_MGMT}",
        "clientID": "$(echo "$nodePoolManagement" | jq -r '.clientID')",
        "credentialsSecretName": "${NODEPOOL_MGMT}-json",
        "objectEncoding": "utf-8"
    }
}
EOF

set +x
