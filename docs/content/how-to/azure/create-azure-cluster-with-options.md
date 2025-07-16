# Create an Azure cluster with Additional Options
This document describes how to set up an Azure cluster with Hypershift with additional options.

Creating an Azure cluster with Hypershift without any additional flag options can be found [here](create-azure-cluster-on-aks.md).

## Prerequisites
All sections assume you are:
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
--replicas <number_of_replicas> \
--release-image <release_image> \
--enable-ephemeral-disk true \
--instance-type Standard_DS4_v2 \
--disk-storage-account-type Standard_LRS
```

## Enabling KMS encryption
This section walks through how to:

1. Set up a new resource group, key vault, and key for etcd encryption using KMSv2
1. Set up the role assignment between the KMS managed identity (MI) and the key vault
1. Set up the flags needed when creating the Azure HostedCluster
1. Verify the etcd encryption is setup and working properly 

There is a `setup_etcd_kv.sh` script in the contrib folder in the HyperShift repo to help automate the first couple of 
steps mentioned above. However, this guide will manually walk through those steps.

1a) Create a resource group for the key vault that will house the key used for etcd encryption. 

!!! note

    It is assumed this key vault is a different key vault, let's call it MI KV, than the one containing all of the 
    managed identities for the control plane. However, the managed identity for KMS is assumed to be in the MI KV.

```bash
az group create --name example-kms --location eastus
```

1b) Create the etcd encryption key vault
```bash
az keyvault create --name example-kms --resource-group example-kms --location eastus --enable-rbac-authorization
```

1c) Create a key in the etcd encryption key vault and capture the ID in a variable, KEY_ID. This will be passed when 
creating the Azure HostedCluster in a later step below.
```bash
KEY_ID=$(az keyvault key create \
  --vault-name example-kms \
  --name example-key \
  --protection software \
  --kty RSA \
  --query key.kid \
  -o tsv)
```

2) Create a role assignment between the KMS MI and the resource group where the etcd encryption key vault is located so 
that it can encrypt & decrypt objects.

```bash
OBJECT_ID="the object ID of the KMS Managed Identity. This object ID can be found under the enterprise application for your KMS Managed Identity"

az role assignment create --assignee $OBJECT_ID --role "Key Vault Crypto User" \
--scope $(az keyvault show --name example-kms --query "resourceGroup" -o tsv | xargs -I{} az group show --name {} --query "id" -o tsv)
```

3) Add these flags to your HyperShift CLI command when creating the Azure HostedCluster. KEY_ID is from step 1d.
```
`--encryption-key-id $KEY_ID` \
`--kms-credentials-secret-name <your KMS credentials secret name>`
```

4) Here are some different things you can do to confirm etcd encryption using KMSv2 is set up properly on the 
HCP/HostedCluster:

First, confirm the kube-apiserver pod is using the `encryption-provider-config` flag such as:
```
--encryption-provider-config=/etc/kubernetes/secret-encryption/config.yaml 
```

If you look at this data, it should contain something like this:
```
apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
- providers:
  - kms:
      apiVersion: v2
      endpoint: unix:///opt/azurekmsactive.socket
      name: azure-20514bc7
      timeout: 35s
  - identity: {}
  resources:
  - secrets
  - configmaps
  - routes.route.openshift.io
  - oauthaccesstokens.oauth.openshift.io
  - oauthauthorizetokens.oauth.openshift.io
```

Next, confirm the ` azure-kms-provider-active` container in the kube-apiserver pod is running properly, there are no 
errors in the log, and the config file is using the KMS MI. The config file path can be found in the flag on the 
container spec:
```
--config-file-path=/etc/kubernetes/azure.json 
```

If you review this data, you should see the KMS MI credentials secret used within it.

Finally, you can create a secret on the HostedCluster and then check the secret on etcd in the etcd pod on the HCP 
directly:

1) Create a secret on the HostedCluster. Example `kubectl create secret generic kms-test --from-literal=foo=bar`.

2) Verify you can see the secret contents on the HostedCluster unencrypted.

3) Switch back to the AKS management cluster and exec into the etcd pod, `kubectl exec -it pod/etcd-0 -- /bin/bash`.

4) Run these commands in the etcd pod
```
export ETCDCTL_API=3
export ETCDCTL_CACERT=/etc/etcd/tls/etcd-ca/ca.crt
export ETCDCTL_CERT=/etc/etcd/tls/client/etcd-client.crt
export ETCDCTL_KEY=/etc/etcd/tls/client/etcd-client.key
export ETCDCTL_ENDPOINTS=https://etcd-client:2379
```
5) Get the secret created on the HostedCluster `etcdctl get /kubernetes.io/secrets/default/kms-test`. You should see it 
is encrypted with KMSv2 by the azure provider:
```
k8s:enc:kms:v2:azure-8298bce7:
�d2%&G
      	E��k(�B�	�H�����6#�]�[���I
...
�=�s��h���Fq��a^(��z ��wIȫ�ݹ��,�բRa�A홟��5u�΀��*��᥃��ƚL�$L1Y'�V�Ӧi��.�	R�"                                            �
version.azure.akv.io1"&
algorithm.azure.akv.io
                      RSA-OAEP-256(
```
