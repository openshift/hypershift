#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/vars.sh"

echo "Setting up etcd backup infrastructure for self-managed Azure..."

# Verify required variables
BACKUP_STORAGE_ACCOUNT_NAME=${BACKUP_STORAGE_ACCOUNT_NAME:?"Provide backup storage account name"}
BACKUP_CONTAINER_NAME=${BACKUP_CONTAINER_NAME:?"Provide backup container name"}
BACKUP_MI_NAME=${BACKUP_MI_NAME:?"Provide backup managed identity name"}

# Verify required files exist
if [[ ! -f "${AZURE_CREDS}" ]]; then
    echo "Error: Azure credentials file not found at ${AZURE_CREDS}"
    exit 1
fi

# Determine management cluster OIDC issuer URL
if [[ -n "${MGMT_OIDC_ISSUER_URL:-}" ]]; then
    echo "Using provided management cluster OIDC issuer URL: ${MGMT_OIDC_ISSUER_URL}"
elif [[ -n "${AKS_CLUSTER_NAME:-}" && -n "${AKS_RG:-}" ]]; then
    echo "Detecting OIDC issuer URL from AKS cluster ${AKS_CLUSTER_NAME}..."
    MGMT_OIDC_ISSUER_URL=$(az aks show \
        --name "${AKS_CLUSTER_NAME}" \
        --resource-group "${AKS_RG}" \
        --query oidcIssuerProfile.issuerUrl -o tsv)
    if [[ -z "${MGMT_OIDC_ISSUER_URL}" ]]; then
        echo "Error: AKS cluster ${AKS_CLUSTER_NAME} does not have OIDC issuer enabled."
        echo "Re-create the AKS cluster with --enable-oidc-issuer --enable-workload-identity"
        exit 1
    fi
    echo "Detected OIDC issuer URL: ${MGMT_OIDC_ISSUER_URL}"
else
    echo "Error: Cannot determine management cluster OIDC issuer URL."
    echo "Set MGMT_OIDC_ISSUER_URL or provide AKS_CLUSTER_NAME and AKS_RG."
    exit 1
fi

# Create storage account for etcd backups
echo "Creating storage account: ${BACKUP_STORAGE_ACCOUNT_NAME}"
if ! az storage account show --name "${BACKUP_STORAGE_ACCOUNT_NAME}" --resource-group "${PERSISTENT_RG_NAME}" >/dev/null 2>&1; then
    az storage account create \
        --name "${BACKUP_STORAGE_ACCOUNT_NAME}" \
        --resource-group "${PERSISTENT_RG_NAME}" \
        --location "${LOCATION}" \
        --sku Standard_LRS \
        --kind StorageV2 \
        --min-tls-version TLS1_2 \
        --allow-blob-public-access false
    echo "Storage account created"
else
    echo "Storage account ${BACKUP_STORAGE_ACCOUNT_NAME} already exists"
fi

# Create blob container for etcd snapshots
echo "Creating blob container: ${BACKUP_CONTAINER_NAME}"
az storage container create \
    --name "${BACKUP_CONTAINER_NAME}" \
    --account-name "${BACKUP_STORAGE_ACCOUNT_NAME}" \
    --auth-mode login \
    2>/dev/null || echo "Container ${BACKUP_CONTAINER_NAME} already exists"

# Create managed identity for etcd backup
echo "Creating managed identity: ${BACKUP_MI_NAME}"
if ! az identity show --name "${BACKUP_MI_NAME}" --resource-group "${PERSISTENT_RG_NAME}" >/dev/null 2>&1; then
    az identity create \
        --name "${BACKUP_MI_NAME}" \
        --resource-group "${PERSISTENT_RG_NAME}" \
        --location "${LOCATION}"
    echo "Managed identity created"
else
    echo "Managed identity ${BACKUP_MI_NAME} already exists"
fi

BACKUP_MI_CLIENT_ID=$(az identity show \
    --name "${BACKUP_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --query clientId -o tsv)
BACKUP_MI_PRINCIPAL_ID=$(az identity show \
    --name "${BACKUP_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --query principalId -o tsv)

echo "Backup MI Client ID: ${BACKUP_MI_CLIENT_ID}"

# Create federated credential for etcd-backup-job ServiceAccount
BACKUP_FED_CRED_NAME="${PREFIX}-etcd-backup-fed-cred"
HO_NAMESPACE="${HO_NAMESPACE:-hypershift}"
echo "Creating federated credential: ${BACKUP_FED_CRED_NAME}"
if ! az identity federated-credential show \
    --name "${BACKUP_FED_CRED_NAME}" \
    --identity-name "${BACKUP_MI_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" >/dev/null 2>&1; then
    az identity federated-credential create \
        --name "${BACKUP_FED_CRED_NAME}" \
        --identity-name "${BACKUP_MI_NAME}" \
        --resource-group "${PERSISTENT_RG_NAME}" \
        --issuer "${MGMT_OIDC_ISSUER_URL}" \
        --subject "system:serviceaccount:${HO_NAMESPACE}:etcd-backup-job" \
        --audiences "api://AzureADTokenExchange"
    echo "Federated credential created"
else
    echo "Federated credential ${BACKUP_FED_CRED_NAME} already exists"
fi

# Assign Storage Blob Data Contributor role on the storage account
STORAGE_ACCOUNT_ID=$(az storage account show \
    --name "${BACKUP_STORAGE_ACCOUNT_NAME}" \
    --resource-group "${PERSISTENT_RG_NAME}" \
    --query id -o tsv)

echo "Assigning Storage Blob Data Contributor role..."
az role assignment create \
    --assignee-object-id "${BACKUP_MI_PRINCIPAL_ID}" \
    --assignee-principal-type ServicePrincipal \
    --role "Storage Blob Data Contributor" \
    --scope "${STORAGE_ACCOUNT_ID}" \
    2>/dev/null || echo "Role assignment already exists"

# Create credential Secret in the HO namespace
echo "Creating credential Secret in ${HO_NAMESPACE} namespace..."
BACKUP_SECRET_NAME="${BACKUP_SECRET_NAME:-etcd-backup-azure-credentials}"

kubectl delete secret "${BACKUP_SECRET_NAME}" -n "${HO_NAMESPACE}" 2>/dev/null || true
kubectl create secret generic "${BACKUP_SECRET_NAME}" \
    --namespace "${HO_NAMESPACE}" \
    --from-literal=cloud="AZURE_SUBSCRIPTION_ID=${SUBSCRIPTION_ID}
AZURE_TENANT_ID=${TENANT_ID}
AZURE_CLIENT_ID=${BACKUP_MI_CLIENT_ID}
AZURE_RESOURCE_GROUP=${PERSISTENT_RG_NAME}
AZURE_CLOUD_NAME=AzurePublicCloud"

echo ""
echo "=== Etcd backup infrastructure setup complete ==="
echo ""
echo "Storage Account:  ${BACKUP_STORAGE_ACCOUNT_NAME}"
echo "Container:        ${BACKUP_CONTAINER_NAME}"
echo "Managed Identity: ${BACKUP_MI_NAME} (Client ID: ${BACKUP_MI_CLIENT_ID})"
echo "Secret:           ${BACKUP_SECRET_NAME} (namespace: ${HO_NAMESPACE})"
echo "OIDC Issuer:      ${MGMT_OIDC_ISSUER_URL}"
echo ""
echo "To create a backup, apply an HCPEtcdBackup CR:"
echo ""
echo "  apiVersion: hypershift.openshift.io/v1beta1"
echo "  kind: HCPEtcdBackup"
echo "  metadata:"
echo "    name: my-backup"
echo "    namespace: <HCP_NAMESPACE>"
echo "  spec:"
echo "    storage:"
echo "      storageType: AzureBlob"
echo "      azureBlob:"
echo "        container: ${BACKUP_CONTAINER_NAME}"
echo "        storageAccount: ${BACKUP_STORAGE_ACCOUNT_NAME}"
echo "        keyPrefix: etcd-backups"
echo "        credentials:"
echo "          name: ${BACKUP_SECRET_NAME}"
