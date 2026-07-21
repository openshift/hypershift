# Etcd Snapshot Backup for Self-Managed Azure

!!! warning "Tech Preview"

    This feature requires the `HCPEtcdBackup` feature gate enabled in the HyperShift Operator.

    **Standalone HyperShift Operator**: pass `--feature-gate HCPEtcdBackup=true` to the `hypershift install` command.

    **MCE/ACM-managed HyperShift Operator**: use the `hypershift-operator-install-flags` ConfigMap to pass the feature gate flag. See [Overriding HyperShift Operator Install Flags](../../recipes/common/acm-mce-hypershift-operator-overrides.md#overriding-hypershift-operator-install-flags) for details.

    **Verify the feature gate is active** by checking that the `HCPEtcdBackup` CRD exists (it is only installed when the feature gate is enabled):

    ```bash
    kubectl get crd hcpetcdbackups.hypershift.openshift.io
    ```

    If the CRD is not found, the feature gate is not active.

This guide describes how to set up and use etcd snapshot backups with Azure Blob Storage for self-managed Azure HCP environments using Azure Workload Identity.

## Overview

The `HCPEtcdBackup` controller automatically detects the authentication mode from the credential Secret. For self-managed Azure, three authentication modes are supported:

| Mode | When to Use | Secret Format |
|------|-------------|---------------|
| **Workload Identity** | Production (recommended) | `cloud` key with `AZURE_CLIENT_ID=...` |
| **Client Secret** | Development/testing | `credentials` key with JSON (`clientId`/`clientSecret`/`tenantId`) |
| **Managed Identity** | ARO HCP only | Certificate-based via CSI mount |

This guide covers the **Workload Identity** setup. For credential format details and auto-detection logic, see [Managed Services Credentials](../disaster-recovery/etcd-snapshot-backup/managed-services-credentials.md).

## Prerequisites

- A self-managed Azure management cluster with HyperShift Operator installed
- Azure CLI (`az`) configured
- `kubectl` or `oc` CLI
- The management cluster must have an OIDC issuer configured:
    - **AKS**: Created with `--enable-oidc-issuer --enable-workload-identity`
    - **OCP on Azure**: Configured via Cloud Credential Operator with Workload Identity

## Setup

### Step 1: Create a Storage Account and Container

Create an Azure Storage Account and blob container for etcd snapshots:

```bash
PERSISTENT_RG_NAME="os4-common"   # Your persistent resource group
LOCATION="eastus"
BACKUP_STORAGE_ACCOUNT="mybackupstorage"  # 3-24 chars, lowercase alphanumeric only
BACKUP_CONTAINER="etcd-backups"

# Create storage account
az storage account create \
    --name $BACKUP_STORAGE_ACCOUNT \
    --resource-group $PERSISTENT_RG_NAME \
    --location $LOCATION \
    --sku Standard_LRS \
    --kind StorageV2 \
    --min-tls-version TLS1_2 \
    --allow-blob-public-access false

# Create blob container
# Note: --auth-mode login requires the caller to have Storage Blob Data Contributor
# on the storage account. If you get a 403, assign the role to your user first or
# omit --auth-mode to use storage account keys.
az storage container create \
    --name $BACKUP_CONTAINER \
    --account-name $BACKUP_STORAGE_ACCOUNT \
    --auth-mode login
```

### Step 2: Create a Managed Identity

Create a User-Assigned Managed Identity for the backup Job:

```bash
BACKUP_MI_NAME="etcd-backup-mi"

az identity create \
    --name $BACKUP_MI_NAME \
    --resource-group $PERSISTENT_RG_NAME \
    --location $LOCATION

# Save the client ID for later
BACKUP_MI_CLIENT_ID=$(az identity show \
    --name $BACKUP_MI_NAME \
    --resource-group $PERSISTENT_RG_NAME \
    --query clientId -o tsv)
```

### Step 3: Create a Federated Credential

Create a federated credential that trusts the `etcd-backup-job` ServiceAccount on the management cluster:

```bash
HO_NAMESPACE="hypershift"  # HyperShift Operator namespace

# Get the management cluster's OIDC issuer URL
# For AKS: if you used contrib/self-managed-azure/setup_all.sh, these are already
# exported. Otherwise, set them to your AKS cluster name and resource group.
AKS_CLUSTER_NAME="${AKS_CLUSTER_NAME:-<your-aks-cluster-name>}"
AKS_RG="${AKS_RG:-<your-aks-resource-group>}"

MGMT_OIDC_ISSUER=$(az aks show \
    --name $AKS_CLUSTER_NAME \
    --resource-group $AKS_RG \
    --query oidcIssuerProfile.issuerUrl -o tsv)

# Create federated credential
az identity federated-credential create \
    --name etcd-backup-fedcred \
    --identity-name $BACKUP_MI_NAME \
    --resource-group $PERSISTENT_RG_NAME \
    --issuer "$MGMT_OIDC_ISSUER" \
    --subject "system:serviceaccount:${HO_NAMESPACE}:etcd-backup-job" \
    --audiences "api://AzureADTokenExchange"
```

!!! important

    The `--issuer` must be the **management cluster's** OIDC issuer URL, not the hosted cluster's OIDC issuer. The backup Job runs on the management cluster.

### Step 4: Assign Storage Permissions

Grant the managed identity `Storage Blob Data Contributor` role on the storage account:

```bash
BACKUP_MI_PRINCIPAL_ID=$(az identity show \
    --name $BACKUP_MI_NAME \
    --resource-group $PERSISTENT_RG_NAME \
    --query principalId -o tsv)

STORAGE_ACCOUNT_ID=$(az storage account show \
    --name $BACKUP_STORAGE_ACCOUNT \
    --resource-group $PERSISTENT_RG_NAME \
    --query id -o tsv)

az role assignment create \
    --assignee-object-id $BACKUP_MI_PRINCIPAL_ID \
    --assignee-principal-type ServicePrincipal \
    --role "Storage Blob Data Contributor" \
    --scope $STORAGE_ACCOUNT_ID
```

### Step 5: Create the Credential Secret

Create the credential Secret in the HyperShift Operator namespace:

```bash
SUBSCRIPTION_ID=$(az account show --query id -o tsv)
TENANT_ID=$(az account show --query tenantId -o tsv)

kubectl create secret generic etcd-backup-azure-credentials \
    --namespace $HO_NAMESPACE \
    --from-literal=cloud="AZURE_SUBSCRIPTION_ID=${SUBSCRIPTION_ID}
AZURE_TENANT_ID=${TENANT_ID}
AZURE_CLIENT_ID=${BACKUP_MI_CLIENT_ID}
AZURE_RESOURCE_GROUP=${PERSISTENT_RG_NAME}
AZURE_CLOUD_NAME=AzurePublicCloud"
```

The controller auto-detects Workload Identity mode when the Secret has a `cloud` key containing `AZURE_CLIENT_ID=...`.

## Creating a Backup

Create an `HCPEtcdBackup` CR in the HostedControlPlane namespace:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HCPEtcdBackup
metadata:
  name: my-backup
  namespace: <HCP_NAMESPACE>
spec:
  storage:
    storageType: AzureBlob
    azureBlob:
      container: etcd-backups
      storageAccount: mybackupstorage
      keyPrefix: etcd-backups
      credentials:
        name: etcd-backup-azure-credentials
```

Replace `<HCP_NAMESPACE>` with the namespace of your HostedControlPlane (e.g., `clusters-my-cluster`).

## Verification

Monitor the backup progress:

```bash
# Check HCPEtcdBackup status
kubectl get hcpetcdbackup my-backup -n <HCP_NAMESPACE> -o yaml

# Watch for the backup Job
kubectl get jobs -n $HO_NAMESPACE -l app=etcd-backup

# Check Job logs
kubectl logs -n $HO_NAMESPACE -l app=etcd-backup --tail=50
```

When the backup completes, the `HCPEtcdBackup` status will show:

- `BackupCompleted` condition set to `True`
- `snapshotURL` containing the Azure Blob URL of the snapshot

## Using Client Secret Mode (Alternative)

For development and testing, you can use a Service Principal with client secret instead of Workload Identity:

```bash
# Create (or update) the credential Secret with JSON format
kubectl create secret generic etcd-backup-azure-credentials \
    --namespace $HO_NAMESPACE \
    --from-literal=credentials='{
  "subscriptionId": "'$SUBSCRIPTION_ID'",
  "tenantId": "'$TENANT_ID'",
  "clientId": "'$CLIENT_ID'",
  "clientSecret": "'$CLIENT_SECRET'"
}' \
    --dry-run=client -o yaml | kubectl apply -f -
```

The Service Principal needs `Storage Blob Data Contributor` role on the storage account.

## Automation with contrib Scripts

The `contrib/self-managed-azure/setup_backup.sh` script automates Steps 1-5:

```bash
cd dev/
source user-vars.sh
source ../contrib/self-managed-azure/vars.sh
../contrib/self-managed-azure/setup_backup.sh
```

Or include it in the full setup:

```bash
../contrib/self-managed-azure/setup_all.sh --setup-backup
```

## Cleanup

To remove backup infrastructure:

```bash
# Delete the credential Secret
kubectl delete secret etcd-backup-azure-credentials -n $HO_NAMESPACE

# Delete the managed identity (also removes federated credentials)
az identity delete \
    --name $BACKUP_MI_NAME \
    --resource-group $PERSISTENT_RG_NAME

# Delete the storage account and container
az storage account delete \
    --name $BACKUP_STORAGE_ACCOUNT \
    --resource-group $PERSISTENT_RG_NAME \
    --yes
```

## See Also

- [Etcd Snapshot Backup Overview](../disaster-recovery/etcd-snapshot-backup/index.md) - Architecture and backup flow
- [Managed Services Credentials](../disaster-recovery/etcd-snapshot-backup/managed-services-credentials.md) - Credential auto-detection and formats
- [Self-Managed Azure Overview](self-managed-azure-index.md) - Self-managed Azure architecture
