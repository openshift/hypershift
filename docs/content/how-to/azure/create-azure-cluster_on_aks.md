# Create an Azure Hosted Cluster on AKS
## General
This document describes how to set up an Azure Hosted Cluster on an AKS management cluster with an ExternalDNS setup(implemented in OCP 4.17).

If you already have an existing AKS cluster up and running, you can jump to the [Setup ExternalDNS](#setup-externaldns).

There are also automated scripts - located in the [/contrib/managed-azure folder in the HyperShift repo](https://github.com/openshift/hypershift/tree/main/contrib/managed-azure) -
to set up the AKS cluster, to set up external DNS, install the HyperShift Operator, and create an Azure HostedCluster.

## Prerequisites
Obtain the az cli. See [this](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli) for instructions.

To create an AKS cluster, first set up your constants. The OBJECT_ID can be obtained from searching your username in
Azure and pulling up your user profile.
```
PREFIX="<your-prefix-here>"
AKS_RG=${PREFIX}"-aks-rg"
LOCATION="eastus"
AKS_CLUSTER_NAME=${PREFIX}"-aks-cluster"
SUBSCRIPTION_ID="<your-subscription-id-here>"
KV_NAME="<your-key-vault-name-here>"
OBJECT_ID="<your-object-id>"
AKS_CP_MI_NAME="${PREFIX}-aks-cp-mi"
AKS_KUBELET_MI_NAME="${PREFIX}-aks-kubelet-mi"
```

Log in to your Azure account through the CLI
`az login`

Create an Azure resource group to create your AKS management cluster in
```
az group create \
--name ${AKS_RG} \
--location ${LOCATION}
```

Create managed identities to reuse for the AKS clusters
```shell
az identity create --name $AKS_CP_MI_NAME --resource-group $AKS_RG
az identity create --name $AKS_KUBELET_MI_NAME --resource-group $AKS_RG
```

Create an AKS Cluster
```
az aks create \
--resource-group ${AKS_RG} \
--name ${AKS_CLUSTER_NAME} \
--node-count 2 \
--generate-ssh-keys \
--load-balancer-sku standard \
--os-sku AzureLinux \
--node-vm-size Standard_D4s_v4 \
--enable-fips-image \
--enable-addons azure-keyvault-secrets-provider \
--kubernetes-version 1.31.1 \
--enable-secret-rotation \
--rotation-poll-interval 1m \
--assign-identity $AKS_CP_MI_ID \
--assign-kubelet-identity $AKS_KUBELET_MI_ID
```

Create a key vault on the AKS cluster
```
az keyvault create \
--name ${KV_NAME} \
--resource-group ${AKS_RG} \
--location ${LOCATION} \
--enable-rbac-authorization
```

Save the key vault's user ID and object ID for later use
```
AZURE_KEY_VAULT_AUTHORIZED_USER_ID=$(az aks show -n ${AKS_CLUSTER_NAME} -g ${AKS_RG} | jq .addonProfiles.azureKeyvaultSecretsProvider.identity.clientId -r)
AZURE_KEY_VAULT_AUTHORIZED_OBJECT_ID=$(az aks show -n ${AKS_CLUSTER_NAME} -g ${AKS_RG} | jq .addonProfiles.azureKeyvaultSecretsProvider.identity.objectId -r)
```

Give the managed identity created on the AKS cluster for the key vault the "Key Vault Secrets" role
```
az role assignment create \
--assignee-object-id "${AZURE_KEY_VAULT_AUTHORIZED_OBJECT_ID}" \
--role "Key Vault Secrets User" \
--scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/"${AKS_RG}" \
--assignee-principal-type ServicePrincipal
```

Associate your object ID with the Key Vault. This is so you can use your profile with the Hypershift CLI to create
certificates in the key vault.
```
az role assignment create \
--assignee ${OBJECT_ID} \
--scope /subscriptions/${SUBSCRIPTION_ID}/resourceGroups/"${AKS_RG}"/providers/Microsoft.KeyVault/vaults/${KV_NAME} \
--role "Key Vault Administrator"
```

Finally, get your kubeconfig to your AKS cluster
```
az aks get-credentials \
    --resource-group ${AKS_RG} \
    --name ${AKS_CLUSTER_NAME} \
    --overwrite-existing
```

## Creating Service Principals for Managed Identities
Define Service Principals for Managed Identities
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

Create service principals and capture app IDs
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

Save service principal IDs and certificate names to a JSON file
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
        "tenantID": "${KV_TENANT_ID}"
    }
}
EOF
```

## Setup ExternalDNS
First setup some constants. The resource group needs to be different from the resource group your AKS cluster is in.
```
DNS_RG="external-dns"
LOCATION="eastus"
MGMT_DNS_ZONE_NAME="blah.hypershift.azure.devcluster.openshift.com"
DNS_RECORD_NAME="blah"
EXTERNAL_DNS_NEW_SP_NAME="ExternalDnsServicePrincipal"
SERVICE_PRINCIPAL_FILEPATH="/Users/your-username/azure_mgmt.json"
```

Create a DNS Zone in Azure
```
az network dns zone create --resource-group $DNS_RG --name $MGMT_DNS_ZONE_NAME
```

Create Service Principal for DNS
```
DNS_SP=$(az ad sp create-for-rbac --name EXTERNAL_DNS_NEW_SP_NAME)
EXTERNAL_DNS_SP_APP_ID=$(echo "$DNS_SP" | jq -r '.appId')
EXTERNAL_DNS_SP_PASSWORD=$(echo "$DNS_SP" | jq -r '.password')
```

Assign Rights to Service Principal
```
DNS_ID=$(az network dns zone show --name ${DNS_ZONE_NAME} --resource-group ${DNS_RG} --query "id" --output tsv)
az role assignment create --role "Reader" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"
az role assignment create --role "Contributor" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"
```

Create DNS Credentials for AKS
```
cat <<-EOF > $SERVICE_PRINCIPAL_FILEPATH
{
"tenantId": "$(az account show --query tenantId -o tsv)",
"subscriptionId": "$(az account show --query id -o tsv)",
"resourceGroup": "$DNS_RG",
"aadClientId": "$EXTERNAL_DNS_SP_APP_ID",
"aadClientSecret": "$EXTERNAL_DNS_SP_PASSWORD"
}
EOF
```

Create an ExternalDNS needed secret with the azure.json configuration file
```
kubectl delete secret/azure-config-file --namespace "default"
kubectl create secret generic azure-config-file --namespace "default" --from-file ${SERVICE_PRINCIPAL_FILEPATH}
```

## Installing the HyperShift Operator
Make sure you add a record in your ExternalDNS provider from your @ records from your Azure DNS Zone.

Set up some constants
```
MGMT_DNS_ZONE_NAME="blah-blah-blah.com"
PULL_SECRET="/Users/your-username/all-the-pull-secrets.json"
SERVICE_PRINCIPAL_FILEPATH="/Users/your-username/azure_mgmt.json"
```

Apply some CRDs that are missing
```
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_servicemonitors.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_prometheusrules.yaml
oc apply -f https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/main/example/prometheus-operator-crd/monitoring.coreos.com_podmonitors.yaml
oc apply -f https://raw.githubusercontent.com/openshift/api/master/route/v1/zz_generated.crd-manifests/routes-Default.crd.yaml
```

Install the HyperShift operator
```
${HYPERSHIFT_BINARY_PATH}/hypershift install \
--enable-conversion-webhook=false \
--external-dns-provider=azure \
--external-dns-credentials ${SERVICE_PRINCIPAL_FILEPATH} \
--pull-secret ${PULL_SECRET} \
--external-dns-domain-filter ${MGMT_DNS_ZONE_NAME} \
--managed-service ARO-HCP \
--aro-hcp-key-vault-users-client-id "${AZURE_KEY_VAULT_AUTHORIZED_USER_ID}" \
--tech-preview-no-upgrade
```

`tech-preview-no-upgrade` and `aro-hcp-key-vault-users-client-id` are needed to enable managed identity on Azure HostedClusters.

## Setup Azure Infra
First setup some constants
```
MANAGED_RG_NAME="managed-aks-rg"
CUSTOMER_RG_NAME="customer-aks-rg"
CUSTOMER_NSG_RG_NAME="customer-nsg-rg"
CUSTOMER_VNET_NAME="customer-vnet"
CUSTOMER_VNET_SUBNET1="customer-subnet-1"
CUSTOMER_NSG="customer-nsg"
```

### Create a managed resource group
```
az group create --name "${MANAGED_RG_NAME}" --location ${LOCATION}
```

### Create a customer VNET resource group
```
az group create --name "${CUSTOMER_RG_NAME}" --location ${LOCATION}
```

### Create a customer NSG resource group
```
az group create --name "${CUSTOMER_NSG_RG_NAME}" --location ${LOCATION}
```

### Create a customer network security group
```
az network nsg create --resource-group "${CUSTOMER_NSG_RG_NAME}" --name "${CUSTOMER_NSG}"
```

### Get customer nsg ID
```
GetNsgID=$(az network nsg list --query "[?name=='${CUSTOMER_NSG}'].id" -o tsv)
```

### Create customer VNET in the customer resource group
```
az network vnet create \
--name "${CUSTOMER_VNET_NAME}" \
--resource-group "${CUSTOMER_RG_NAME}" \
--address-prefix 10.0.0.0/16 \
--subnet-name "${CUSTOMER_VNET_SUBNET1}" \
--subnet-prefixes 10.0.0.0/24 \
--nsg "${GetNsgID}"
```

### Get customer vnet ID
```
GetVnetID=$(az network vnet list --query "[?name=='${CUSTOMER_VNET_NAME}'].id" -o tsv)
```

### Get customer subnet ID
```
GetSubnetID=$(az network vnet subnet show --vnet-name "${CUSTOMER_VNET_NAME}" --name "${CUSTOMER_VNET_SUBNET1}" --resource-group "${CUSTOMER_RG_NAME}" --query id --output tsv)
```

## Create the Azure Hosted Cluster
Run the following command to create an Azure Hosted Cluster
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
--dns-zone-rg-name $DNS_RG \
--fips=true \
--marketplace-publisher azureopenshift \
--marketplace-offer aro4 \
--marketplace-sku aro_417 \
--marketplace-version 417.94.20240701
```

## Deleting the Azure Hosted Cluster
You can delete the cluster by using the following command:
```
${HYPERSHIFT_BINARY_PATH}/hypershift destroy cluster azure \
--name $CLUSTER_NAME \
--azure-creds $AZURE_CREDS \
--resource-group-name ${MANAGED_RG_NAME}
```
