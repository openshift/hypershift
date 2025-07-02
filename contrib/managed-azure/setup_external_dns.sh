#!/bin/bash
set -x

# Constants
RG="external-dns"
LOCATION="eastus"
EXTERNAL_DNS_NEW_SP_NAME="ExternalDnsServicePrincipal"
SERVICE_PRINCIPAL_FILEPATH="/Users/your-username/azure_mgmt.json"

# These are HyperShift specific. These values may need adjusted based on your Azure tenant.
DNS_RECORD_NAME="<your-dns-record-name>"
PARENT_DNS_RG="os4-common"

########################################################################################################################
# Tip - the value of `$PARENT_DNS_ZONE` may be different for different teams. Check `os4-common` resource group 
# associated with your subscription-id for pre-existing DNS zones.  If there are multiple DNS zones more than likely 
# the parent DNS zone is the common suffix among them.
########################################################################################################################

PARENT_DNS_ZONE="hypershift.azure.devcluster.openshift.com"
DNS_ZONE_NAME="$DNS_RECORD_NAME.hypershift.azure.devcluster.openshift.com"

# Delete any pre-existing DNS Recordsets
az network dns record-set ns delete --resource-group $PARENT_DNS_RG --zone-name $PARENT_DNS_ZONE --name $DNS_RECORD_NAME -y

# Get Name Servers for DNS Zone
name_servers=$(az network dns zone show --resource-group $PERSISTENT_RG_NAME --name $DNS_ZONE_NAME --query nameServers --output tsv)
ns_array=()
while IFS= read -r ns; do
    ns_array+=("$ns")
done <<< "$name_servers"

for ns in "${ns_array[@]}"; do
    az network dns record-set ns add-record --resource-group $PARENT_DNS_RG --zone-name $PARENT_DNS_ZONE --record-set-name $DNS_RECORD_NAME --nsdname "$ns"
done

# Creating a service principal
DNS_SP=$(az ad sp create-for-rbac --name ${EXTERNAL_DNS_NEW_SP_NAME})
EXTERNAL_DNS_SP_APP_ID=$(echo "$DNS_SP" | jq -r '.appId')
EXTERNAL_DNS_SP_PASSWORD=$(echo "$DNS_SP" | jq -r '.password')

# Assign the rights for the service principal
DNS_ID=$(az network dns zone show --name ${DNS_ZONE_NAME} --resource-group ${PERSISTENT_RG_NAME} --query "id" --output tsv)
az role assignment create --role "Reader" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"
az role assignment create --role "Contributor" --assignee "${EXTERNAL_DNS_SP_APP_ID}" --scope "${DNS_ID}"

########################################################################################################################
# Tip - If your DNS Zone is not in the same resource group where the parent DNS zone is located, you will need to add 
# these same role assignments to the resource group where the parent DNS zone is located.
########################################################################################################################

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