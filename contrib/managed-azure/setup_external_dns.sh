#!/bin/bash
set -x

# Prerequisites.
LOCATION="${LOCATION:-}"
PARENT_DNS_RG_NAME="${PERSISTENT_RG_NAME:-}"
PARENT_DNS_ZONE="${PARENT_DNS_ZONE:-}"
RG="${PERSISTENT_RG_NAME:-}"

# Local.
MGMT_DNS_ZONE_NAME="${DNS_RECORD_NAME}.${PARENT_DNS_ZONE}"
EXTERNAL_DNS_NEW_SP_NAME="${PREFIX}-ExternalDnsServicePrincipal"
EXTERNAL_DNS_SERVICE_PRINCIPAL_FILEPATH="${EXTERNAL_DNS_SERVICE_PRINCIPAL_FILEPATH:-}"

# Create DNS Zone.
az network dns zone create --resource-group $RG --name $MGMT_DNS_ZONE_NAME
# Delete Any Existing Record Set from parent zone.
az network dns record-set ns delete --resource-group $PARENT_DNS_RG_NAME --zone-name $PARENT_DNS_ZONE --name ${DNS_RECORD_NAME} -y

# Add Name Servers to Parent DNS Zone
name_servers=$(az network dns zone show --resource-group $RG --name $MGMT_DNS_ZONE_NAME --query nameServers --output tsv)
ns_array=()
while IFS= read -r ns; do
    ns_array+=("$ns")
done <<< "$name_servers"

for ns in "${ns_array[@]}"; do
    az network dns record-set ns add-record --resource-group $PARENT_DNS_RG_NAME --zone-name $PARENT_DNS_ZONE --record-set-name $DNS_RECORD_NAME --nsdname "$ns"
done

# Creating a service principal
DNS_SP=$(az ad sp create-for-rbac --name ${EXTERNAL_DNS_NEW_SP_NAME})
EXTERNAL_DNS_SP_APP_ID=$(echo "$DNS_SP" | jq -r '.appId')
EXTERNAL_DNS_SP_PASSWORD=$(echo "$DNS_SP" | jq -r '.password')

# Assign the rights for the service principal
DNS_ID=$(az network dns zone show --name ${MGMT_DNS_ZONE_NAME} --resource-group ${RG} --query "id" --output tsv)
az role assignment create --role "Reader" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"
az role assignment create --role "Contributor" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"

# Creating a configuration file for our service principal
cat <<-EOF > ${EXTERNAL_DNS_SERVICE_PRINCIPAL_FILEPATH}
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
kubectl create secret generic azure-config-file --namespace "default" --from-file ${EXTERNAL_DNS_SERVICE_PRINCIPAL_FILEPATH}

set +x