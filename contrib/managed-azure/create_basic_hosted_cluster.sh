#!/bin/bash
set -x

# This creates an Azure HostedCluster with the VNET in its own RG and the NSG in its own NSG.
# The MANAGED_RG_NAME contains all the cloud resources created by the HC creation.

# Prerequisites:
# 1. JSON file containing control plane service principals, see steps 5 & 6 in setup_dev_environment.md

# Constants
PREFIX="<your-prefix>"
RELEASE_IMAGE=quay.io/openshift-release-dev/ocp-release-nightly@sha256:96a2ae25ddc2f291de4f143ce2ba84b94c6c6fb48c34fdedc45f22a20838c752
TAG="<your-CPO-image-tag>"

LOCATION="eastus"
MANAGED_RG_NAME="${PREFIX}-managed-rg"
CUSTOMER_VNET_RG_NAME="${PREFIX}-customer-vnet-rg"
CUSTOMER_NSG_RG_NAME="${PREFIX}-customer-nsg-rg"
CUSTOMER_VNET_NAME="${PREFIX}-customer-vnet"
CUSTOMER_VNET_SUBNET1="${PREFIX}-customer-subnet-1"
CUSTOMER_NSG="${PREFIX}-customer-nsg"
MGMT_DNS_ZONE_NAME="blah-blah.com"
CLUSTER_NAME="${PREFIX}-hc"
AZURE_CREDS=/Users/your-username/.azure/credentials
AZURE_BASE_DOMAIN=blah-blah.openshift.com
PULL_SECRET=/Users/your-username/all-the-pull-secrets.json
HYPERSHIFT_BINARY_PATH="/Users/your-username/hypershift_ws/hypershift/bin"
MANAGED_IDENTITIES_FILE="/Users/your-username/azure/mi-file.json"
DNS_ZONE_RG_NAME="<NAME_OF_THE_RG_OF_THE_DNS_ZONE>"


# Delete any previous instances of the resource groups
az group delete -n "${MANAGED_RG_NAME}" --yes
az group delete -n "${CUSTOMER_VNET_RG_NAME}" --yes
az group delete -n "${CUSTOMER_NSG_RG_NAME}" --yes

# Create managed resource group
az group create --name "${MANAGED_RG_NAME}" --location ${LOCATION}

# Create customer resource group
az group create --name "${CUSTOMER_VNET_RG_NAME}" --location ${LOCATION}

# Create customer nsg resource group
az group create --name "${CUSTOMER_NSG_RG_NAME}" --location ${LOCATION}

# Create customer network security group
az network nsg create --resource-group "${CUSTOMER_NSG_RG_NAME}" --name "${CUSTOMER_NSG}"

# Get customer nsg ID
GetNsgID=$(az network nsg list --query "[?name=='${CUSTOMER_NSG}'].id" -o tsv)

# Create customer vnet in customer resource group
az network vnet create \
    --name "${CUSTOMER_VNET_NAME}" \
    --resource-group "${CUSTOMER_VNET_RG_NAME}" \
    --address-prefix 10.0.0.0/16 \
    --subnet-name "${CUSTOMER_VNET_SUBNET1}" \
    --subnet-prefixes 10.0.0.0/24 \
    --nsg "${GetNsgID}"

# Get customer vnet ID
GetVnetID=$(az network vnet list --query "[?name=='${CUSTOMER_VNET_NAME}'].id" -o tsv)

# Get customer subnet ID
GetSubnetID=$(az network vnet subnet show --vnet-name "${CUSTOMER_VNET_NAME}" --name "${CUSTOMER_VNET_SUBNET1}" --resource-group "${CUSTOMER_VNET_RG_NAME}" --query id --output tsv)

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
--control-plane-operator-image=<YOUR_REPO>:"${TAG}" \
--managed-identities-file ${MANAGED_IDENTITIES_FILE} \
--assign-service-principal-roles \
--dns-zone-rg-name ${DNS_ZONE_RG_NAME} \
--fips=true \
--marketplace-publisher azureopenshift \
--marketplace-offer aro4 \
--marketplace-sku aro_417 \
--marketplace-version 417.94.20240701

set +x