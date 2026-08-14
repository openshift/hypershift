# Etcd Snapshot Backup and Restore for Self-Managed Azure

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

## Restoring from an Etcd Snapshot

!!! important

    Only same-management-cluster restore is validated for self-managed Azure. Cross-management-cluster restore is not currently supported due to the lack of end-to-end testing coverage for that scenario.

Restore is driven by OADP. The HyperShift OADP plugin orchestrates the full restore lifecycle:

1. OADP recreates the HostedCluster from the Velero backup
2. The plugin reads the etcd snapshot URL from the backup annotations and injects it into the restored HostedCluster's `spec.etcd.managed.storage.restoreSnapshotURL` field
3. When the restored control plane boots, the control-plane-operator detects the URL and injects an `etcd-init` init container into the etcd StatefulSet
4. The init container downloads the snapshot and restores it using `etcdutl snapshot restore`
5. Once the restore completes, the `EtcdSnapshotRestored` condition is set on the HostedControlPlane

The `restoreSnapshotURL` field is immutable — once set at HostedCluster creation time, it cannot be changed. The OADP plugin handles SAS token generation and URL injection automatically; no manual intervention is needed.

For the full restore architecture and OADP plugin behavior, see [Restore Flow Architecture](../disaster-recovery/etcd-snapshot-backup/restore-flow.md).

### Prerequisites

- OADP 1.5+ installed with the HyperShift plugin (see [OADP Setup](#oadp-setup) below)
- A completed OADP backup created with `--use-etcd-snapshot` mode
- The `HCPEtcdBackup` associated with the backup must have `BackupCompleted` condition `True`

### Step 1: Clean Up the Existing HostedCluster

Before restoring, remove the existing HostedCluster resources:

```bash
# Delete the HostedCluster and NodePools
kubectl delete hostedcluster my-hosted-cluster -n clusters

# Verify no PVCs remain in the HostedControlPlane namespace
kubectl get pvc -n clusters-my-hosted-cluster
```

### Step 2: Restore with the CLI

```bash
hypershift create oadp-restore \
    --hc-name my-hosted-cluster \
    --hc-namespace clusters \
    --from-backup <backup-name> \
    --use-etcd-snapshot
```

The `--use-etcd-snapshot` flag sets `restorePVs: false` in the Velero Restore CR. The HyperShift OADP plugin reads the snapshot URL from the backup annotations and injects it into the restored HostedCluster's `restoreSnapshotURL` field automatically.

### Step 3: Verify the Restore

Monitor the restore process:

```bash
# Watch the Velero restore status
watch "oc get restore -n openshift-adp -o jsonpath='{.items[-1].status}' | jq"

# Watch etcd pods for the init container
kubectl get pods -n <HCP_NAMESPACE> -l app=etcd -w

# Check the EtcdSnapshotRestored condition on the HostedControlPlane
kubectl get hostedcontrolplane -n <HCP_NAMESPACE> \
    -o jsonpath='{.items[0].status.conditions[?(@.type=="EtcdSnapshotRestored")]}' | jq

# Verify the hosted cluster API server becomes available
kubectl get hostedcluster my-hosted-cluster -n clusters \
    -o jsonpath='{.status.conditions[?(@.type=="Available")]}'
```

The restore is complete when:

- The Velero Restore reaches `Completed` phase
- The `etcd-init` init container exits successfully
- The `EtcdSnapshotRestored` condition is `True` on the HostedControlPlane
- The HostedCluster reaches `Available` status

### Restore Troubleshooting

| Symptom | Cause | Resolution |
|---------|-------|------------|
| `etcd-init` container shows XML error output | SAS token expired or invalid URL | Delete the HostedCluster and trigger a new OADP restore |
| `etcd-init` container shows curl 404 | Wrong blob URL | Verify the `snapshotURL` from the HCPEtcdBackup status; ensure the storage account and container still exist |
| `etcd-init` logs "not empty, not restoring snapshot" | Existing data in etcd PVC | Delete the PVC and let the restore recreate it |
| Velero restore stuck | OADP plugin error | Check `oc logs -n openshift-adp -l deploy=velero -f` for details |

## OADP Setup

OADP (OpenShift API for Data Protection) with the HyperShift plugin is required for backup and restore operations on self-managed Azure. The `hypershift create oadp-*` CLI commands auto-detect the Azure platform and include the correct CAPI resources (`azureclusters`, `azuremachinetemplates`, `azuremachines`).

### Prerequisites

- OADP 1.5+ installed on the management cluster
- DataProtectionApplication (DPA) configured with `hypershift` in `defaultPlugins`
- Azure Blob Storage credentials configured for the BackupStorageLocation

For DPA configuration details, see the [Azure DPA tab in the OADP 1.5+ guide](../disaster-recovery/backup-and-restore-oadp-1-5.md#sample-dpa-configurations).

### Creating a Backup with the CLI

Two backup modes are available:

```bash
# Volume snapshot mode (default) — backs up etcd PVs via CSI snapshots
hypershift create oadp-backup \
    --hc-name my-hosted-cluster \
    --hc-namespace clusters

# Etcd snapshot mode (Tech Preview) — uses HCPEtcdBackup CRD instead of PV snapshots
hypershift create oadp-backup \
    --hc-name my-hosted-cluster \
    --hc-namespace clusters \
    --use-etcd-snapshot
```

The `--use-etcd-snapshot` mode creates an `HCPEtcdBackup` CR and excludes PV-related resources from the Velero backup. This mode requires the `HCPEtcdBackup` feature gate and Azure Blob Storage configured as described in the [Setup](#setup) section.

### Scheduling Backups

Set up recurring backups using the OADP Schedule CR:

```bash
hypershift create oadp-schedule \
    --hc-name my-hosted-cluster \
    --hc-namespace clusters \
    --schedule "0 2 * * *" \
    --ttl 168h \
    --use-etcd-snapshot
```

### Verifying OADP Operations

```bash
# Watch backup status
watch "oc get backup -n openshift-adp -o jsonpath='{.items[-1].status}' | jq"

# Watch restore status
watch "oc get restore -n openshift-adp -o jsonpath='{.items[-1].status}' | jq"

# Follow Velero logs
oc logs -n openshift-adp -l deploy=velero -f
```

## Known Limitations

| Limitation | Details |
|-----------|---------|
| Same-cluster restore only | Cross-management-cluster restore is not currently supported due to the lack of end-to-end testing coverage for that scenario |
| OADP required for restore | Restore is driven by OADP; there is no standalone manual restore path for self-managed Azure |
| `restoreSnapshotURL` is immutable | Set by the OADP plugin at HostedCluster creation time; cannot be changed afterward |
| No in-place restore | Etcd data cannot be restored onto an existing HostedCluster; OADP recreates it from the backup |
| Worker node reprovisioning | After restore, Azure worker nodes are reprovisioned; node readoption is not supported |
| Tech Preview | The `HCPEtcdBackup` feature gate is required; this feature is not for production use |
| Single snapshot per restore | `restoreSnapshotURL` accepts at most 1 entry |

## See Also

- [Etcd Snapshot Backup Overview](../disaster-recovery/etcd-snapshot-backup/index.md) - Architecture and backup flow
- [Restore Flow Architecture](../disaster-recovery/etcd-snapshot-backup/restore-flow.md) - Detailed restore sequence and OADP plugin behavior
- [Managed Services Credentials](../disaster-recovery/etcd-snapshot-backup/managed-services-credentials.md) - Credential auto-detection and formats
- [OADP 1.5+ Disaster Recovery](../disaster-recovery/backup-and-restore-oadp-1-5.md) - Full OADP DR procedure with Azure DPA configuration
- [Disaster Recovery CLI](../disaster-recovery/dr-cli.md) - CLI reference for `hypershift create oadp-*` commands
- [Self-Managed Azure Overview](self-managed-azure-index.md) - Self-managed Azure architecture
