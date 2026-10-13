#!/bin/bash

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# User variables
if [[ ! -f "./user-vars.sh" ]]; then
  echo "Error: ./user-vars.sh not found. Please create it before running this script."
  exit 1
fi
source "./user-vars.sh"

# Internal Variables shared by the scripts
# We export the values here so any script can be commented out from setup_all.sh without breaking other scripts
export PERSISTENT_RG_NAME="os4-common"
export PERSISTENT_RG_LOCATION="centralus"

# Parent DNS zone - check `os4-common` resource group for pre-existing DNS zones
export PARENT_DNS_ZONE="hypershift.azure.devcluster.openshift.com"
export PARENT_DNS_RG="os4-common"
export DNS_ZONE_NAME="${DNS_RECORD_NAME}.${PARENT_DNS_ZONE}"

# Get Azure account details
ACCOUNT_DETAILS=$(az account show --query '{subscriptionId: id, tenantId: tenantId}' -o json)
export SUBSCRIPTION_ID=$(echo "$ACCOUNT_DETAILS" | jq -r '.subscriptionId')
export TENANT_ID=$(echo "$ACCOUNT_DETAILS" | jq -r '.tenantId')

# External DNS configuration (for compatibility with managed Azure script)
export EXTERNAL_DNS_SP_NAME="ExternalDnsServicePrincipal-${PREFIX}"
export EXTERNAL_DNS_SERVICE_PRINCIPAL_FILEPATH="./external_DNS_SA.json"
export MGMT_DNS_ZONE_NAME="${DNS_ZONE_NAME}"  # Alias for managed Azure compatibility
export RG="${PERSISTENT_RG_NAME}"  # Alias for managed Azure compatibility
export PARENT_DNS_RG_NAME="${PARENT_DNS_RG}"  # Alias for managed Azure compatibility

# Azure infrastructure
export LOCATION="${LOCATION:-eastus}"
export MANAGED_RG_NAME="${PREFIX}-managed-rg"
export CUSTOMER_VNET_RG_NAME="${PREFIX}-customer-vnet-rg"
export CUSTOMER_NSG_RG_NAME="${PREFIX}-customer-nsg-rg"
export CUSTOMER_VNET_NAME="${PREFIX}-customer-vnet"
export CUSTOMER_VNET_SUBNET1="${PREFIX}-customer-subnet-1"
export CUSTOMER_NSG="${PREFIX}-customer-nsg"

# Cluster configuration
export CLUSTER_NAMESPACE="clusters"
export CLUSTER_NAME="${PREFIX}-hc"

# OIDC configuration
# Storage account names can only contain lowercase letters and numbers
export OIDC_ISSUER_NAME="${PREFIX//[^a-z0-9]/}wi"
export OIDC_ISSUER_URL="https://${OIDC_ISSUER_NAME}.blob.core.windows.net/${OIDC_ISSUER_NAME}"

# Service account token issuer keys (use default names that ccoctl generates)
export SA_TOKEN_ISSUER_PUBLIC_KEY_PATH="./serviceaccount-signer.public"
export SA_TOKEN_ISSUER_PRIVATE_KEY_PATH="./serviceaccount-signer.private"


# Workload identities
export AZURE_DISK_MI_NAME="workload-azure-disk-MI-${PREFIX}"
export AZURE_FILE_MI_NAME="workload-azure-file-MI-${PREFIX}"
export IMAGE_REGISTRY_MI_NAME="workload-image-registry-MI-${PREFIX}"
export INGRESS_MI_NAME="workload-ingress-MI-${PREFIX}"
export CLOUD_PROVIDER_MI_NAME="workload-cloud-provider-MI-${PREFIX}"
export NODE_POOL_MANAGEMENT_MI_NAME="workload-nodepool-mgmt-MI-${PREFIX}"
export NETWORK_MI_NAME="workload-network-MI-${PREFIX}"

# Binary paths
export HYPERSHIFT_BINARY_PATH="${HYPERSHIFT_BINARY_PATH:-./bin}"
export CCOCTL_BINARY_PATH="${CCOCTL_BINARY_PATH:-${HOME}/cloud-credential-operator}"


# File paths
export AZURE_CREDS="${AZURE_CREDS:-${HOME}/.azure/credentials}"
export PULL_SECRET="${PULL_SECRET:-${HOME}/all-the-pull-secrets.json}"
export WORKLOAD_IDENTITIES_FILE="${WORKLOAD_IDENTITIES_FILE:-./workload-identities.json}"