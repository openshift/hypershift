# Disaster recovery on Hosted Control Planes with OADP-1.5+

!!! important

    This procedure differs from the regular disaster recovery (DR) process due to the use of an OADP plugin that automates many tasks. If you’re unable to deploy OADP 1.5 in your management cluster, please refer to [the regular DR procedure](./backup-and-restore-oadp.md).

This guide outlines the steps for performing disaster recovery on a Hosted Cluster using the OpenShift API for Data Protection (OADP). It distinguishes between:

- Control Plane: Pods running in the Management cluster, functioning as a Hosted Control Plane.
- Data Plane: The Hosted Cluster where customers deploy workloads and develop core business applications.

## Pre-requisites

Ensure the following prerequisites are met on the Management cluster (connected or disconnected):

- A valid StorageClass.
- Cluster-admin access.
- Access to the openshift-adp version 1.5+ subscription via a CatalogSource.
- Access to online storage compatible with OpenShift ADP cloud storage providers (e.g., S3, Azure, GCP, MinIO).
- HostedControlPlane pods are accessible and functioning correctly.

!!! Note "Note for Bare Metal Providers"

    Since the InfraEnv has a different lifecycle than the HostedCluster, it should reside in a separate namespace from the HostedControlPlane and must not be deleted during backup or restore procedures.


!!! important

    Before proceeding further, two crucial points must be noted:

    1. Restoration will occur in a green field environment, signifying that after the HostedCluster has been backed up, it must be destroyed to initiate the restoration process.

    2. Node reprovisioning will take place, necessitating the backup of workloads in the Data Plane before deleting the HostedCluster..

## Deploying OpenShift ADP

To deploy the OADP Operator, refer to the [official Red Hat documentation](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/installing/oadp-installing-operator.html), which provides instructions based on your Management cluster version.
After installation, create a DataProtectionApplication (DPA) object, which defines backup locations, Velero pod configurations, and more. This process varies depending on your cloud or remote storage provider. Relevant documentation is available here.
This guide focuses on the following platforms:

- [AWS](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/installing/installing-oadp-aws.html)
- [Baremetal](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/installing/installing-oadp-mcg.html)
- [Openstack](https://docs.redhat.com/en/documentation/openshift_container_platform/latest/html/installing/installing-on-openstack)
- [KubeVirt](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/installing/installing-oadp-mcg.html)

### Creating Cloud Provider Credentials
Begin by creating credentials for your backup storage platform. Specific instructions are available in the official documentation. For AWS S3 and MinIO, the basic steps are:

```bash
cat << EOF > ./credentials
[default]
aws_access_key_id=<AWS_ACCESS_KEY_ID>
aws_secret_access_key=<AWS_SECRET_ACCESS_KEY>
EOF

oc create secret generic cloud-credentials -n openshift-adp --from-file cloud=credentials
```

!!! note

    If using AWS S3, additional AWS resources must be created to enable data backup and restoration. Follow [these instructions](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/installing/installing-oadp-aws.html#migration-configuring-aws-s3_installing-oadp-aws) to set up the necessary configurations.


### Sample DPA Configurations

Below are some samples of DPA configurations for the mentioned platforms

=== "**AWS**"

    ```yaml
    ---
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
              bucket: <bucket_name>
              prefix: <prefix>
            config:
              region: us-east-1
              profile: "backupStorage"
            credential:
              key: cloud
              name: cloud-credentials
      snapshotLocations:
        - velero:
            provider: aws
            config:
              region: us-east-1
              profile: "volumeSnapshot"
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

=== "**Bare Metal**"

    ```yaml
    ---
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
              s3Url: "http://registry.hypershiftbm.lab:9002"
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

=== "**Openstack**"

    ```yaml
    ---
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


Once you create any of these DPA objects, several pods will be instantiated in the `openshift-adp` namespace. This includes one `node-agent` per node in the Management Cluster and the `velero` deployment.

!!! note

    To follow backup and restore procedures, you can monitor the logs in the velero pod.


## Backup and Upload
=== "**AWS**"
    ### Data Plane workloads backup

    !!! Note

        If the workloads in the Data Plane are not crucial for you, it's safe to skip this step.

    If you need to backup the applications running under the HostedCluster, it's advisable to follow [the official documentation for backup and restore of OpenShift applications](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/backing_up_and_restoring/backing-up-applications.html)

    The steps are the following:

    - Deploy the OADP operator from OLM.
      - Create the DPA (Data Protection Application), with a manifest similar to the one provided earlier. It might be beneficial to adjust the `Prefix` or/and `Bucket` fields to keep the ControlPlane and DataPlane backups separated.
      - Create the backup manifest. This step varies depending on the complexity of the workloads in the Data Plane. It's essential to thoroughly examine how to back up the PersistentVolumes, the backend used, and ensure compatibility with our storage provisioner.

      We recommend checking if your workloads contain Persistent Volumes and if our StorageClass is compatible with CSI Volume Snapshots, which is one of the simplest ways to handle this aspect.

    As a standard approach to maintain consistency in the backup layer for the Hosted Control Plane, we will utilize [`Kopia`](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/backing_up_and_restoring/oadp-about-kopia.html) as the backend tool for data snapshots, along with [`File System Backup`](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/backing_up_and_restoring/oadp-backing-up-applications-restic-doc.html). However, it's possible that your workloads may benefit from a different approach that better aligns with your specific use case.

    !!! Important

        The backup of the workloads residing in the Data Plane falls outside the scope of this documentation. Please refer to the official Openshift-ADP backup documentation for further details. Additional links and information can be found in the [References](#References) section.

    Once we have completed the backup of the Data Plane layer, we can proceed with the backup of the Hosted Control Plane (HCP).


    ### Control Plane backup

    Now, we will apply the backup manifest. Here is how it looks like:

    ```yaml
    ---
    apiVersion: velero.io/v1
    kind: Backup
    metadata:
      name: hc-clusters-hosted-backup
      namespace: openshift-adp
      labels:
        velero.io/storage-location: default
        spec:
      hooks: {}
      includedNamespaces:
      - clusters
      - clusters-hosted
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

    We will emphasize the most important fields:

    - These two fields enable the CSI VolumeSnapshots to be automatically uploaded to the remote cloud storage.

    ```yaml
    snapshotMoveData: true
    datamover: "velero"
    ```

    - This field selects the namespaces from which objects will be backed up. They should include namespaces from both the HostedCluster (in the example `clusters`) and the HostedControlPlane (in the example `clusters-hosted`).

    ```yaml
    includedNamespaces:
    - clusters
    - clusters-hosted
    ```

    Once you apply the manifest, you can monitor the backup process in two places: the backup object status and the Velero logs. Please refer to the [Watching](#watching) section for more information.

    The backup process is considered complete when the `status.phase` is `Completed`.

=== "**Bare Metal - CSI Support**"

    !!! Note

        If the workloads in the Data Plane are not crucial for you, it's safe to skip this step.

    If you need to backup the applications running under the HostedCluster, it's advisable to follow [the official documentation for backup and restore of OpenShift applications](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/backing_up_and_restoring/backing-up-applications.html)

    The steps are the following:

    - Deploy the OADP operator from OLM.
      - Create the DPA (Data Protection Application), with a manifest similar to the one provided earlier. It might be beneficial to adjust the `Prefix` or/and `Bucket` fields to keep the ControlPlane and DataPlane backups separated.
      - Create the backup manifest. This step varies depending on the complexity of the workloads in the Data Plane. It's essential to thoroughly examine how to back up the PersistentVolumes, the backend used, and ensure compatibility with our storage provisioner.

      We recommend checking if your workloads contain Persistent Volumes and if our StorageClass is compatible with CSI Volume Snapshots, which is one of the simplest ways to handle this aspect.

    As a standard approach to maintain consistency in the backup layer for the Hosted Control Plane, we will utilize [`Kopia`](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/backing_up_and_restoring/oadp-about-kopia.html) as the backend tool for data snapshots, along with [`File System Backup`](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/backing_up_and_restoring/oadp-backing-up-applications-restic-doc.html). However, it's possible that your workloads may benefit from a different approach that better aligns with your specific use case.

    !!! Important

        The backup of the workloads residing in the Data Plane falls outside the scope of this documentation. Please refer to the official Openshift-ADP backup documentation for further details. Additional links and information can be found in the [References](#References) section.

    Once we have completed the backup of the Data Plane layer, we can proceed with the backup of the Hosted Control Plane (HCP).

    ### Control Plane backup - For CSI compatible storage class

    Now, we will apply the backup manifest. Here is how it looks like:

    ```yaml
    ---
    apiVersion: velero.io/v1
    kind: Backup
    metadata:
      name: hc-clusters-hosted-backup
      namespace: openshift-adp
      labels:
        velero.io/storage-location: default
        spec:
      hooks: {}
      includedNamespaces:
      - clusters
      - clusters-hosted
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

    We will emphasize the most important fields:

    - These two fields enable the CSI VolumeSnapshots to be automatically uploaded to the remote cloud storage.

    ```yaml
    snapshotMoveData: true
    datamover: "velero"
    ```

    - This field selects the namespaces from which objects will be backed up. They should include namespaces from both the HostedCluster (in the example `clusters`) and the HostedControlPlane (in the example `clusters-hosted`).

    ```yaml
    includedNamespaces:
    - clusters
    - clusters-hosted
    ```

    Once you apply the manifest, you can monitor the backup process in two places: the backup object status and the Velero logs. Please refer to the [Watching](#watching) section for more information.

    The backup process is considered complete when the `status.phase` is `Completed`.

=== "**Bare Metal - Non CSI Support**"

    !!! Note

        If the workloads in the Data Plane are not crucial for you, it's safe to skip this step.

    If you need to backup the applications running under the HostedCluster, it's advisable to follow [the official documentation for backup and restore of OpenShift applications](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/backing_up_and_restoring/backing-up-applications.html)

    The steps are the following:

    - Deploy the OADP operator from OLM.
      - Create the DPA (Data Protection Application), with a manifest similar to the one provided earlier. It might be beneficial to adjust the `Prefix` or/and `Bucket` fields to keep the ControlPlane and DataPlane backups separated.
      - Create the backup manifest. This step varies depending on the complexity of the workloads in the Data Plane. It's essential to thoroughly examine how to back up the PersistentVolumes, the backend used, and ensure compatibility with our storage provisioner.

      We recommend checking if your workloads contain Persistent Volumes and if our StorageClass is compatible with CSI Volume Snapshots, which is one of the simplest ways to handle this aspect.

    As a standard approach to maintain consistency in the backup layer for the Hosted Control Plane, we will utilize [`Kopia`](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/backing_up_and_restoring/oadp-about-kopia.html) as the backend tool for data snapshots, along with [`File System Backup`](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/backing_up_and_restoring/oadp-backing-up-applications-restic-doc.html). However, it's possible that your workloads may benefit from a different approach that better aligns with your specific use case.

    !!! Important

        The backup of the workloads residing in the Data Plane falls outside the scope of this documentation. Please refer to the official Openshift-ADP backup documentation for further details. Additional links and information can be found in the [References](#References) section.

    Once we have completed the backup of the Data Plane layer, we can proceed with the backup of the Hosted Control Plane (HCP).

    ### Control Plane backup - For **Non** CSI compatible storage class

    Now, we will apply the backup manifest. Here is how it looks like:

    ```yaml
    ---
    apiVersion: velero.io/v1
    kind: Backup
    metadata:
      name: hc-clusters-hosted-backup
      namespace: openshift-adp
      labels:
        velero.io/storage-location: default
        spec:
      hooks: {}
      includedNamespaces:
      - clusters
      - clusters-hosted
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

    We will emphasize the most important fields:

    - This particular field is crucial if you utilize a storage class that does not supported by CSI drivers (e.g NFS). It designates fs-backup as the default method for Persistent Volume backup. If you wish to continue using CSI Volume Snapshot (within the same backup manifest), please check the "Bare Metal - CSI Support" tab.

    ```yaml
    defaultVolumesToFsBackup: true
    ```

    - This field selects the namespaces from which objects will be backed up. They should include namespaces from both the HostedCluster (in the example `clusters`) and the HostedControlPlane (in the example `clusters-hosted`).

    ```yaml
    includedNamespaces:
    - clusters
    - clusters-hosted
    ```

    Once you apply the manifest, you can monitor the backup process in two places: the backup object status and the Velero logs. Please refer to the [Watching](#watching) section for more information.

    The backup process is considered complete when the `status.phase` is `Completed`.

=== "**Openstack**"

    !!! Note

        If the workloads in the Data Plane are not crucial for you, it's safe to skip this step.

    If you need to backup the applications running under the HostedCluster, it's advisable to follow [the official documentation for backup and restore of OpenShift applications](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/backing_up_and_restoring/backing-up-applications.html)

    The steps are the following:

    - Deploy the OADP operator from OLM.
      - Create the DPA (Data Protection Application), with a manifest similar to the one provided earlier. It might be beneficial to adjust the `Prefix` or/and `Bucket` fields to keep the ControlPlane and DataPlane backups separated.
      - Create the backup manifest. This step varies depending on the complexity of the workloads in the Data Plane. It's essential to thoroughly examine how to back up the PersistentVolumes, the backend used, and ensure compatibility with our storage provisioner.

      We recommend checking if your workloads contain Persistent Volumes and if our StorageClass is compatible with CSI Volume Snapshots, which is one of the simplest ways to handle this aspect.

    As a standard approach to maintain consistency in the backup layer for the Hosted Control Plane, we will utilize [`Kopia`](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/backing_up_and_restoring/oadp-about-kopia.html) as the backend tool for data snapshots, along with [`File System Backup`](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/backing_up_and_restoring/oadp-backing-up-applications-restic-doc.html). However, it's possible that your workloads may benefit from a different approach that better aligns with your specific use case.

    !!! Important

        The backup of the workloads residing in the Data Plane falls outside the scope of this documentation. Please refer to the official Openshift-ADP backup documentation for further details. Additional links and information can be found in the [References](#References) section.

    Once we have completed the backup of the Data Plane layer, we can proceed with the backup of the Hosted Control Plane (HCP).

    ### Control Plane backup - Openstack

    Now, we will apply the backup manifest. Here is how it looks like:

    ```yaml
    ---
    apiVersion: velero.io/v1
    kind: Backup
    metadata:
      name: hc-clusters-hosted-backup
      namespace: openshift-adp
      labels:
        velero.io/storage-location: default
        spec:
      hooks: {}
      includedNamespaces:
      - clusters
      - clusters-hosted
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
      - openstackmachine
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

    We will emphasize the most important fields:

    - These two fields enable the CSI VolumeSnapshots to be automatically uploaded to the remote cloud storage.

    ```yaml
    snapshotMoveData: true
    datamover: "velero"
    ```

    - This field selects the namespaces from which objects will be backed up. They should include namespaces from both the HostedCluster (in the example `clusters`) and the HostedControlPlane (in the example `clusters-hosted`).

    ```yaml
    includedNamespaces:
    - clusters
    - clusters-hosted
    ```

    Once you apply the manifest, you can monitor the backup process in two places: the backup object status and the Velero logs. Please refer to the [Watching](#watching) section for more information.

    The backup process is considered complete when the `status.phase` is `Completed`.

=== "**KubeVirt**"

    ### Backup of a kubeVirt Hosted Cluster
    !!! important
        The restore may only be done on the same management cluster where the backup was created.

    Backup of a hosted cluster, running on a KubeVirt platform may be done on a running hosted cluster, and there is no
    need to pause it.

    The backup will contain the hosted control plane components, the hosted cluster ETCD, and the data stored on the
    hosted cluster PVCs.

    The backup will not contain the KubeVirt VMs, used as worker nodes, and they will be automatically recreated after
    the restore.
    #### Performing the Backup
    The backup itself is done by creating a velero Backup resource. Here is how its manifest looks like:
    ```yaml
    apiVersion: velero.io/v1
    kind: Backup
    metadata:
      name: hc-clusters-hosted-backup
      namespace: openshift-adp
      labels:
        velero.io/storage-location: default
    spec:
      includedNamespaces:
      - clusters
      - clusters-hosted
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
      - datavolume
      - service
      - route
      excludedResources: [ ]
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
    We will emphasize the most important fields:

    - These two fields enable the CSI VolumeSnapshots to be automatically uploaded to the remote cloud storage.

        ```yaml
        snapshotMoveData: true
        datamover: "velero"
        ```

    - We don't want to use this feature. This will allow us to safely backup the PVCs we want

        ```yaml
        defaultVolumesToFsBackup: false
        ```

    - This field selects the namespaces from which objects will be backed up. They should include namespaces from both
      the HostedCluster (in the example `clusters`) and the HostedControlPlane (in the example `clusters-hosted`).


        ```yaml
        includedNamespaces:
        - clusters
        - clusters-hosted
        ```

    !!! hint
        By default, the HostedControlPlane namespace is `clusters-<hosted cluster name>`.

    - The boot image of the KubeVirt VMs, that are used as the hosted cluster nodes, are stored in huge PVCs. We don't
      need these PVCs because the VMs are going to be recreated as new VMs. We want tilter these PVCs out of the backup
      to gain meaningful reduce in backup time and storage size. We'll filter these PVC using this label selector:

        ```yaml
        labelSelector:
          matchExpressions:
          - key: 'hypershift.openshift.io/is-kubevirt-rhcos'
            operator: 'DoesNotExist'
        ```

    Once you apply the manifest, you can monitor the backup process in two places: the backup object status and the Velero logs. Please refer to the [Watching](#watching) section for more information.

    The backup process is considered complete when the `status.phase` is `Completed`.


## Restore

For the restoration procedure, ensure that there are no Pods/PVCs running in the HostedControlPlane namespace to facilitate a successful restoration. If restoring the HostedCluster within the same Management cluster, delete the following objects:

- HostedCluster
- Nodepools
- PVCs

This will remove the Pods/PVCs, allowing for a proper restoration of the environment.

!!! important

    In the case of the Bare Metal provider (Agent), it's crucial to ensure that we don't delete the InfraEnv object. This object is mandatory for the new nodes that will be reprovisioned, as they need access to it in order to retrieve the Discovery ISO.

The restoration object is provider agnostic. Just make sure you point to the right backupName to restore from

Now let's take a look to the restoration manifest:

```yaml
---
apiVersion: velero.io/v1
kind: Restore
metadata:
  name: hc-clusters-hosted-restore
  namespace: openshift-adp
spec:
  backupName: hc-clusters-hosted-backup
  restorePVs: true
  existingResourcePolicy: update
  excludedResources:
  - nodes
  - events
  - events.events.k8s.io
  - backups.velero.io
  - restores.velero.io
  - resticrepositories.velero.io
```

Here we have a couple of important fields

```yaml
restorePVs: true
existingResourcePolicy: update
```

- `restorePvs` will initiate the recovery of pods with the included persistent volumes.
- Setting `existingResourcePolicy` to `update` ensures that any existing objects are overwritten with the backup content. This may cause issues with objects containing immutable fields, which is why we delete them in a previous step. If this policy is not set, the Velero engine will skip the restoration of objects that already exist.

You can monitor the restoration process by checking the restore status field and following the Velero logs mentioned in the [Watching](#watching) section.

The restoration process is considered complete once the `status.phase` is `Completed`.

!!! important

    The restore may only be done on the same management cluster where the backup was created. Depending on the HostedCluster provider you are using, there are important topics to have in mind before the restoration.


=== "**AWS**"

    - Node readoption is not supported in this provider yet, so the worker nodes will be reprovisioned at restoration time

=== "**Openstack**"

    - Node readoption is not supported in this provider yet, so the worker nodes will be reprovisioned at restoration time

=== "**BareMetal**"

    - If you are restoring the HostedCluster in the same Management Cluster, make sure:
        - You don't delete the Infraenv namespace or the objects inside.
        - You don't remove the Assisted Installer Postgres DDBB
    - If you are restoring the HostedCluster in a different Management Cluster, have in mind that you should backup some things too:
        - Backup of the infraenv namespace and their objects
        - You need to backup and restore the Postgres DDBB
        - All of this could be included in the Backup manifest to do this automatically
    - Node readoption is supported in this provider only under certain conditions:
        - For now only in supported in HostedClusters with 4.19 OCP versions
        - MCE 2.9 or ACM 2.14

=== "**Kubevirt**"

    - Restoration in a separated Management cluster is not supported by this provider
    - Node readoption is not supported in this provider yet, so the worker nodes will be reprovisioned at restoration time


## Schedule

OADP provides the ability to schedule backups using the Schedule CR. This is fully compatible with the Hosted Control Planes backup procedure; just make sure you are following the official Red Hat documentation.

- https://docs.redhat.com/en/documentation/openshift_container_platform/latest/html/backup_and_restore/oadp-application-backup-and-restore#oadp-scheduling-backups-doc

## Watching and Troubleshooting

### Watching

Here we will describe how to monitor and observe a Backup or Restore process:

- Watch the Backup process

```bash
watch "oc get backup -n openshift-adp <BACKUP_NAME> -o jsonpath='{.status}' | jq"
```

- Watch the Restore process

```bash
watch "oc get restore -n openshift-adp <BACKUP_NAME> -o jsonpath='{.status}' | jq"
```

- Follow the Velero logs

```bash
oc logs -n openshift-adp -ldeploy=velero -f
```

- Watch all the OADP Main objects

```bash
watch "echo BackupRepositories:;echo;oc get backuprepositories.velero.io -A;echo; echo BackupStorageLocations: ;echo; oc get backupstoragelocations.velero.io -A;echo;echo DataUploads: ;echo;oc get datauploads.velero.io -A;echo;echo DataDownloads: ;echo;oc get datadownloads.velero.io -n openshift-adp; echo;echo VolumeSnapshotLocations: ;echo;oc get volumesnapshotlocations.velero.io -A;echo;echo Backups:;echo;oc get backup -A; echo;echo Restores:;echo;oc get restore -A"
```

### Troubleshooting

For troubleshooting purposes, it's crucial to identify where the process is stuck. Here are some tips:

- Review the Velero logs in the `openshift-adp` namespace.
- Utilize the `velero` command with the `--details` flag to describe the backup/restore objects.
- Check the backup/restore status directly in the respective object, which can provide valuable hints.
- Examine the Events in the affected namespaces (in the previous examples, these were `clusters` and `clusters-hosted`).
- Verify the status of all OADP objects using the command provided in the [Watching section](#watching).

## Tips and Tricks

### Use the velero CLI

To gain more insights into the backup/restore objects or to perform actions such as deleting a backup/restore object, you can utilize the `velero` CLI. This tool provides additional context in each case, and the best part is that you don't need to download anything.

- Create an alias to use the `velero` CLI from a container

```bash
alias velero='oc -n openshift-adp exec deployment/velero -c velero -it -- ./velero'
```

Now you can execute commands using the `velero` CLI. Here are some sample commands:

- Describe a restore called `hc-clusters-hosted-restore`

```bash
velero restore describe hc-clusters-hosted-restore --details
```

- Describe a backup called `hc-clusters-hosted-backup`

```bash
velero backup describe hc-clusters-hosted-backup --details
```

### How to handle backups properly

To handle the `backup` and `restore` objects in the cloud storage effectively, the following command is quite useful. It helps identify issues with the `backuprepositories.velero.io` object if you manually modify the folder structure in the destination storage. Therefore, we recommend avoiding modifications over that storage and managing the backups/restore objects using the CLI.

- Delete a backup created and called `hc-clusters-hosted-backup`

```bash
velero delete backup hc-clusters-hosted-backup
```

!!! Important

    If you modify the folder structure of the remote storage where your backups are hosted, you may encounter issues with `backuprepositories.velero.io`. In such cases, you will need to recreate all the associated objects, including DPAs, backups, restores, etc.