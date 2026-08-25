---
title: OpenStack Platform Guide
---

# OpenStack Disaster Recovery Guide

This page documents OpenStack-specific configuration, caveats, and post-restore procedures for HostedCluster disaster recovery.

For the general backup and restore procedures, see:

- [Same-cluster Restore](../scenarios/same-cluster-restore.md)
- [Cross-cluster Migration](../scenarios/cross-cluster-migration.md)

## OpenStack-Specific Prerequisites

In addition to the [general prerequisites](../prerequisites.md):

- S3-compatible object storage configured for OADP backup (e.g., Ceph Object Gateway, MinIO, or external S3).
- CSI driver with snapshot support for volume-based backups.

## OADP DPA Configuration for OpenStack

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
          bucket: example-oadp
          prefix: backup-objects
        config:
          region: region-one
          profile: "default"
        credential:
          key: cloud
          name: cloud-credentials
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

## OpenStack-Specific Backup Resources

When creating a Velero Backup for an OpenStack HostedCluster, ensure the following OpenStack CAPI resources are included in `includedResources`:

```yaml
- openstackclusters
- openstackmachinetemplates
- openstackmachines
- openstackfloatingippools
```

See the [OADP method reference](../methods/oadp.md) for the complete backup manifest.

## Restore Caveats

- **Node readoption**: Not supported. Worker nodes will be reprovisioned during restore.
- **Etcd Snapshot method**: Not validated for the OpenStack platform.
- **Floating IP pools**: Floating IP pool resources are backed up and restored. Ensure the floating IP pool configuration on the destination environment matches the source.
- **Cross-cluster migration**: Not yet supported (no E2E coverage). The procedure is documented for reference.
