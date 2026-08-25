---
title: Agent / Bare Metal Platform Guide
---

# Agent / Bare Metal Disaster Recovery Guide

This page documents Agent (Bare Metal) specific configuration, caveats, and post-restore procedures for HostedCluster disaster recovery.

For the general backup and restore procedures, see:

- [Same-cluster Restore](../scenarios/same-cluster-restore.md)
- [Cross-cluster Migration](../scenarios/cross-cluster-migration.md)

## Agent-Specific Prerequisites

In addition to the [general prerequisites](../prerequisites.md):

- The InfraEnv object must reside in a namespace **separate** from the HostedControlPlane namespace.
- The InfraEnv has a different lifecycle than the HostedCluster and must **not** be deleted during backup or restore procedures.

!!! warning "InfraEnv Lifecycle"

    The InfraEnv is essential for node reprovisioning. Agents need access to the Discovery ISO served by the InfraEnv to join or rejoin the cluster. Deleting the InfraEnv or its namespace will prevent node recovery.

## OADP DPA Configuration for Bare Metal

Bare Metal environments typically use MinIO or another S3-compatible storage.

!!! warning "Development Example"

    The example below uses HTTP and disables TLS verification (`insecureSkipTLSVerify: "true"`). For production, use HTTPS, set `insecureSkipTLSVerify` to `"false"`, and configure `objectStorage.caCert` if a custom CA is required.

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
        provider: aws
        default: true
        objectStorage:
          bucket: oadp-backup
          prefix: hcp
        config:
          region: minio
          profile: "default"
          s3ForcePathStyle: "true"
          s3Url: "http://<MINIO_ENDPOINT>:<PORT>"
          insecureSkipTLSVerify: "true"
        credential:
          key: cloud
          name: cloud-credentials
          default: true
  snapshotLocations:
    - velero:
        provider: aws
        config:
          region: minio
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
        - csi
        - hypershift
      resourceTimeout: 2h
```

## Agent-Specific Backup Resources

When creating a Velero Backup for an Agent HostedCluster, ensure the following Agent CAPI resources are included in `includedResources`:

```yaml
- bmh
- infraenv
- agents
- agentcluster
- agentmachinetemplate
- agentmachine
- nmstateconfigs
- clusterdeployment
```

See the [OADP method reference](../methods/oadp.md) for the complete backup manifest.

## CSI Compatibility

Bare Metal environments may or may not have CSI snapshot support. This affects how persistent volumes are backed up:

| Storage Configuration | Backup Configuration |
| --------------------- | --------------------- |
| **CSI snapshot-capable** (CSI driver + matching `VolumeSnapshotClass` with `velero.io/csi-volumesnapshot-class: "true"` label) | Use `snapshotMoveData: true` and `defaultVolumesToFsBackup: false` |
| **Non-CSI or no snapshot support** (e.g., NFS, CSI without `VolumeSnapshotClass`) | Use `defaultVolumesToFsBackup: true` |

## Node Readoption

Node readoption **is supported** on the Agent platform under the following conditions:

- HostedCluster OCP version **4.19 or later**.
- MCE **2.9** or ACM **2.14** or later.

When node readoption is available, existing worker nodes will rejoin the restored cluster without being reprovisioned. On older versions, nodes will be reprovisioned.

## Cross-Cluster Considerations

When performing cross-cluster migration for the Agent platform, additional objects must be backed up and restored:

1. **InfraEnv namespace and objects**: The InfraEnv and its associated resources must be available on the destination Management cluster.
2. **Assisted Installer PostgreSQL database**: The Assisted Installer database must be backed up and restored on the destination cluster.
3. **Include in backup manifest**: These resources can be included in the Velero Backup manifest to automate this process.

!!! important

    Cross-cluster migration for the Agent platform is not yet supported (no E2E coverage), but the procedure is documented here for reference.

## Restore Caveats

- **Do not delete** the InfraEnv namespace or its objects during cleanup before restore.
- **Do not remove** the Assisted Installer PostgreSQL database.
- If restoring on the same cluster, ensure agents still have network access to the Discovery ISO endpoint.
