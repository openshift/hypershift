# Create an Azure cluster with Additional Options
This document describes how to set up an Azure cluster with Hypershift with additional flag options. 

Creating an Azure cluster with Hypershift without any additional flag options can be found [here](create-azure-cluster_on_aks.md).

## Prerequisites
This assumes you are:
1. Using an AKS management cluster
2. Set up external DNS
3. Installed the HyperShift Operator

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
${HYPERSHIFT_BINARY_PATH}/hypershift create cluster azure \
--name "$CLUSTER_NAME" \
--azure-creds $AZURE_CREDS \
--location ${LOCATION} \
--node-pool-replicas 2 \
--base-domain $AZURE_BASE_DOMAIN \
--pull-secret $PULL_SECRET \
--generate-ssh \
--release-image ${RELEASE_IMAGE} \
--external-dns-domain ${MGMT_DNS_ZONE_NAME} \
--resource-group-name "${MANAGED_RG_NAME}" \
--vnet-id "${GetVnetID}" \
--subnet-id "${GetSubnetID}" \
--network-security-group-id "${GetNsgID}" \
--annotations hypershift.openshift.io/pod-security-admission-label-override=baseline \
--management-key-vault-name ${KEY_VAULT_NAME} \
--management-key-vault-tenant-id ${KEY_VAULT_TENANT_ID} \
--fips=true \
--marketplace-publisher azureopenshift \
--marketplace-offer aro4 \
--marketplace-sku aro_417 \
--marketplace-version 417.94.20240701 \
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
${HYPERSHIFT_BINARY_PATH}/hypershift create cluster azure \
--name "$CLUSTER_NAME" \
--azure-creds $AZURE_CREDS \
--location ${LOCATION} \
--node-pool-replicas 2 \
--base-domain $AZURE_BASE_DOMAIN \
--pull-secret $PULL_SECRET \
--generate-ssh \
--release-image ${RELEASE_IMAGE} \
--external-dns-domain ${MGMT_DNS_ZONE_NAME} \
--resource-group-name "${MANAGED_RG_NAME}" \
--vnet-id "${GetVnetID}" \
--subnet-id "${GetSubnetID}" \
--network-security-group-id "${GetNsgID}" \
--annotations hypershift.openshift.io/pod-security-admission-label-override=baseline \
--management-key-vault-name ${KEY_VAULT_NAME} \
--management-key-vault-tenant-id ${KEY_VAULT_TENANT_ID} \
--fips=true \
--marketplace-publisher azureopenshift \
--marketplace-offer aro4 \
--marketplace-sku aro_417 \
--marketplace-version 417.94.20240701 \
--enable-ephemeral-disk true \
--instance-type Standard_DS4_v2 \
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
--instance-type Standard_DS4_v2 \
--disk-storage-account-type Standard_LRS
```
