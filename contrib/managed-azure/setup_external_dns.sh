#!/bin/bash
set -x

# Constants
RG="external-dns"
LOCATION="eastus"
MGMT_DNS_ZONE_NAME="blah-blah-blah.com"
EXTERNAL_DNS_NEW_SP_NAME="ExternalDnsServicePrincipal"
SERVICE_PRINCIPAL_FILEPATH="/Users/your-username/azure_mgmt.json"

# Create Azure RG and DNS Zone
az group create --name ${RG} --location ${LOCATION}
az network dns zone create --resource-group ${RG} --name ${MGMT_DNS_ZONE_NAME}

# Creating a service principal
DNS_SP=$(az ad sp create-for-rbac --name ${EXTERNAL_DNS_NEW_SP_NAME})
EXTERNAL_DNS_SP_APP_ID=$(echo "$DNS_SP" | jq -r '.appId')
EXTERNAL_DNS_SP_PASSWORD=$(echo "$DNS_SP" | jq -r '.password')

# Assign the rights for the service principal
DNS_ID=$(az network dns zone show --name ${MGMT_DNS_ZONE_NAME} --resource-group ${RG} --query "id" --output tsv)
az role assignment create --role "Reader" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"
az role assignment create --role "Contributor" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"

# Creating a configuration file for our service principal
cat <<-EOF > ${SERVICE_PRINCIPAL_FILEPATH}
{
  "tenantId": "$(az account show --query tenantId -o tsv)",
  "subscriptionId": "$(az account show --query id -o tsv)",
  "resourceGroup": "$RG",
  "aadClientId": "$EXTERNAL_DNS_SP_APP_ID",
  "aadClientSecret": "$EXTERNAL_DNS_SP_PASSWORD"
}
EOF

# Create needed secret with azure_mgmt.json
kubectl delete secret/azure-config-file --namespace "default"
kubectl create secret generic azure-config-file --namespace "default" --from-file ${SERVICE_PRINCIPAL_FILEPATH}

set +x