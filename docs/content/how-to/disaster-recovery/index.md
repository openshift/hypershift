---
title: Backup and Restore on HostedControlPlanes
---

This section of the Hypershift documentation contains pages that show how to perform disaster recovery tasks using different methods.

## Available Guides

### [Prerequisites](prerequisites.md)
Required prerequisites for all disaster recovery operations, including HostedCluster service publishing strategy requirements for cross-management-cluster restore.

### [DR CLI Domain](dr-cli.md)
Use the HyperShift CLI disaster recovery commands with platform-aware backup creation and OADP integration.

### [Backup and Restore with OADP](backup-and-restore-oadp.md)
Manual backup and restore procedures using OADP (OpenShift API for Data Protection) with Velero.

### [Backup and Restore with OADP 1.5](backup-and-restore-oadp-1-5.md)
Updated procedures and enhanced features for OADP version 1.5.

### [ETCD Recovery](etcd-recovery.md)
ETCD disaster recovery procedures for control plane data backup and restoration.

### [Etcd Snapshot Backup (Tech Preview)](etcd-snapshot-backup/index.md)
Alternative backup method using native etcd snapshots instead of volume snapshots. Requires the `HCPEtcdBackup` feature gate. Includes detailed backup and restore flow documentation.
