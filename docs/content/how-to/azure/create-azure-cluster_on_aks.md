# Create an Azure Hosted Cluster on AKS
This document describes how to set up an Azure Hosted Cluster on an AKS management cluster with an ExternalDNS setup(starting in OCP 4.17).

If you already have an existing AKS cluster up and running, you can jump to the [Setup ExternalDNS](#setup-externaldns).

There are also automated scripts to set up the AKS cluster, set up external DNS, install the HyperShift Operator, and 
create an Azure HostedCluster in the /contrib/aks folder in the HyperShift repo.

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
```

Log in to your Azure account through the CLI
`az login`

Create an Azure Resource Group
```
az group create \
--name ${AKS_RG} \
--location ${LOCATION}
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
--enable-addons azure-keyvault-secrets-provider
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
az keyvault create \
--name ${KV_NAME} \
--resource-group ${AKS_RG} \
--location ${LOCATION} \
--enable-rbac-authorization
```


## Setup ExternalDNS
First setup some constants. The resource group needs to be different from the resource group your AKS cluster is in.
```
RG="external-dns"
LOCATION="eastus"
MGMT_DNS_ZONE_NAME="blah-blah-blah.com"
EXTERNAL_DNS_NEW_SP_NAME="ExternalDnsServicePrincipal"
SERVICE_PRINCIPAL_FILEPATH="/Users/your-username/azure_mgmt.json"
```

Create an Azure resource group and your DNS zone, which will be used with ExternalDNS
```
az group create --name ${RG} --location ${LOCATION}
az network dns zone create --resource-group ${RG} --name ${MGMT_DNS_ZONE_NAME}
```

Create a service principal for the DNS zone
```
DNS_SP=$(az ad sp create-for-rbac --name ${EXTERNAL_DNS_NEW_SP_NAME})
EXTERNAL_DNS_SP_APP_ID=$(echo "$DNS_SP" | jq -r '.appId')
EXTERNAL_DNS_SP_PASSWORD=$(echo "$DNS_SP" | jq -r '.password')
```

Assign the rights for the service principal
```
DNS_ID=$(az network dns zone show --name ${MGMT_DNS_ZONE_NAME} --resource-group ${RG} --query "id" --output tsv)
az role assignment create --role "Reader" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"
az role assignment create --role "Contributor" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"
```

Create a configuration file for our service principal. Change the user path to the azure.json below as desired.
```
cat <<-EOF > ${SERVICE_PRINCIPAL_FILEPATH}
{
  "tenantId": "$(az account show --query tenantId -o tsv)",
  "subscriptionId": "$(az account show --query id -o tsv)",
  "resourceGroup": "$RG",
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
--management-key-vault-name ${KEY_VAULT_NAME} \
--management-key-vault-tenant-id ${KEY_VAULT_TENANT_ID} \
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
