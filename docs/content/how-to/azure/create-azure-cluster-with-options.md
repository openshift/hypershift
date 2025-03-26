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

## Creating Service Principals for Managed Identities
Pre-requisites:
1. Key Vault Administrator role on the Key Vault


### Define variables for service principal names
```
AZURE_DISK_SP_NAME=<azure-disk-sp-name>
AZURE_FILE_SP_NAME=<azure-file-sp-name>
NODEPOOL_MGMT=<nodepool-mgmt-sp-name>
CLOUD_PROVIDER_SP_NAME=<cloud-provider-sp-name>
CNCC_NAME=<cncc-sp-name>
CONTROL_PLANE_SP_NAME=<cpo-sp-name>
IMAGE_REGISTRY_SP_NAME=<ciro-sp-name>
INGRESS_SP_NAME=<ingress-sp-name>
KEY_VAULT_NAME=<name-of-precreated-key-vault>
KEY_VAULT_TENANT_ID=<tenant-id-of-precreated-key-vault>
```

### Create service principals and capture app IDs
```
DISK_SP_APP_ID=$(az ad sp create-for-rbac --name "${AZURE_DISK_SP_NAME}" --create-cert --cert "${AZURE_DISK_SP_NAME}" --keyvault ${KV_NAME} --output json --only-show-errors | jq '.appId' | sed 's/"//g')
FILE_SP_APP_ID=$(az ad sp create-for-rbac --name "${AZURE_FILE_SP_NAME}" --create-cert --cert "${AZURE_FILE_SP_NAME}" --keyvault ${KV_NAME} --output json --only-show-errors | jq '.appId' | sed 's/"//g')
NODEPOOL_MGMT_APP_ID=$(az ad sp create-for-rbac --name "${NODEPOOL_MGMT}" --create-cert --cert "${NODEPOOL_MGMT}" --keyvault ${KV_NAME} --output json --only-show-errors | jq '.appId' | sed 's/"//g')
CLOUD_PROVIDER_APP_ID=$(az ad sp create-for-rbac --name "${CLOUD_PROVIDER_SP_NAME}" --create-cert --cert "${CLOUD_PROVIDER_SP_NAME}" --keyvault ${KV_NAME} --output json --only-show-errors | jq '.appId' | sed 's/"//g')
CNCC_APP_ID=$(az ad sp create-for-rbac --name "${CNCC_NAME}" --create-cert --cert "${CNCC_NAME}" --keyvault ${KV_NAME} --output json --only-show-errors | jq '.appId' | sed 's/"//g')
CONTROL_PLANE_APP_ID=$(az ad sp create-for-rbac --name "${CONTROL_PLANE_SP_NAME}" --create-cert --cert "${CONTROL_PLANE_SP_NAME}" --keyvault ${KV_NAME} --output json --only-show-errors | jq '.appId' | sed 's/"//g')
IMAGE_REGISTRY_APP_ID=$(az ad sp create-for-rbac --name "${IMAGE_REGISTRY_SP_NAME}" --create-cert --cert "${IMAGE_REGISTRY_SP_NAME}" --keyvault ${KV_NAME} --output json --only-show-errors | jq '.appId' | sed 's/"//g')
INGRESS_APP_ID=$(az ad sp create-for-rbac --name "${INGRESS_SP_NAME}" --create-cert --cert "${INGRESS_SP_NAME}" --keyvault ${KV_NAME} --output json --only-show-errors | jq '.appId' | sed 's/"//g')
```

### Save service principal IDs and certificate names to a JSON file
```
SP_FILE="service-principals.json"

# Save service principal IDs and certificate names to a JSON file
OUTPUT_FILE="service-principals.json"

cat <<EOF > SP_FILE
{
    "cloudProvider": {
        "certificateName": "${CLOUD_PROVIDER_SP_NAME}",
        "clientID": "${CLOUD_PROVIDER_APP_ID}"
    },
    "controlPlaneOperator": {
        "certificateName": "${CONTROL_PLANE_SP_NAME}",
        "clientID": "${CONTROL_PLANE_APP_ID}"
    },
    "disk": {
        "certificateName": "${AZURE_DISK_SP_NAME}",
        "clientID": "${DISK_SP_APP_ID}"
    },
    "file": {
        "certificateName": "${AZURE_FILE_SP_NAME}",
        "clientID": "${FILE_SP_APP_ID}"
    },
    "imageRegistry": {
        "certificateName": "${IMAGE_REGISTRY_SP_NAME}",
        "clientID": "${IMAGE_REGISTRY_APP_ID}"
    },
    "ingress": {
        "certificateName": "${INGRESS_SP_NAME}",
        "clientID": "${INGRESS_APP_ID}"
    },
    "network": {
        "certificateName": "${CNCC_NAME}",
        "clientID": "${CNCC_APP_ID}"
    },
    "nodePoolManagement": {
        "certificateName": "${NODEPOOL_MGMT}",
        "clientID": "${NODEPOOL_MGMT_APP_ID}"
    },
    "managedIdentitiesKeyVault": {
        "name": "${KV_NAME}",
        "tenantID": "{KV_TENANT_ID}"
    }
}
EOF
```

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
--managed-identities-file ${SP_FILE} \
--assign-service-principal-roles \
--dns-zone-rg-name ${DNS_ZONE_RG_NAME} \
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
--managed-identities-file ${MANAGED_IDENTITIES_FILE} \
--assign-service-principal-roles \
--dns-zone-rg-name ${DNS_ZONE_RG_NAME} \
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
