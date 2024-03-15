# Create an Azure cluster with Additional Options
This document describes how to set up an Azure cluster with Hypershift with additional flag options. 

Creating an Azure cluster with Hypershift without any additional flag options can be found [here](create-azure-cluster.md).

## Prerequisites
See the Prerequisites section in [Create an Azure Cluster](./create-azure-cluster.md#prerequisites)

## Creating the Cluster in an Existing Resource Group
If you want to use an existing resource group you've created in Azure, you can pass the name into the `--resource-group-name` flag. This will create all needed Azure infrastructure in specified resource group.

```
hypershift create cluster azure \ 
--name <cluster_name> \
--pull-secret <pull_secret_file> \
--azure-creds <path_to_azure_credentials_file> \
--location <location> \
--base-domain <base_domain> \
--release-image <release_image> \
--node-pool-replicas <number_of_replicas> \
--resource-group-name <my_existing_resource_group>
```

If you need to delete your hosted cluster, you will need to also use the `--resource-group-name` flag on the delete command.

```
hypershift destroy cluster azure \
--name <cluster_name> \ 
--azure-creds <path_to_azure_credentials_file> \
--resource-group-name <my_existing_resource_group>
```

!!! note

    If you delete your hosted cluster, it will end up deleting any existing resources prior to when the hosted cluster was created as well as the resource group itself.

## Encrypting the OS Disks on Azure VMs
There are a few prerequisites for encrypting the OS disks on the Azure VMs:

1. Create your own resource group
2. Create an Azure Key Vault, with purge protection required, within the resource group
3. Create a key in the vault to use to create a DiskEncryptionSet
4. Create a DiskEncryptionSet with key in the vault and grant it permissions to assess the key vault

!!! note

    You will need to use the `resource-group-name` flag when using the `DiskEncryptionSetID` flag.

After performing these steps, you just need to provide the DiskEncryptionSet ID when creating a hosted cluster.

### CLI Example
```
hypershift create cluster azure \
--name <cluster_name> \
--pull-secret <pull_secret_file> \
--azure-creds <path_to_azure_credentials_file> \
--location <location> \
--base-domain <base_domain> \
--release-image <release_image> \
--node-pool-replicas <number_of_replicas> \
--resource-group-name <resource_group_name> \
--disk-encryption-set-id <disk_encryption_set_id>
```

You can also pass in the DiskEncryptionSet ID when creating a NodePool.

```
hypershift create nodepool azure \
--name <name_of_nodepool> \
--cluster-name <cluster_name> \
--resource-group-name <resource_group_name> \
--disk-encryption-set-id <disk_encryption_set_id>
```

### NodePool CR Example
The DiskEncryptionSet ID can also be set directly through the NodePool CR. 

```
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  creationTimestamp: null
  name: <nodepool_name>
  namespace: clusters
spec:
  arch: amd64
  clusterName: <cluster_name>
  management:
    autoRepair: false
    upgradeType: Replace
  platform:
    azure:
      diskEncryptionSetID: <disk_encryption_set_id>
      diskSizeGB: 120
      vmsize: Standard_D4s_v4
    type: Azure
  release:
    image: <release_image>
  replicas: <number_of_replicas>
status:
  replicas: 0
```

## Enabling Ephemeral OS Disks on Azure VMs
To enable the ephemeral OS disk option on the Azure VMs in your HostedCluster, set the `enable-ephemeral-disk` flag to true.

!!! important

    Ephermeral OS disks are not available in every region or for every instance type. 

    You may need to adjust the disk storage account type; to adjust the disk storage account type, 
    use the `disk-storage-account-type` flag as shown in the example below.
    
    You may need to adjust the disk size depending on the instance type used; to adjust the disk size, use the 
    `root-disk-size` flag.

    See [Ephemeral OS disks for Azure VMs](https://learn.microsoft.com/en-us/azure/virtual-machines/ephemeral-os-disks) for more details.

```
hypershift create cluster azure \
--name <cluster_name> \
--pull-secret <pull_secret_file> \
--azure-creds <path_to_azure_credentials_file> \
--location <location> \
--base-domain <base_domain> \
--release-image <release_image> \
--node-pool-replicas <number_of_replicas> \
--enable-ephemeral-disk true \
--instance-type Standard_DS2_v2 \
--disk-storage-account-type Standard_LRS
```

You can also set the `enable-ephemeral-disk` flag when creating a NodePool.
```
hypershift create nodepool azure \
--name <name_of_nodepool> \
--cluster-name <cluster_name> \
--node-count <number_of_replicas> \
--release-image <release_image> \
--enable-ephemeral-disk true \
--instance-type Standard_DS2_v2 \
--disk-storage-account-type Standard_LRS
```

## Setting Subnet Name on NodePools
You can specify which subnet your nodes (i.e., VMs) are placed in when either creating a Hosted Cluster from scratch or when creating a new NodePool.

!!! important

    You must specify which subnet your Load Balancer will be located in, in the HostedCluster CRD as it is needed by Azure Cloud Provider.

### Setting Subnet Name When Creating a Hosted Cluster
Here is an example of setting the default subnet name through the HyperShift CLI:
```
hypershift create cluster azure \
--name <cluster_name> \
--pull-secret <pull_secret_file> \
--azure-creds <path_to_azure_credentials_file> \
--location <location> \
--base-domain <base_domain> \
--release-image <release_image> \
--node-pool-replicas <number_of_replicas> \
--subnet-name defaultSubnetName
```

Here is an example of setting the subnet name, under spec.platform.azure, in the HostedCluster CRD:
```
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: <cluster_name>
  namespace: <namespace>
spec:
  fips: false
  release:
    image: <release_image>
  dns:
    baseDomain: <baseDomain>
    privateZoneID: <privateZoneID>
    publicZoneID: <publicZoneID>
  controllerAvailabilityPolicy: SingleReplica
  infraID: <infra_id>
  etcd:
    managed:
      storage:
        persistentVolume:
          size: 8Gi
        type: PersistentVolume
    managementType: Managed
  infrastructureAvailabilityPolicy: SingleReplica
  platform:
    azure:
      machineIdentityID: <machine_identity_id>
      vnetID: <vnet_id>
      subnetName: default
...
```

### Setting Subnet Name When Creating a NodePool
Here is an example of setting the subnet name of the NodePool through the HyperShift CLI:
```
hypershift create nodepool azure \
--cluster-name <cluster_name> \
--name <nodepool_name> \
--node-count <number_of_replicas> \
--release-image <release_image> \
--subnet-name <subnet_name>
```

Here is an example of setting the subnet name of the NodePool, under spec.platform.azure, in the NodePool CRD:
```
apiVersion: hypershift.openshift.io/v1beta1
    kind: NodePool
    metadata:
      name: test-np
      namespace: clusters
    spec:
      arch: amd64
      clusterName: <cluster_name>
      management:
        autoRepair: false
        replace:
          rollingUpdate:
            maxSurge: 1
            maxUnavailable: 0
          strategy: RollingUpdate
        upgradeType: Replace
      platform:
        azure:
          diskSizeGB: 120
          diskStorageAccountType: Premium_LRS
          subnetName: <subnet_name>
          vmsize: Standard_D4s_v4
        type: Azure
      release:
        image: <release_image>
      replicas: 2
```
