# Create an Azure Hosted Cluster on AKS
## General
This document describes how to set up an Azure Hosted Cluster on an AKS management cluster with an ExternalDNS setup. 
Azure HostedClusters on AKS are supported from OCP 4.18.0+.

There are also automated scripts - located in the [/contrib/managed-azure folder in the HyperShift repo](https://github.com/openshift/hypershift/tree/main/contrib/managed-azure) -
to set up the AKS cluster, to set up external DNS, install the HyperShift Operator, and create an Azure HostedCluster.

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
Note: Some of the variables set here, e.g. persistent rg name and dns related vars, are given as examples for the
HyperShift team, before proceeding please ensure that the variables are set according to your environment.

```sh
PERSONAL_SP_NAME=<NAME_OF_PERSONAL_SP>
LOCATION="eastus"
PULL_SECRET=<PATH_TO_PULLSECRET>
PERSISTENT_RG_NAME="os4-common"
SP_AKS_CREDS=<PATH_TO_AKS_CREDS>
RELEASE_IMAGE=<OCP_PAYLOAD_RELEASE_IMAGE>
AKS_CP_MI_NAME=<MANAGED_IDENTITY_NAME_FOR_AKS_CLUSTER>
AKS_KUBELET_MI_NAME=<KUBELET_MANAGED_IDENTITY_NAME_FOR_AKS_CLUSTER>
KV_NAME=<KV_NAME>
AZURE_DISK_SP_NAME="azure-disk-<PERSONAL_SP_NAME>"
AZURE_FILE_SP_NAME="azure-file-<PERSONAL_SP_NAME>"
NODEPOOL_MGMT="nodepool-mgmt-<PERSONAL_SP_NAME>"
CLOUD_PROVIDER_SP_NAME="cloud-provider-<PERSONAL_SP_NAME>"
CNCC_NAME="cncc-<PERSONAL_SP_NAME>"
CONTROL_PLANE_SP_NAME="cpo-<PERSONAL_SP_NAME>"
IMAGE_REGISTRY_SP_NAME="ciro-<PERSONAL_SP_NAME>"
INGRESS_SP_NAME="ingress-<PERSONAL_SP_NAME>"
CP_OUTPUT_FILE=<output file for control plane service principals>
DP_OUTPUT_FILE=<output file for data plane managed identities>
AKS_CLUSTER_RG_NAME=<AKS_CLUSTER_RG_NAME>
DNS_RECORD_NAME=<DNS_RECORD_NAME>
EXTERNAL_DNS_SP_NAME=<EXTERNAL_DNS_SP_NAME>
EXTERNAL_DNS_CREDS=<PATH_TO_FILE_WITH_DNS_CREDS>
DNS_ZONE_NAME="<DNS_RECORD_NAME>.hypershift.azure.devcluster.openshift.com"
PARENT_DNS_ZONE="hypershift.azure.devcluster.openshift.com"
PARENT_DNS_RG="os4-common"
HC_NAME=<HC_NAME>
```

You can look up `USER_ACCOUNT_ID` by searching for your account in azure portal's search box and selecting Microsoft
Entra ID.You can use value of `Object ID` as shown in azure portal.


Value of `$PARENT_DNS_ZONE` may be different for different teams. Check `os4-common` resource group associated with your
subscription-id for pre-existing DNS zones.  If there are multiple DNS zones more than likely the parent DNS zone is the
common suffix among them.

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

```sh
cat <<EOF > "${CP_OUTPUT_FILE}"
{
    "cloudProvider": ${cloudProvider},
    "controlPlaneOperator": ${controlPlaneOperator},
    "disk": ${disk},
    "file": ${file},
    "imageRegistry": ${imageRegistry},
    "ingress": ${ingress},
    "managedIdentitiesKeyVault": {
        "name": "${KV_NAME}",
        "tenantID": "$(az account show --query tenantId -o tsv)"
    },
    "network": ${network},
    "nodePoolManagement": ${nodePoolManagement}
}
EOF
```

### 7. Create and Save Managed Identities for the Data Plane Component
```shell
AZURE_DISK_MI_NAME="azure-disk-dp-mi"
AZURE_FILE_MI_NAME="azure-file-dp-mi"
IMAGE_REGISTRY_MI_NAME="image-registry-dp-mi"

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

### 8. DNS Setup
#### Create DNS Zone

```sh
az network dns zone create --resource-group $PERSISTENT_RG_NAME --name $DNS_ZONE_NAME
```

#### Delete Any Existing Record Set

```sh
az network dns record-set ns delete --resource-group $PARENT_DNS_RG --zone-name $PARENT_DNS_ZONE --name $DNS_RECORD_NAME -y
```

#### Get Name Servers for DNS Zone

```sh
name_servers=$(az network dns zone show --resource-group $PERSISTENT_RG_NAME --name $DNS_ZONE_NAME --query nameServers --output tsv)
ns_array=()
while IFS= read -r ns; do
    ns_array+=("$ns")
done <<< "$name_servers"
```

#### Add Name Servers to Parent DNS Zone

```sh
for ns in "${ns_array[@]}"; do
    az network dns record-set ns add-record --resource-group $PARENT_DNS_RG --zone-name $PARENT_DNS_ZONE --record-set-name $DNS_RECORD_NAME --nsdname "$ns"
done
```

#### Create Service Principal for DNS

```sh
DNS_SP=$(az ad sp create-for-rbac --name $EXTERNAL_DNS_SP_NAME)
EXTERNAL_DNS_SP_APP_ID=$(echo "$DNS_SP" | jq -r '.appId')
EXTERNAL_DNS_SP_PASSWORD=$(echo "$DNS_SP" | jq -r '.password')
```

#### Assign Rights to Service Principal

```sh
DNS_ID=$(az network dns zone show --name ${DNS_ZONE_NAME} --resource-group ${PERSISTENT_RG_NAME} --query "id" --output tsv)
az role assignment create --role "Reader" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"
az role assignment create --role "Contributor" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"
```

Note: if your DNS Zone is not in the same resource group where the parent DNS zone is located, you will need to add
these same role assignments to the resource group where the parent DNS zone is located.

#### Create DNS Credentials for AKS

```sh
cat <<-EOF > $EXTERNAL_DNS_CREDS
{
"tenantId": "$(az account show --query tenantId -o tsv)",
"subscriptionId": "$(az account show --query id -o tsv)",
"resourceGroup": "$PERSISTENT_RG_NAME",
"aadClientId": "$EXTERNAL_DNS_SP_APP_ID",
"aadClientSecret": "$EXTERNAL_DNS_SP_PASSWORD"
}
EOF
```

### 9. Create Resource Group for AKS Cluster

```sh
az group create --name $AKS_CLUSTER_RG_NAME --location eastus
```

### 10. Create AKS Cluster

```sh
az aks create \
    --resource-group $AKS_CLUSTER_RG_NAME \
    --name $AKS_CLUSTER_NAME \
    --node-count 3 \
    --generate-ssh-keys \
    --load-balancer-sku standard \
    --os-sku AzureLinux \
    --node-vm-size Standard_D4s_v4 \
    --enable-addons azure-keyvault-secrets-provider \
    --enable-fips-image \
    --enable-cluster-autoscaler \
    --min-count 2 \
    --max-count 6 \
    --enable-secret-rotation \
    --rotation-poll-interval 1m \
    --kubernetes-version 1.31.1 \
    --assign-identity $AKS_CP_MI_ID \
    --assign-kubelet-identity $AKS_KUBELET_MI_ID
```

### 11. Export Kubeconfig

```sh
az aks get-credentials --resource-group $AKS_CLUSTER_RG_NAME --name $AKS_CLUSTER_NAME --overwrite-existing
export AZURE_KEY_VAULT_AUTHORIZED_USER_ID=$(az aks show -n $AKS_CLUSTER_NAME -g $AKS_CLUSTER_RG_NAME | jq .addonProfiles.azureKeyvaultSecretsProvider.identity.clientId -r)
export AZURE_KEY_VAULT_AUTHORIZED_OBJECT_ID=$(az aks show -n $AKS_CLUSTER_NAME -g $AKS_CLUSTER_RG_NAME | jq .addonProfiles.azureKeyvaultSecretsProvider.identity.objectId -r)
```

### 12. Assign Role to Secret Provider

```sh
az role assignment create --assignee-object-id $AZURE_KEY_VAULT_AUTHORIZED_OBJECT_ID --role "Key Vault Secrets User" --scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${PERSISTENT_RG_NAME} --assignee-principal-type ServicePrincipal
```

### 13. Create External DNS Secret Config File

```sh
kubectl delete secret azure-config-file --namespace "default" --ignore-not-found
kubectl create secret generic azure-config-file --namespace "default" --from-file $EXTERNAL_DNS_CREDS
```

### 14. Install Hypershift Operator

```sh
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_podmonitors.yaml
oc apply -f https://raw.githubusercontent.com/openshift/api/master/route/v1/zz_generated.crd-manifests/routes-Default.crd.yaml

hypershift install \
    --enable-conversion-webhook=false \
    --external-dns-provider=azure \
    --external-dns-credentials $EXTERNAL_DNS_CREDS \
    --pull-secret $PULL_SECRET \
    --external-dns-domain-filter $DNS_ZONE_NAME \
    --managed-service ARO-HCP \
    --aro-hcp-key-vault-users-client-id $AZURE_KEY_VAULT_AUTHORIZED_USER_ID \
    --tech-preview-no-upgrade
```

### 15. Create RSA keys
Follow the instructions [here](https://github.com/openshift/cloud-credential-operator/blob/master/docs/ccoctl.md#creating-rsa-keys-1)
to run this command. It should look something like this:
```shell
% ./ccoctl azure create-key-pair
2024/11/14 12:57:29 Generating RSA keypair
2024/11/14 12:57:30 Writing private key to /Users/username/cloud-credential-operator/serviceaccount-signer.private
2024/11/14 12:57:30 Writing public key to /Users/username/cloud-credential-operator/serviceaccount-signer.public
2024/11/14 12:57:30 Copying signing key for use by installer
```

Save the public and private key path off as you will need it in the next steps. We will refer to them as
`SA_TOKEN_ISSUER_PUBLIC_KEY_PATH` and `SA_TOKEN_ISSUER_PRIVATE_KEY_PATH` in the rest of this guide.

### 16. Create the OIDC Issuer URL and ServiceAccount Keys
Follow the instructions [here](https://github.com/openshift/cloud-credential-operator/blob/master/docs/ccoctl.md#creating-openid-connect-issuer)
to run this command.

The NAME provided in the ccoctl tool below can only be alphanumerical characters and must be between 3 and 24 characters 
in length.

If you are using `os4-common` for the PERSISTENT_RG_NAME, this is in the `centralus` region.

```shell
NAME="wiOIDC"
PERSISTENT_RG_LOCATION="centralus"

./ccoctl azure create-oidc-issuer \
--oidc-resource-group-name $PERSISTENT_RG_NAME \
--tenant-id $TENANT_ID \
--region ${PERSISTENT_RG_LOCATION} \
--name $NAME \
--subscription-id $SUBSCRIPTION_ID \
--public-key-file $SA_TOKEN_ISSUER_PUBLIC_KEY_PATH
```

Save your Issuer URL off as you will need it in the next step, let's call it `OIDC_ISSUER_URL`.

### 17. Create Federated Identities for Workload Identities

```shell
az identity federated-credential create --name "${AZURE_DISK_MI_NAME}"-fed-id \
--identity-name "${AZURE_DISK_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-node-sa \
--audience openshift

az identity federated-credential create --name "${AZURE_FILE_MI_NAME}"-fed-id \
--identity-name "${AZURE_FILE_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-node-sa \
--audience openshift

az identity federated-credential create --name "${IMAGE_REGISTRY_MI_NAME}"-fed-id \
--identity-name "${IMAGE_REGISTRY_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-image-registry:cluster-image-registry-operator \
--audience openshift

az identity federated-credential create --name "${IMAGE_REGISTRY_MI_NAME}"-fed-id \
--identity-name "${IMAGE_REGISTRY_MI_NAME}" \
--resource-group "${PERSISTENT_RG_NAME}" \
--issuer "${OIDC_ISSUER_URL}" \
--subject system:serviceaccount:openshift-image-registry:registry \
--audience openshift
```

### 18. Create Hosted Cluster

```sh
hypershift create cluster azure \
--name $HC_NAME \
--azure-creds $SP_AKS_CREDS \
--location eastus \
--node-pool-replicas 2 \
--base-domain $PARENT_DNS_ZONE \
--pull-secret $PULL_SECRET \
--generate-ssh \
--release-image $RELEASE_IMAGE \
--external-dns-domain $DNS_ZONE_NAME \
--annotations hypershift.openshift.io/pod-security-admission-label-override=baseline \
--fips=true \
--marketplace-publisher azureopenshift \
--marketplace-offer aro4 \
--marketplace-sku aro_417 \
--marketplace-version 417.94.20240701 \
--assign-service-principal-roles \
--dns-zone-rg-name $PERSISTENT_RG_NAME \
--oidc-issuer-url "${OIDC_ISSUER_URL}" \
--sa-token-issuer-private-key-path "${SA_TOKEN_ISSUER_PRIVATE_KEY_PATH}" \
--managed-identities-file $CP_OUTPUT_FILE \
--data-plane-identities-file $DP_OUTPUT_FILE
```

### 19. Deleting the Azure Hosted Cluster
You can delete the cluster by using the following command:
```shell
${HYPERSHIFT_BINARY_PATH}/hypershift destroy cluster azure \
--name $CLUSTER_NAME \
--azure-creds $ASP_AKS_CREDS
```
