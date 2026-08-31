---
title: OADP Backup and Restore
---

# Backup and Restore with OADP

The OpenShift API for Data Protection (OADP) with the HyperShift plugin is the primary method for backing up and restoring HostedClusters. This page serves as a unified reference for OADP-based DR procedures across all platforms and OADP versions.

!!! tip "Which OADP version?"

    - **OADP 1.5+** includes a built-in `hypershift` plugin — use the `hypershift` default plugin in the DPA.
    - **OADP < 1.5** requires a custom plugin image. If you cannot deploy OADP 1.5+, use the `customPlugins` section in the DPA.

    This guide covers both versions. The procedures are identical except for the DPA plugin configuration.

## Prerequisites

Review the [Disaster Recovery Prerequisites](../prerequisites.md) before proceeding. Key requirements:

- OADP Operator installed on the Management cluster.
- DataProtectionApplication (DPA) configured with backup and snapshot storage locations.
- Cloud credentials secret created in the `openshift-adp` namespace.

For platform-specific DPA configurations and credentials, see the platform guides:

- [AWS](../platform-guides/aws.md#oadp-dpa-configuration-for-aws)
- [Azure](../platform-guides/azure.md#oadp-dpa-configuration-for-azure)
- [Agent / Bare Metal](../platform-guides/agent.md#oadp-dpa-configuration-for-bare-metal)
- [KubeVirt](../platform-guides/kubevirt.md#oadp-dpa-configuration-for-kubevirt)
- [OpenStack](../platform-guides/openstack.md#oadp-dpa-configuration-for-openstack)

## OADP Plugin Configuration

=== "**OADP 1.5+**"

    Use the built-in `hypershift` plugin:

    ```yaml
    velero:
      defaultPlugins:
        - openshift
        - aws      # or azure, depending on provider
        - csi
        - hypershift
    ```

=== "**OADP < 1.5**"

    Use the custom plugin image:

    ```yaml
    velero:
      defaultPlugins:
        - openshift
        - aws      # or azure, depending on provider
        - csi
      customPlugins:
        - name: hypershift-oadp-plugin
          image: quay.io/redhat-user-workloads/ocp-art-tenant/oadp-hypershift-oadp-plugin-main:main
    ```

## Creating a Backup

### Using the HyperShift CLI (Recommended)

The simplest way to create a backup:

```bash
hypershift create oadp-backup \
  --hc-name <HC_NAME> \
  --hc-namespace <HC_NAMESPACE>
```

See the [DR CLI reference](../dr-cli.md) for all available options including `--use-etcd-snapshot`, `--ttl`, and `--snapshot-move-data`.

### Using a Manual Velero Backup Manifest

The backup manifest varies by platform due to different CAPI resources. Select your platform:

=== "**AWS**"

    ```yaml
    apiVersion: velero.io/v1
    kind: Backup
    metadata:
      name: hc-backup
      namespace: openshift-adp
      labels:
        velero.io/storage-location: default
    spec:
      includedNamespaces:
      - <HC_NAMESPACE>
      - <HC_NAMESPACE>-<HC_NAME>
      includedResources:
      - sa
      - role
      - rolebinding
      - pod
      - pvc
      - pv
      - configmap
      - priorityclasses
      - pdb
      - hostedcluster
      - nodepool
      - secrets
      - services
      - deployments
      - statefulsets
      - hostedcontrolplane
      - cluster
      - awscluster
      - awsmachinetemplate
      - awsmachine
      - machinedeployment
      - machineset
      - machine
      - route
      - clusterdeployment
      excludedResources: []
      storageLocation: default
      ttl: 2h30m0s
      snapshotMoveData: true
      datamover: "velero"
      defaultVolumesToFsBackup: false
      snapshotVolumes: true
    ```

=== "**Azure**"

    ```yaml
    apiVersion: velero.io/v1
    kind: Backup
    metadata:
      name: hc-backup
      namespace: openshift-adp
      labels:
        velero.io/storage-location: default
    spec:
      includedNamespaces:
      - <HC_NAMESPACE>
      - <HC_NAMESPACE>-<HC_NAME>
      includedResources:
      - sa
      - role
      - rolebinding
      - pod
      - pvc
      - pv
      - configmap
      - priorityclasses
      - pdb
      - hostedcluster
      - nodepool
      - secrets
      - services
      - deployments
      - statefulsets
      - hostedcontrolplane
      - cluster
      - azureclusters
      - azuremachinetemplates
      - azuremachines
      - machinedeployment
      - machineset
      - machine
      - route
      - clusterdeployment
      excludedResources: []
      storageLocation: default
      ttl: 2h30m0s
      snapshotMoveData: true
      datamover: "velero"
      defaultVolumesToFsBackup: false
      snapshotVolumes: true
    ```

=== "**Agent / Bare Metal (CSI)**"

    ```yaml
    apiVersion: velero.io/v1
    kind: Backup
    metadata:
      name: hc-backup
      namespace: openshift-adp
      labels:
        velero.io/storage-location: default
    spec:
      includedNamespaces:
      - <HC_NAMESPACE>
      - <HC_NAMESPACE>-<HC_NAME>
      includedResources:
      - sa
      - role
      - rolebinding
      - pod
      - pvc
      - pv
      - bmh
      - configmap
      - infraenv
      - priorityclasses
      - pdb
      - agents
      - hostedcluster
      - nodepool
      - secrets
      - services
      - deployments
      - statefulsets
      - hostedcontrolplane
      - cluster
      - agentcluster
      - agentmachinetemplate
      - agentmachine
      - nmstateconfigs
      - machinedeployment
      - machineset
      - machine
      - route
      - clusterdeployment
      excludedResources: []
      storageLocation: default
      ttl: 2h30m0s
      snapshotMoveData: true
      datamover: "velero"
      defaultVolumesToFsBackup: false
      snapshotVolumes: true
    ```

=== "**Agent / Bare Metal (Non-CSI)**"

    ```yaml
    apiVersion: velero.io/v1
    kind: Backup
    metadata:
      name: hc-backup
      namespace: openshift-adp
      labels:
        velero.io/storage-location: default
    spec:
      includedNamespaces:
      - <HC_NAMESPACE>
      - <HC_NAMESPACE>-<HC_NAME>
      includedResources:
      - sa
      - role
      - rolebinding
      - pod
      - pvc
      - pv
      - bmh
      - configmap
      - infraenv
      - priorityclasses
      - pdb
      - agents
      - hostedcluster
      - nodepool
      - secrets
      - services
      - deployments
      - statefulsets
      - hostedcontrolplane
      - cluster
      - agentcluster
      - agentmachinetemplate
      - agentmachine
      - nmstateconfigs
      - machinedeployment
      - machineset
      - machine
      - route
      - clusterdeployment
      excludedResources: []
      defaultVolumesToFsBackup: true
      storageLocation: default
      ttl: 2h30m0s
    ```

=== "**OpenStack**"

    ```yaml
    apiVersion: velero.io/v1
    kind: Backup
    metadata:
      name: hc-backup
      namespace: openshift-adp
      labels:
        velero.io/storage-location: default
    spec:
      includedNamespaces:
      - <HC_NAMESPACE>
      - <HC_NAMESPACE>-<HC_NAME>
      includedResources:
      - sa
      - role
      - rolebinding
      - pod
      - pvc
      - pv
      - configmap
      - priorityclasses
      - pdb
      - hostedcluster
      - nodepool
      - secrets
      - services
      - deployments
      - statefulsets
      - hostedcontrolplane
      - cluster
      - openstackclusters
      - openstackmachinetemplates
      - openstackmachines
      - machinedeployment
      - openstackfloatingippools
      - machineset
      - machine
      - route
      - clusterdeployment
      excludedResources: []
      storageLocation: default
      ttl: 2h30m0s
      snapshotMoveData: true
      datamover: "velero"
      defaultVolumesToFsBackup: false
      snapshotVolumes: true
    ```

=== "**KubeVirt**"

    ```yaml
    apiVersion: velero.io/v1
    kind: Backup
    metadata:
      name: hc-backup
      namespace: openshift-adp
      labels:
        velero.io/storage-location: default
    spec:
      includedNamespaces:
      - <HC_NAMESPACE>
      - <HC_NAMESPACE>-<HC_NAME>
      includedResources:
      - sa
      - role
      - rolebinding
      - deployment
      - statefulset
      - pv
      - pvc
      - bmh
      - configmap
      - infraenv
      - priorityclasses
      - pdb
      - hostedcluster
      - nodepool
      - secrets
      - hostedcontrolplane
      - cluster
      - kubevirtcluster
      - kubevirtmachinetemplate
      - datavolume
      - service
      - route
      excludedResources: []
      labelSelector:
        matchExpressions:
        - key: 'hypershift.openshift.io/is-kubevirt-rhcos'
          operator: 'DoesNotExist'
      storageLocation: default
      preserveNodePorts: true
      ttl: 2h30m0s
      snapshotMoveData: true
      datamover: "velero"
      defaultVolumesToFsBackup: false
    ```

### Key Backup Fields

| Field | Value | Description |
| ------- | ------- | ------------- |
| `includedNamespaces` | HC + HCP namespaces | Must include both the HostedCluster namespace (e.g., `clusters`) and the HostedControlPlane namespace (e.g., `clusters-<hc-name>`) |
| `snapshotMoveData` | `true` | Enables CSI VolumeSnapshots to be uploaded to remote cloud storage |
| `datamover` | `"velero"` | Uses Velero's built-in data mover |
| `defaultVolumesToFsBackup` | `false` (CSI) / `true` (non-CSI) | `false` for CSI-compatible storage, `true` for filesystem backup on non-CSI storage |

## Creating a Restore

The restore manifest is the same across all platforms:

```yaml
apiVersion: velero.io/v1
kind: Restore
metadata:
  name: hc-restore
  namespace: openshift-adp
spec:
  backupName: <BACKUP_NAME>
  restorePVs: true
  existingResourcePolicy: update
  excludedResources:
  - nodes
  - events
  - events.events.k8s.io
  - backups.velero.io
  - restores.velero.io
  - resticrepositories.velero.io
  - csinodes.storage.k8s.io
  - volumeattachments.storage.k8s.io
  - backuprepositories.velero.io
```

Key fields:

- `restorePVs: true` — Initiates recovery of persistent volumes (etcd data).
- `existingResourcePolicy: update` — Overwrites existing objects with backup content. Objects with immutable fields must be deleted before restore (see the scenario guides).

## Scheduling Backups

OADP supports scheduled backups using the Schedule CR. This is fully compatible with the HyperShift backup procedure:

- [OADP Scheduling Documentation](https://docs.redhat.com/en/documentation/openshift_container_platform/latest/html/backup_and_restore/oadp-application-backup-and-restore#oadp-scheduling-backups-doc)

## Data Plane Workloads Backup

!!! note

    If the workloads in the Data Plane are not critical, this step can be skipped.

The backup of Data Plane workloads (applications running in the HostedCluster) is separate from the Control Plane backup. Follow the [official OpenShift backup documentation](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/backing_up_and_restoring/backing-up-applications.html) for Data Plane backup procedures.

Consider:

- Adjusting the `prefix` or `bucket` fields in the DPA to keep Control Plane and Data Plane backups separated.
- Checking if your workloads contain Persistent Volumes and if your StorageClass is compatible with CSI Volume Snapshots.

## Monitoring and Troubleshooting

### Watching Backup/Restore Progress

```bash
# Watch backup
watch "oc get backup -n openshift-adp <NAME> -o jsonpath='{.status}' | jq"

# Watch restore
watch "oc get restore -n openshift-adp <NAME> -o jsonpath='{.status}' | jq"

# Follow Velero logs
oc logs -n openshift-adp -ldeploy=velero -f

# Watch all OADP objects
watch "echo BackupRepositories:;echo;oc get backuprepositories.velero.io -A;\
echo;echo BackupStorageLocations:;echo;oc get backupstoragelocations.velero.io -A;\
echo;echo DataUploads:;echo;oc get datauploads.velero.io -A;\
echo;echo DataDownloads:;echo;oc get datadownloads.velero.io -n openshift-adp;\
echo;echo Backups:;echo;oc get backup -A;\
echo;echo Restores:;echo;oc get restore -A"
```

### Using the Velero CLI

Access the Velero CLI without downloading anything:

```bash
alias velero='oc -n openshift-adp exec deployment/velero -c velero -it -- ./velero'

# Describe a backup
velero backup describe <BACKUP_NAME> --details

# Describe a restore
velero restore describe <RESTORE_NAME> --details

# Delete a backup
velero delete backup <BACKUP_NAME>
```

!!! important

    If you modify the folder structure of the remote storage where backups are hosted, you may encounter issues with `backuprepositories.velero.io`. In such cases, you will need to recreate all associated objects (DPAs, backups, restores, etc.). Always manage backups using the Velero CLI.

For more troubleshooting, see the [Troubleshooting Guide](../troubleshooting.md).
