# Create an Azure Hosted Cluster on AKS
## General
This document describes how to set up an Azure Hosted Cluster on an AKS management cluster with an ExternalDNS setup. 
Azure HostedClusters on AKS are supported from OCP 4.18.0+.

Use the automated scripts - located in the [/contrib/managed-azure folder in the HyperShift repo](https://github.com/openshift/hypershift/tree/main/contrib/managed-azure) -
to set up the AKS management cluster, to set up external DNS, install the HyperShift Operator, create an Azure HostedCluster, destroy an Azure HostedCluster, and destroy the AKS management cluster.

!!! tip

    The scripts may have some prerequisite expectations so please review the script in whole before running it.

    Constants will also need to be updated in each script for your exact tenant and environment.

## Prerequisites
- Azure CLI
- jq
- oc (OpenShift CLI)
- kubectl
- ccoctl tool
  - The ccoctl tool provides various commands to assist with the creating and maintenance of cloud credentials from outside
    a cluster. More information on the tool can be found [here](https://github.com/openshift/cloud-credential-operator/blob/master/docs/ccoctl.md).

## Variables
Set the following variables according to your environment.

!!! tip

    Some of the variables set here, e.g. `PERSISTENT_RG_NAME`, `LOCATION`, and other DNS related variables, are 
    given as examples for the HyperShift development team, before proceeding please ensure that the variables are set 
    according to your environment.

```sh
PERSONAL_SP_NAME=<NAME_OF_PERSONAL_SP>
LOCATION="eastus"
PERSISTENT_RG_NAME="os4-common"
SP_AKS_CREDS=<PATH_TO_AKS_CREDS>
AKS_CP_MI_NAME=<MANAGED_IDENTITY_NAME_FOR_AKS_CLUSTER>
AKS_KUBELET_MI_NAME=<KUBELET_MANAGED_IDENTITY_NAME_FOR_AKS_CLUSTER>
KV_NAME=<KV_NAME>
AZURE_DISK_SP_NAME="azure-disk-$PERSONAL_SP_NAME"
AZURE_FILE_SP_NAME="azure-file-$PERSONAL_SP_NAME"
NODEPOOL_MGMT="nodepool-mgmt-$PERSONAL_SP_NAME"
CLOUD_PROVIDER_SP_NAME="cloud-provider-$PERSONAL_SP_NAME"
CNCC_NAME="cncc-$PERSONAL_SP_NAME"
CONTROL_PLANE_SP_NAME="cpo-$PERSONAL_SP_NAME"
IMAGE_REGISTRY_SP_NAME="ciro-$PERSONAL_SP_NAME"
INGRESS_SP_NAME="ingress-$PERSONAL_SP_NAME"
CP_OUTPUT_FILE=<output file for control plane service principals>
DP_OUTPUT_FILE=<output file for data plane managed identities>
AKS_CLUSTER_NAME=<AKS_CLUSTER_NAME>
AZURE_DISK_MI_NAME=<AZURE_DISK_MI_NAME>
AZURE_FILE_MI_NAME=<AZURE_FILE_MI_NAME>
IMAGE_REGISTRY_MI_NAME=<IMAGE_REGISTRY_MI_NAME>
```

You can look up `USER_ACCOUNT_ID` by searching for your account in azure portal's search box and selecting Microsoft
Entra ID.You can use value of `Object ID` as shown in azure portal.


## Steps
Note: Steps 1-8 set up the environment so that, if created in a persistent group, they can be reused for creation of
clusters in the future. Reusing these helps with reducing the managed identity quota, which has previously caused issues
for the HyperShift team's Azure tenant. We should reuse MIs and SPs wherever possible.

### 1. Retrieve User Account Details

```sh
ACCOUNT_DETAILS=$(az account show --query '{subscriptionId: id, tenantId: tenantId}' -o json)
SUBSCRIPTION_ID=$(echo "$ACCOUNT_DETAILS" | jq -r '.subscriptionId')
TENANT_ID=$(echo "$ACCOUNT_DETAILS" | jq -r '.tenantId')
```

### 2. Create Service Principal for Authentication

```sh
SP_DETAILS=$(az ad sp create-for-rbac --name "$PERSONAL_SP_NAME" --role Contributor --scopes "/subscriptions/$SUBSCRIPTION_ID" -o json)
CLIENT_ID=$(echo "$SP_DETAILS" | jq -r '.appId')
CLIENT_SECRET=$(echo "$SP_DETAILS" | jq -r '.password')

cat <<EOF > $SP_AKS_CREDS
{
  "subscriptionId": "$SUBSCRIPTION_ID",
  "tenantId": "$TENANT_ID",
  "clientId": "$CLIENT_ID",
  "clientSecret": "$CLIENT_SECRET"
}
EOF
```
!!! warning
      
    In order for your Hypershift cluster to create properly, the Microsoft Graph `Application.ReadWrite.OwnedBy` 
    permission must be added to your Service Principal and it also must be assigned to User Access Administrator at the 
    subscription level. 

    In most cases, you'll need to submit a DPTP request to have this done.

### 3. Create Managed Identities for AKS Cluster Creation

```sh
az identity create --name $AKS_CP_MI_NAME --resource-group $PERSISTENT_RG_NAME
az identity create --name $AKS_KUBELET_MI_NAME --resource-group $PERSISTENT_RG_NAME
```

```shell
export AKS_CP_MI_ID=$(az identity show --name $AKS_CP_MI_NAME --resource-group $PERSISTENT_RG_NAME --query id -o tsv)
export AKS_KUBELET_MI_ID=$(az identity show --name $AKS_KUBELET_MI_NAME --resource-group $PERSISTENT_RG_NAME --query id -o tsv)
```

### 4. Create Key Vault for Certificate Storage

```sh
export USER_ACCOUNT_ID=$(az ad signed-in-user show | jq -r .id)
az keyvault create --name $KV_NAME --resource-group $PERSISTENT_RG_NAME --location $LOCATION --enable-rbac-authorization
az role assignment create --assignee ${USER_ACCOUNT_ID} --scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${PERSISTENT_RG_NAME}/providers/Microsoft.KeyVault/vaults/${KV_NAME} --role "Key Vault Administrator"
```

### 5. Create Service Principals for the Control Plane Components

```sh
cloudProvider=$(az ad sp create-for-rbac --name "${CLOUD_PROVIDER_SP_NAME}" --create-cert --cert "${CLOUD_PROVIDER_SP_NAME}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${CLOUD_PROVIDER_SP_NAME}'}" -o json)
controlPlaneOperator=$(az ad sp create-for-rbac --name "${CONTROL_PLANE_SP_NAME}" --create-cert --cert "${CONTROL_PLANE_SP_NAME}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${CONTROL_PLANE_SP_NAME}'}" -o json)
disk=$(az ad sp create-for-rbac --name "${AZURE_DISK_SP_NAME}" --create-cert --cert "${AZURE_DISK_SP_NAME}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${AZURE_DISK_SP_NAME}'}" -o json)
file=$(az ad sp create-for-rbac --name "${AZURE_FILE_SP_NAME}" --create-cert --cert "${AZURE_FILE_SP_NAME}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${AZURE_FILE_SP_NAME}'}" -o json)
imageRegistry=$(az ad sp create-for-rbac --name "${IMAGE_REGISTRY_SP_NAME}" --create-cert --cert "${IMAGE_REGISTRY_SP_NAME}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${IMAGE_REGISTRY_SP_NAME}'}" -o json)
ingress=$(az ad sp create-for-rbac --name "${INGRESS_SP_NAME}" --create-cert --cert "${INGRESS_SP_NAME}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${INGRESS_SP_NAME}'}" -o json)
network=$(az ad sp create-for-rbac --name "${CNCC_NAME}" --create-cert --cert "${CNCC_NAME}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${CNCC_NAME}'}" -o json)
nodePoolManagement=$(az ad sp create-for-rbac --name "${NODEPOOL_MGMT}" --create-cert --cert "${NODEPOOL_MGMT}" --keyvault "${KV_NAME}" --query "{clientID: appId, certificateName: '${NODEPOOL_MGMT}'}" -o json)
```

### 6. Save Service Principal and Key Vault Details
#### Set Names

```sh
CERT_NAMES=(
    "${AZURE_DISK_SP_NAME}"
    "${AZURE_FILE_SP_NAME}"
    "${IMAGE_REGISTRY_SP_NAME}"
    "${CLOUD_PROVIDER_SP_NAME}"
    "${CNCC_NAME}"
    "${CONTROL_PLANE_SP_NAME}"
    "${INGRESS_SP_NAME}"
    "${NODEPOOL_MGMT}"
)
```

#### Create Secret JSON Files

```sh
for CERT_NAME in "${CERT_NAMES[@]}"; do
    echo "Processing certificate: $CERT_NAME"
    
    CERT_DETAILS=$(az keyvault secret show --vault-name $KV_NAME --name $CERT_NAME --query "{value: value, notBefore: attributes.notBefore, expires: attributes.expires}" -o json)
    CLIENT_SECRET=$(echo $CERT_DETAILS | jq -r '.value')
    NOT_BEFORE=$(echo $CERT_DETAILS | jq -r '.notBefore')
    NOT_AFTER=$(echo $CERT_DETAILS | jq -r '.expires')
    SP_DETAILS=$(az ad sp list --display-name $CERT_NAME --query "[0].{client_id: appId, tenant_id: appOwnerOrganizationId}" -o json)
    CLIENT_ID=$(echo $SP_DETAILS | jq -r '.client_id')
    TENANT_ID=$(echo $SP_DETAILS | jq -r '.tenant_id')

    if [[ -z "$CLIENT_ID" || -z "$TENANT_ID" ]]; then
        echo "Error: Could not retrieve client ID or tenant ID for certificate: $CERT_NAME"
        continue
    fi

    JSON_FILE="${CERT_NAME}.json"
    echo "{
        \"authentication_endpoint\": \"https://login.microsoftonline.com/\",
        \"client_id\": \"$CLIENT_ID\",
        \"client_secret\": \"$CLIENT_SECRET\",
        \"tenant_id\": \"$TENANT_ID\",
        \"not_before\": \"$NOT_BEFORE\",
        \"not_after\": \"$NOT_AFTER\"
    }" > $JSON_FILE

    echo "Created JSON file: $JSON_FILE"
done
```

#### Add Secrets to Key Vault

```sh
for CERT_NAME in "${CERT_NAMES[@]}"; do
    echo "Processing certificate: $CERT_NAME"
    JSON_FILE="${CERT_NAME}.json"

    az keyvault secret set --name "${CERT_NAME}-json" --vault-name $KV_NAME --file $JSON_FILE
done
```
These secrets are uploaded to the Key Vault, which are used by the control plane pods SecretProviderClasses to mount volumes (certificates) to there pods for authentication with specific Resource Groups. 
Note: This step sets up your Azure environment to give access to the SecretProviderClasses to mount the certificates onto the pods through the nested credentials object files in the file system of the pods (MIv3), as opposed to giving the SecretProviderClasses direct access to the certificates in the key vault (MIv2).

#### Create Managed Identities File
```sh
cat <<EOF > "${CP_OUTPUT_FILE}"
{
    "cloudProvider": {
        "certificateName": "${CLOUD_PROVIDER_SP_NAME}",
        "clientID": "$(echo "$cloudProvider" | jq -r '.clientID')",
        "credentialsSecretName": "${CLOUD_PROVIDER_SP_NAME}-json",
        "objectEncoding": "utf-8"
    },
    "controlPlaneOperator": {
        "certificateName": "${CONTROL_PLANE_SP_NAME}",
        "clientID": "$(echo "$controlPlaneOperator" | jq -r '.clientID')",
        "credentialsSecretName": "${CONTROL_PLANE_SP_NAME}-json",
        "objectEncoding": "utf-8"
    },
    "disk": {
        "certificateName": "${AZURE_DISK_SP_NAME}",
        "clientID": "$(echo "$disk" | jq -r '.clientID')",
        "credentialsSecretName": "${AZURE_DISK_SP_NAME}-json",
        "objectEncoding": "utf-8"
    },
    "file": {
        "certificateName": "${AZURE_FILE_SP_NAME}",
        "clientID": "$(echo "$file" | jq -r '.clientID')",
        "credentialsSecretName": "${AZURE_FILE_SP_NAME}-json",
        "objectEncoding": "utf-8"
    },
    "imageRegistry": {
        "certificateName": "${IMAGE_REGISTRY_SP_NAME}",
        "clientID": "$(echo "$imageRegistry" | jq -r '.clientID')",
        "credentialsSecretName": "${IMAGE_REGISTRY_SP_NAME}-json",
        "objectEncoding": "utf-8"
    },
    "ingress": {
        "certificateName": "${INGRESS_SP_NAME}",
        "clientID": "$(echo "$ingress" | jq -r '.clientID')",
        "credentialsSecretName": "${INGRESS_SP_NAME}-json",
        "objectEncoding": "utf-8"
    },
    "managedIdentitiesKeyVault": {
        "name": "${KV_NAME}",
        "tenantID": "$(az account show --query tenantId -o tsv)"
    },
    "network": {
        "certificateName": "${CNCC_NAME}",
        "clientID": "$(echo "$network" | jq -r '.clientID')",
        "credentialsSecretName": "${CNCC_NAME}-json",
        "objectEncoding": "utf-8"
    },
    "nodePoolManagement": {
        "certificateName": "${NODEPOOL_MGMT}",
        "clientID": "$(echo "$nodePoolManagement" | jq -r '.clientID')",
        "credentialsSecretName": "${NODEPOOL_MGMT}-json",
        "objectEncoding": "utf-8"
    }
}
EOF
```

### 7. Create and Save Managed Identities for the Data Plane Component
```shell
AZURE_DISK_CLIENT_ID=$(az identity create --name $AZURE_DISK_MI_NAME --resource-group $PERSISTENT_RG_NAME --query clientId -o tsv)
AZURE_FILE_CLIENT_ID=$(az identity create --name $AZURE_FILE_MI_NAME --resource-group $PERSISTENT_RG_NAME --query clientId -o tsv)
IMAGE_REGISTRY_CLIENT_ID=$(az identity create --name $IMAGE_REGISTRY_MI_NAME --resource-group $PERSISTENT_RG_NAME --query clientId -o tsv)

cat <<EOF > "${DP_OUTPUT_FILE}"
{
  "imageRegistryMSIClientID": ${IMAGE_REGISTRY_CLIENT_ID},
  "diskMSIClientID": ${AZURE_DISK_CLIENT_ID},
  "fileMSIClientID": ${AZURE_FILE_CLIENT_ID}
}
EOF
```
