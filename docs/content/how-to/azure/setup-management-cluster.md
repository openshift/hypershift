# Setup Azure Management Cluster for HyperShift

!!! note "Developer Preview in OCP 4.21"
    
    Self-managed Azure HostedClusters are available as a Developer Preview feature in OpenShift Container Platform 4.21.

This document describes how to set up DNS and install the HyperShift operator on your Azure management cluster.

## Prerequisites

- Azure CLI (`az`) installed and configured
- OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`)
- `jq` command-line JSON processor
- An Azure OpenShift management cluster

## DNS Zone Configuration

Before creating HostedClusters, you need to set up DNS zones and delegate DNS records for your clusters.

!!! note "About PERSISTENT_RG_NAME"
    In Red Hat environments, a periodic Azure resource "reaper" deletes resources that are not properly tagged or not located in an approved resource group. We frequently use the `os4-common` resource group for shared, long-lived assets (for example, public DNS zones) to avoid accidental cleanup. If you are not in Red Hat infrastructure, set `PERSISTENT_RG_NAME` to any long-lived resource group in your subscription that will not be automatically reaped, or ensure your organization’s required tags/policies are applied. The name does not have to be `os4-common`—use whatever persistent resource group fits your environment.

```bash
# Set DNS configuration variables
PARENT_DNS_RG="your-parent-dns-rg"
PARENT_DNS_ZONE="your-parent.dns.zone.com"
DNS_RECORD_NAME="your-subdomain"
PERSISTENT_RG_NAME="os4-common"  # Use persistent resource group
DNS_ZONE_NAME="your-subdomain.your-parent.dns.zone.com"

az group create \  
      --name $PERSISTENT_RG_NAME \  
      --location $LOCATION  

az network dns zone create \  
        --resource-group $PERSISTENT_RG_NAME \  
        --name $DNS_ZONE_NAME  

# Delete existing NS record if it exists
az network dns record-set ns delete \
    --resource-group $PARENT_DNS_RG \
    --zone-name $PARENT_DNS_ZONE \
    --name $DNS_RECORD_NAME -y

# Get name servers from your DNS zone
name_servers=$(az network dns zone show \
    --resource-group $PERSISTENT_RG_NAME \
    --name $DNS_ZONE_NAME \
    --query nameServers \
    --output tsv)

# Create array of name servers
ns_array=()
while IFS= read -r ns; do
    ns_array+=("$ns")
done <<< "$name_servers"

# Add NS records to parent zone
for ns in "${ns_array[@]}"; do
    az network dns record-set ns add-record \
        --resource-group $PARENT_DNS_RG \
        --zone-name $PARENT_DNS_ZONE \
        --record-set-name $DNS_RECORD_NAME \
        --nsdname "$ns"
done
```

## External DNS Service Principal Setup

Create a dedicated service principal for External DNS:

```bash
# Set External DNS configuration
EXTERNAL_DNS_NEW_SP_NAME="ExternalDnsServicePrincipal"
SERVICE_PRINCIPAL_FILEPATH="/path/to/azure_mgmt.json"
PERSISTENT_RG_NAME="hypershift-shared-resources"  # Use persistent resource group

# Create service principal for External DNS
DNS_SP=$(az ad sp create-for-rbac --name ${EXTERNAL_DNS_NEW_SP_NAME})
EXTERNAL_DNS_SP_APP_ID=$(echo "$DNS_SP" | jq -r '.appId')
EXTERNAL_DNS_SP_PASSWORD=$(echo "$DNS_SP" | jq -r '.password')

# Get DNS zone ID
DNS_ID=$(az network dns zone show \
    --name ${DNS_ZONE_NAME} \
    --resource-group ${PERSISTENT_RG_NAME} \
    --query "id" \
    --output tsv)

# Assign roles to the service principal
az role assignment create \
    --role "Reader" \
    --assignee "${EXTERNAL_DNS_SP_APP_ID}" \
    --scope "${DNS_ID}"

az role assignment create \
    --role "Contributor" \
    --assignee "${EXTERNAL_DNS_SP_APP_ID}" \
    --scope "${DNS_ID}"

# Create Azure credentials file
cat <<-EOF > ${SERVICE_PRINCIPAL_FILEPATH}
{
  "tenantId": "$(az account show --query tenantId -o tsv)",
  "subscriptionId": "$(az account show --query id -o tsv)",
  "resourceGroup": "$PERSISTENT_RG_NAME",
  "aadClientId": "$EXTERNAL_DNS_SP_APP_ID",
  "aadClientSecret": "$EXTERNAL_DNS_SP_PASSWORD"
}
EOF

# Create Kubernetes secret for Azure credentials
kubectl delete secret/azure-config-file --namespace "default" || true
kubectl create secret generic azure-config-file \
    --namespace "default" \
    --from-file ${SERVICE_PRINCIPAL_FILEPATH}
```

## HyperShift Operator Installation

Install the HyperShift operator with External DNS configuration on your Azure management cluster:

```bash
# Set installation variables
TAG="self-managed-2025-09-04-1"  # Use appropriate tag
PULL_SECRET="/path/to/pull-secret.json"
HYPERSHIFT_BINARY_PATH="/path/to/hypershift/bin"

# Install HyperShift operator
${HYPERSHIFT_BINARY_PATH}/hypershift install \
    --external-dns-provider=azure \
    --external-dns-credentials ${SERVICE_PRINCIPAL_FILEPATH} \
    --pull-secret ${PULL_SECRET} \
    --external-dns-domain-filter ${DNS_ZONE_NAME} \
    --limit-crd-install Azure \
    --hypershift-image quay.io/hypershift/hypershift:${TAG}
```

!!! important "HyperShift Image"
    
    Replace the `--hypershift-image` value with the appropriate image for your environment. Or remove this flag completely if not using a custom HyperShift Operator image.

## Verification

After installation, verify that the HyperShift operator and External DNS are running:

```bash
# Check HyperShift operator and ExternalDNS pods
oc get pods -n hypershift
```

## Next Steps

Once the management cluster is set up, you can proceed to:

- [Create a Self-Managed Azure HostedCluster](create-self-managed-azure-cluster.md)