---
title: KubeVirt Platform Guide
---

# KubeVirt Disaster Recovery Guide

This page documents KubeVirt-specific configuration, caveats, and post-restore procedures for HostedCluster disaster recovery.

For the general backup and restore procedures, see:

- [Same-cluster Restore](../scenarios/same-cluster-restore.md)
- [Cross-cluster Migration](../scenarios/cross-cluster-migration.md)

## KubeVirt-Specific Considerations

- **Same-cluster restore only**: Cross-cluster migration is not validated for the KubeVirt platform.
- **No pause required**: Backup of a KubeVirt HostedCluster can be performed on a running cluster without pausing it.
- **VMs are recreated**: KubeVirt VMs used as worker nodes are **not** backed up. They are automatically recreated as new VMs after restore.
- **Boot image PVCs excluded**: The boot images for KubeVirt VMs are stored in large PVCs. These are excluded from the backup to reduce backup time and storage size.

## OADP DPA Configuration for KubeVirt

KubeVirt environments typically use MinIO or another S3-compatible storage:

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
        config:
          region: minio
          profile: "default"
          s3ForcePathStyle: 'true'
          s3Url: 'http://<MINIO_ENDPOINT>:<PORT>'
        credential:
          key: cloud
          name: cloud-credentials
        default: true
        objectStorage:
          bucket: example-oadp
          prefix: backup-objects
        provider: aws
  snapshotLocations:
    - velero:
        provider: aws
        config:
          region: region-one
          profile: "default"
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
        - aws
        - kubevirt
        - csi
        - hypershift
      resourceTimeout: 2h
```

## KubeVirt-Specific Backup Resources

When creating a Velero Backup for a KubeVirt HostedCluster, include the following KubeVirt-specific resources:

```yaml
- kubevirtcluster
- kubevirtmachinetemplate
- datavolume
```

### Boot Image PVC Filtering

The boot image PVCs for KubeVirt VMs are very large and unnecessary for backup (VMs are recreated). Filter them out using a label selector:

```yaml
labelSelector:
  matchExpressions:
  - key: 'hypershift.openshift.io/is-kubevirt-rhcos'
    operator: 'DoesNotExist'
```

### Additional Backup Options

```yaml
# Preserve NodePort assignments during restore
preserveNodePorts: true
```

## Restore Caveats

- **Same-cluster only**: Restore must be performed on the same Management cluster where the backup was created.
- **Node readoption**: Not supported. KubeVirt VMs are automatically recreated as new VMs after restore.
- **Etcd Snapshot method**: Not validated for the KubeVirt platform.
- **Data Plane PVCs**: PVCs for hosted cluster workloads (non-boot-image) are included in the backup and will be restored.
