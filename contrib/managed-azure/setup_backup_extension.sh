#!/bin/bash
set -x

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

source "${SCRIPT_DIR}/vars.sh"

# Prerequisites.
AKS_RG="${PREFIX}-aks-rg"
AKS_BACKUP_VAULT_NAME="${PREFIX}-backup-vault"
AKS_STORAGE_BLOB_CONTAINER="${PREFIX}-velero"
# Azure storage account names must be between 3 and 24 characters in length and may contain numbers and lowercase letters only.
AKS_STORAGE_ACCOUNT="$(echo "${PREFIX}" | tr -d '_-')"
AKS_BACKUP_POLICY_NAME="${PREFIX}-backup-policy"
AKS_BACKUP_POLICY_JSON="${PREFIX}-backup-policy.json"
AKS_CLUSTER_DR_ROLEBINDING_NAME="$(echo "${PREFIX}" | tr -d '_-')rb"

function setup_storage_account() {
    az storage account create \
        --name $AKS_STORAGE_ACCOUNT \
        --resource-group $AKS_RG \
        --location $LOCATION \
        --sku Standard_RAGRS \
        --kind StorageV2 \
        --min-tls-version TLS1_2 \
        --allow-blob-public-access false
}

function setup_backup_requirements() {
    ## Create Vault
    az dataprotection backup-vault create \
        --resource-group $AKS_RG \
        --vault-name $AKS_BACKUP_VAULT_NAME \
        --location $LOCATION \
        --type SystemAssigned \
        --storage-settings datastore-type="VaultStore" type="LocallyRedundant"

    # Create backup policy JSON
    az dataprotection backup-policy get-default-policy-template \
        --datasource-type AzureKubernetesService \
        --output json > $AKS_BACKUP_POLICY_JSON

    ## Create Backup policy
    az dataprotection backup-policy create \
        -g $AKS_RG \
        --vault-name $AKS_BACKUP_VAULT_NAME \
        -n $AKS_BACKUP_POLICY_NAME \
        --policy $AKS_BACKUP_POLICY_JSON
}

function register_backup_extension() {
    az provider register --namespace Microsoft.KubernetesConfiguration
    sleep 10
    az provider show --name Microsoft.KubernetesConfiguration --output table
}

function install_backup_extension() {
    az k8s-extension create \
        --name azure-aks-backup \
        --extension-type microsoft.dataprotection.kubernetes \
        --scope cluster \
        --cluster-type managedClusters \
        --cluster-name $AKS_CLUSTER_NAME \
        --resource-group $AKS_RG \
        --release-train stable \
        --configuration-settings blobContainer=$AKS_STORAGE_BLOB_CONTAINER storageAccount=$AKS_STORAGE_ACCOUNT storageAccountResourceGroup=$AKS_RG storageAccountSubscriptionId=$SUBSCRIPTION_ID

    ## Create Role
    az role assignment create \
        --assignee-object-id $(az k8s-extension show --name azure-aks-backup --cluster-name $AKS_CLUSTER_NAME --resource-group $AKS_RG --cluster-type managedClusters --query aksAssignedIdentity.principalId --output tsv) \
        --role 'Storage Blob Data Contributor' \
        --scope /subscriptions/$SUBSCRIPTION_ID/resourceGroups/$AKS_RG/providers/Microsoft.Storage/storageAccounts/$AKS_STORAGE_ACCOUNT

    ## Trust role from/to AKS cluster
    az aks trustedaccess rolebinding create \
        --resource-group $AKS_RG \
        --cluster-name $AKS_CLUSTER_NAME \
        --name $AKS_CLUSTER_DR_ROLEBINDING_NAME \
        --source-resource-id $(az dataprotection backup-vault show --resource-group $AKS_RG --vault $AKS_BACKUP_VAULT_NAME --query id -o tsv) \
        --roles Microsoft.DataProtection/backupVaults/backup-operator

}


# Flag handling
SETUP_BACKUP_REQUIREMENTS=false
DEPLOY_BACKUP_EXTENSION=false

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --setup-backup-requirements)
            SETUP_BACKUP_REQUIREMENTS=true
            shift 1
            ;;
        --deploy-backup-extension)
            DEPLOY_BACKUP_EXTENSION=true
            shift 1
            ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo "Options:"
            echo "  --setup-backup-requirements     Setup backup requirements (vault, policy, roles)"
            echo "  --deploy-backup-extension       Deploy backup extension to AKS cluster"
            echo "  -h, --help                      Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0 --setup-backup-requirements"
            echo "  $0 --deploy-backup-extension"
            echo "  $0 --setup-backup-requirements --deploy-backup-extension"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use -h or --help for usage information"
            exit 1
            ;;
    esac
done

# Check if at least one flag is provided
if [[ "$SETUP_BACKUP_REQUIREMENTS" == "false" && "$DEPLOY_BACKUP_EXTENSION" == "false" ]]; then
    echo "Error: At least one flag must be provided."
    echo "Use -h or --help for usage information"
    exit 1
fi

# Execute in order: setup first, then deploy
if [[ "$SETUP_BACKUP_REQUIREMENTS" == "true" ]]; then
    echo "Setting up backup requirements..."
    setup_storage_account
    setup_backup_requirements
    register_backup_extension
fi

if [[ "$DEPLOY_BACKUP_EXTENSION" == "true" ]]; then
    echo "Deploying backup extension..."
    install_backup_extension
fi