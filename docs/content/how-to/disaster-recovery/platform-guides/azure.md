---
title: Azure Platform Guide
---

# Azure Disaster Recovery Guide

This page documents Azure-specific configuration, caveats, and post-restore procedures for HostedCluster disaster recovery.

For the general backup and restore procedures, see:

- [Same-cluster Restore](../scenarios/same-cluster-restore.md)
- [Cross-cluster Migration](../scenarios/cross-cluster-migration.md)

## Azure-Specific Prerequisites

In addition to the [general prerequisites](../prerequisites.md):

- Azure Storage Account and Blob Container for OADP backup storage.
- Azure credentials configured for the OADP Operator.

### Azure Credentials for OADP

```bash
cat << EOF > ./credentials-azure
[default]
AZURE_SUBSCRIPTION_ID=<subscription-id>
AZURE_TENANT_ID=<tenant-id>
AZURE_CLIENT_ID=<client-id>
AZURE_CLIENT_SECRET=<client-secret>
AZURE_RESOURCE_GROUP=<resource-group>
AZURE_CLOUD_NAME=AzurePublicCloud
EOF

oc create secret generic cloud-credentials -n openshift-adp --from-file cloud=credentials-azure
```

For detailed Azure OADP setup, follow the [Azure OADP installation guide](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/installing/installing-oadp-azure.html).

## OADP DPA Configuration for Azure

```yaml
apiVersion: oadp.openshift.io/v1alpha1
kind: DataProtectionApplication
metadata:
  name: dpa-instance
  namespace: openshift-adp
spec:
  backupLocations:
    - name: default
      velero:
        provider: azure
        default: true
        objectStorage:
          bucket: <blob_container_name>
          prefix: hcp
        config:
          resourceGroup: <resource_group>
          storageAccount: <storage_account_name>
        credential:
          key: cloud
          name: cloud-credentials
  configuration:
    nodeAgent:
      enable: true
      uploaderType: kopia
    velero:
      defaultPlugins:
        - openshift
        - azure
        - csi
        - hypershift
      resourceTimeout: 2h
```

## Azure-Specific Backup Resources

When creating a Velero Backup for an Azure HostedCluster, ensure the following Azure CAPI resources are included in `includedResources`:

```yaml
- azureclusters
- azuremachinetemplates
- azuremachines
```

See the [OADP method reference](../methods/oadp.md) for the complete backup manifest.

## Etcd Snapshot Backup with Azure Blob Storage

Azure supports the Etcd Snapshot Backup method (Tech Preview) using Azure Blob Storage. Three authentication modes are supported:

| Mode | When to Use | Secret Format |
| ------ | ------------- | --------------- |
| **Workload Identity** | Production (recommended) | `cloud` key with `AZURE_CLIENT_ID=...` |
| **Client Secret** | Development/testing | `credentials` key with JSON (`clientId`/`clientSecret`/`tenantId`) |
| **Managed Identity** | ARO HCP only | Certificate-based via CSI mount |

For detailed setup instructions including Workload Identity federation configuration, see the [Managed Services Credentials](../methods/etcd-snapshot/managed-services-credentials.md) page.

## Restore Caveats

- For etcd snapshot backup and restore specific to self-managed Azure, the controller auto-detects the credential mode from the Secret format — no explicit configuration flag is needed.

For supportability details per platform and scenario, see the [Supportability Matrix](../index.md#supportability-matrix).
