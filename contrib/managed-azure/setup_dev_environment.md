# Setting Up a Development Environment

## Prerequisites

- Azure CLI
- jq
- oc (OpenShift CLI)
- kubectl

## Variables

Set the following variables according to your environment.
Note: Some of the variables set here eg persistent rg name and dns related vars are given as examples for the HyperShift team, before proceeding please ensure that the variables are set according to your environment.

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
USER_ACCOUNT_ID=<ID_OF_USER_ACCOUNT_OR_SP>
SP_OUTPUT_FILE=<OUTPUT_FILE_FOR_CREATED_SPs>
AKS_CLUSTER_RG_NAME=<AKS_CLUSTER_RG_NAME>
DNS_RECORD_NAME=<DNS_RECORD_NAME>
EXTERNAL_DNS_SP_NAME=<EXTERNAL_DNS_SP_NAME>
EXTERNAL_DNS_CREDS=<PATH_TO_FILE_WITH_DNS_CREDS>
DNS_ZONE_NAME="<DNS_RECORD_NAME>.hypershift.azure.devcluster.openshift.com"
PARENT_DNS_ZONE="hypershift.azure.devcluster.openshift.com"
PARENT_DNS_RG="os4-common"
HC_NAME=<HC_NAME>
```

You can look up `USER_ACCOUNT_ID` by searching for your account in azure portal's search box and selecting Microsoft Entra ID.You can
use value of `Object ID` as shown in azure portal.


Value of `$PARENT_DNS_ZONE` may be different for different teams. Check `os4-common` resourcegroup associated with your subscription-id for pre existing DNS zones.  If there are multiple DNS zones more than likely the parent DNS zone is the common suffix among them.

## Steps
Note: Steps 1-7 set up the environment so that if created in a persistent group they can be
reused for creation of clusters in the future. Reusing these helps with reducing the quota used which has previously
caused issues. We should reuse MIs and SPs where possible.

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
az keyvault create --name $KV_NAME --resource-group $PERSISTENT_RG_NAME --location $LOCATION --enable-rbac-authorization
az role assignment create --assignee ${USER_ACCOUNT_ID} --scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${PERSISTENT_RG_NAME}/providers/Microsoft.KeyVault/vaults/${KV_NAME} --role "Key Vault Administrator"
```

### 5. Create Service Principals

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
cat <<EOF > "${SP_OUTPUT_FILE}"
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

### 7. DNS Setup

#### Create DNS Zone

```sh
az network dns zone create --resource-group $PERSISTENT_RG_NAME --name $DNS_ZONE_NAME
```

#### Delete Existing Record Set

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

### 8. Create Resource Group for AKS Cluster

```sh
az group create --name $AKS_CLUSTER_RG_NAME --location eastus
```

### 9. Create AKS Cluster

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

### 10. Export Kubeconfig

```sh
az aks get-credentials --resource-group $AKS_CLUSTER_RG_NAME --name $AKS_CLUSTER_NAME --overwrite-existing
export AZURE_KEY_VAULT_AUTHORIZED_USER_ID=$(az aks show -n $AKS_CLUSTER_NAME -g $AKS_CLUSTER_RG_NAME | jq .addonProfiles.azureKeyvaultSecretsProvider.identity.clientId -r)
export AZURE_KEY_VAULT_AUTHORIZED_OBJECT_ID=$(az aks show -n $AKS_CLUSTER_NAME -g $AKS_CLUSTER_RG_NAME | jq .addonProfiles.azureKeyvaultSecretsProvider.identity.objectId -r)
```

### 11. Assign Role to Secret Provider

```sh
az role assignment create --assignee-object-id $AZURE_KEY_VAULT_AUTHORIZED_OBJECT_ID --role "Key Vault Secrets User" --scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/${PERSISTENT_RG_NAME} --assignee-principal-type ServicePrincipal
```

### 12. Create External DNS Secret Config File

```sh
kubectl delete secret azure-config-file --namespace "default" --ignore-not-found
kubectl create secret generic azure-config-file --namespace "default" --from-file $EXTERNAL_DNS_CREDS
```

### 13. Install Hypershift Operator

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

### 14. Create Hosted Cluster

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
--managed-identities-file $SP_OUTPUT_FILE \
--assign-service-principal-roles \
--dns-zone-rg-name $PERSISTENT_RG_NAME
```
