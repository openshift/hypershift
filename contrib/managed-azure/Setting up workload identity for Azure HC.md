# Setting up Workload Identity for Azure HostedClusters

This document describes the steps needed to set up things for Workload Identity for Azure HostedClusters using the
following workload identity PR, https://github.com/openshift/hypershift/pull/4587. In general, the following is a brief
description of the steps needed to set everything up:

1. Create a resource group for the managed Azure resources for the HCP
2. Run the ccoctl tool to generate some prerequisite items
3. Create an AKS mgmt cluster
4. Setup externalDNS
5. Install the HO
6. Create and set up managed identities for azure disk csi, azure file csi, and image registry operators
7. Create the HostedCluster

The document sections that follow will walk through each part in more depth. This document also:
 - assumes you have already set up persistent service principals for the HCP components.
 - assumes you know how to set up the AKS management cluster to support managed identities/service principals for the
hosted control plane.
 - See [this for more details on those subjects](setup_dev_environment.md).

## Create a resource group for the managed Azure resources for the HCP
You will need a resource group for the ccoctl tool to place some Azure resources. This same resource group will be used
for the other Azure resources created during the `hypershift create cluster azure ...` command.

```shell
PREFIX="my-prefix" # this could be a Jira ticket, your username, etc.
MANAGED_RG_NAME="${PREFIX}-managed-rg"
LOCATION="eastus"

az group create --name "${MANAGED_RG_NAME}" --location ${LOCATION}
```

## Run the ccoctl tool to generate some prerequisite items
The ccoctl tool provides various commands to assist with the creating and maintenance of cloud credentials from outside
a cluster. More information on the tool can be found [here](https://github.com/openshift/cloud-credential-operator/blob/master/docs/ccoctl.md).

You will need this tool to run two commands, though there are probably other ways of doing each of these:
1. Creating RSA keys
2. Creating OpenID Connect Issuer


### Creating RSA keys
Follow the instructions [here](https://github.com/openshift/cloud-credential-operator/blob/master/docs/ccoctl.md#creating-rsa-keys-1)
to run this command. It should look something like this:
```shell
% ./ccoctl azure create-key-pair
2024/11/14 12:57:29 Generating RSA keypair
2024/11/14 12:57:30 Writing private key to /Users/username/cloud-credential-operator/serviceaccount-signer.private
2024/11/14 12:57:30 Writing public key to /Users/username/cloud-credential-operator/serviceaccount-signer.public
2024/11/14 12:57:30 Copying signing key for use by installer
```

Save the private key path off as you will need it in [this step](Setting%20up%20workload%20identity%20for%20Azure%20HC.md#create-the-hostedcluster).


### Creating OpenID Connect Issuer
Follow the instructions [here](https://github.com/openshift/cloud-credential-operator/blob/master/docs/ccoctl.md#creating-openid-connect-issuer)
to run this command. It should look something like this:
```shell
STORAGE_ACCOUNT_NAME=mystoragename

% ./ccoctl azure create-oidc-issuer \
--oidc-resource-group-name ${MANAGED_RG_NAME} \
--tenant-id ${TENANT_ID} \
--region ${LOCATION} \
--name ${STORAGE_ACCOUNT_NAME} \
--subscription-id <subscriptionID> \
--public-key-file /Users/username/cloud-credential-operator/serviceaccount-signer.public
2024/11/14 12:09:39 No --output-dir provided, defaulting output directory to the current working directory /Users/username/cloud-credential-operator
2024/11/14 12:09:39 No --storage-account-name provided, defaulting storage account name to mystoragename
2024/11/14 12:09:39 No --blob-container-name provided, defaulting blob container name to mystoragename
2024/11/14 12:09:41 Updated resource group /subscriptions/<subscriptionID>/resourceGroups/your-managed-rg
2024/11/14 12:10:03 Created storage account /subscriptions/<subscriptionID>/resourceGroups/your-managed-rg/providers/Microsoft.Storage/storageAccounts/mystoragename
2024/11/14 12:10:04 Created blob container /subscriptions/<subscriptionID>/resourceGroups/your-managed-rg/providers/Microsoft.Storage/storageAccounts/mystoragename/blobServices/default/containers/mystoragename
2024/11/14 12:10:04 Saved OIDC discovery document at path /Users/username/cloud-credential-operator/openid-configuration
2024/11/14 12:10:04 Reading public key
2024/11/14 12:10:04 Saved JSON web key set at path /Users/username/cloud-credential-operator/jwks
2024/11/14 12:10:05 Uploaded OIDC discovery document https://mystoragename.blob.core.windows.net/mystoragename/.well-known/openid-configuration
2024/11/14 12:10:05 Uploaded JSON web key set https://mystoragename.blob.core.windows.net/mystoragename/openid/v1/jwks
2024/11/14 12:10:05 Wrote cluster authentication manifest at path /Users/username/cloud-credential-operator/manifests/cluster-authentication-02-config.yaml
2024/11/14 12:10:05 Issuer URL (serviceAccountIssuer) is https://mystoragename.blob.core.windows.net/mystoragename
2024/11/14 12:10:05 Saved Azure AD pod identity webhook configuration to: /Users/username/cloud-credential-operator/manifests/azure-ad-pod-identity-webhook-config.yaml
```

Save your Issuer URL off as you will need it in [this step](Setting%20up%20workload%20identity%20for%20Azure%20HC.md#create-the-hostedcluster)

## Create an AKS mgmt cluster
See [this shell script](setup_aks_cluster.sh) for information on how to create an AKS management cluster.

## Setup externalDNS
See [this shell script](setup_external_dns.sh) for information on how to set up externalDNS on AKS.

## Install the HO
See [this shell script](setup_install_ho_on_aks.sh) for information on how to install the HO on AKS.

## Create and set up managed identities for azure disk csi, azure file csi, and image registry operators
You'll need to create and setup three managed identities to enable workload identity to work successfully. You'll want
to make those in a resource group that is persistent. These should be reused during development, testing, and in CI.

Here are the commands to create and setup those three managed identities; this assumes you are making the managed
identities outside the managed resource group, in a group let's call _PERSISTENT_RG_NAME_:

```shell
CSI_DISK_MSI_NAME="csi-disk-msi"
CSI_FILE_CCM_MSI_NAME="csi-file-msi"
IR_MSI_NAME="ir-msi"
OIDC_ISSUER_URL="this is the URL you saved in step - Creating OpenID Connect Issuer"

# Azure Disk managed identity
## Create the managed identity
az identity create --name "${CSI_DISK_MSI_NAME}" --resource-group "${MANAGED_RG_NAME}"

## Get the managed identity principal ID
CSI_DISK_ID=$(az identity show --name "${CSI_DISK_MSI_NAME}" --resource-group "${MANAGED_RG_NAME}" --query principalId --output tsv)

az role assignment create \
--assignee-object-id "${CSI_DISK_ID}" \
--role "Azure Red Hat OpenShift Storage Operator Role" \
--scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/"${MANAGED_RG_NAME}" \
--assignee-principal-type ServicePrincipal

# Get the client id of the managed identity
CSI_DISK_CLIENT_ID=$(az identity show --name "${CSI_DISK_MSI_NAME}" --resource-group "${MANAGED_RG_NAME}" --query clientId --output tsv)

#Federated ID
az identity federated-credential create --name "${CSI_DISK_MSI_NAME}"-fed-id \
--identity-name "${CSI_DISK_MSI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-node-sa \
--audience openshift

########################################################################################################################

# Azure file managed identity
## Create the managed identity
az identity create --name "${CSI_FILE_CCM_MSI_NAME}" --resource-group "${MANAGED_RG_NAME}"

## Get the managed identity principal ID
CSI_FILE_MSI_ID=$(az identity show --name "${CSI_FILE_CCM_MSI_NAME}" --resource-group "${MANAGED_RG_NAME}" --query principalId --output tsv)

az role assignment create \
--assignee-object-id "${CSI_FILE_MSI_ID}" \
--role "Azure Red Hat OpenShift Azure Files Storage Operator Role" \
--scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/"${MANAGED_RG_NAME}" \
--assignee-principal-type ServicePrincipal

# Get the client id of the managed identity
CSI_FILE_MSI_CLIENT_ID=$(az identity show --name "${CSI_FILE_CCM_MSI_NAME}" --resource-group "${MANAGED_RG_NAME}" --query clientId --output tsv)

#Federated ID
az identity federated-credential create --name "${CSI_FILE_CCM_MSI_NAME}"-fed-id \
--identity-name "${CSI_FILE_CCM_MSI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-node-sa \
--audience openshift

########################################################################################################################

# IR managed identity
## Create the managed identity
az identity create --name "${IR_MSI_NAME}" --resource-group "${MANAGED_RG_NAME}"

## Get the managed identity principal ID
IR_MSI_ID=$(az identity show --name "${IR_MSI_NAME}" --resource-group "${MANAGED_RG_NAME}" --query principalId --output tsv)

az role assignment create \
--assignee-object-id "${IR_MSI_ID}" \
--role "Azure Red Hat OpenShift Image Registry Operator Role" \
--scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/"${MANAGED_RG_NAME}" \
--assignee-principal-type ServicePrincipal

# Get the client id of the managed identity
IR_MSI_CLIENT_ID=$(az identity show --name "${IR_MSI_NAME}" --resource-group "${MANAGED_RG_NAME}" --query clientId --output tsv)

#Federated ID
az identity federated-credential create --name "${IR_MSI_NAME}"-fed-id \
--identity-name "${IR_MSI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-image-registry:cluster-image-registry-operator \
--audience openshift

az identity federated-credential create --name "${IR_MSI_NAME}"-fed-id \
--identity-name "${IR_MSI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-image-registry:registry \
--audience openshift
```

## Create the HostedCluster
Now you should be ready to create the HostedCluster.

```shell
SA_TOKEN_ISSUER_PRIVATE_KEY_PATH="This is the value you saved in step - Creating RSA keys"

hypershift create cluster azure \
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
--oidc-issuer-url "${OIDC_ISSUER_URL}" \
--sa-token-issuer-private-key-path "${SA_TOKEN_ISSUER_PRIVATE_KEY_PATH}" \
--annotations hypershift.openshift.io/pod-security-admission-label-override=baseline \
--fips=true \
--marketplace-publisher azureopenshift \
--marketplace-offer aro4 \
--marketplace-sku aro_417 \
--marketplace-version 417.94.20240701 \
--dns-zone-rg-name os4-common \
--assign-service-principal-roles \
--managed-identities-file /Users/brcox/aro-hcp-service-principals.json
```