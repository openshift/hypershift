#!/bin/bash
set -x

# This creates an Azure HostedCluster with the VNET in its own RG and the NSG in its own NSG.
# The MANAGED_RG_NAME contains all the cloud resources created by the HC creation.

# Constants
PREFIX="your-prefix"
RELEASE_IMAGE=quay.io/openshift-release-dev/ocp-release-nightly@sha256:cd69dadc9b89036f24394191969219886db5f3ae5d40ca831130a9ad7d92a2c7
CUSTOM_IMAGE="quay.io/user/hypershift:mytag1"

LOCATION="eastus"
MANAGED_RG_NAME="${PREFIX}-managed-aks-rg"
CUSTOMER_RG_NAME="${PREFIX}-customer-aks-rg"
CUSTOMER_NSG_RG_NAME="${PREFIX}-customer-nsg-rg"
CUSTOMER_VNET_NAME="${PREFIX}-customer-vnet"
CUSTOMER_VNET_SUBNET1="${PREFIX}-customer-subnet-1"
CUSTOMER_NSG="${PREFIX}-customer-nsg"
MGMT_DNS_ZONE_NAME="azure.blah.com"
CLUSTER_NAME="${PREFIX}-hc"
AZURE_CREDS=</path/to/credentials>
AZURE_BASE_DOMAIN=<your-domin>
PULL_SECRET=<your/path/to/pullsecrets>
HYPERSHIFT_BINARY_PATH="<your/path/>"
# As of 2024 July 10th, there is no 4.17 version of these images
OCP_VERSION=v4.16
CERTIFIED_OPERATOR_INDEX_REPO="registry.redhat.io/redhat/certified-operator-index"
COMMUNITY_OPERATOR_INDEX_REPO="registry.redhat.io/redhat/community-operator-index"
REDHAT_MARKETPLACE_INDEX_REPO="registry.redhat.io/redhat/redhat-marketplace-index"
REDHAT_OPERATOR_INDEX_REPO="registry.redhat.io/redhat/redhat-operator-index"

# Delete any previous instances of the resource groups
az group delete -n "${MANAGED_RG_NAME}" --yes
az group delete -n "${CUSTOMER_RG_NAME}" --yes
az group delete -n "${CUSTOMER_NSG_RG_NAME}" --yes

# Create managed resource group
az group create --name "${MANAGED_RG_NAME}" --location ${LOCATION}

# Create customer resource group
az group create --name "${CUSTOMER_RG_NAME}" --location ${LOCATION}

# Create customer nsg resource group
az group create --name "${CUSTOMER_NSG_RG_NAME}" --location ${LOCATION}

# Create customer network security group
az network nsg create --resource-group "${CUSTOMER_NSG_RG_NAME}" --name "${CUSTOMER_NSG}"

# Get customer nsg ID
GetNsgID=$(az network nsg list --query "[?name=='${CUSTOMER_NSG}'].id" -o tsv)

# Create customer vnet in customer resource group
az network vnet create \
    --name "${CUSTOMER_VNET_NAME}" \
    --resource-group "${CUSTOMER_RG_NAME}" \
    --address-prefix 10.0.0.0/16 \
    --subnet-name "${CUSTOMER_VNET_SUBNET1}" \
    --subnet-prefixes 10.0.0.0/24 \
    --nsg "${GetNsgID}"

# Get customer vnet ID
GetVnetID=$(az network vnet list --query "[?name=='${CUSTOMER_VNET_NAME}'].id" -o tsv)

# Get customer subnet ID
GetSubnetID=$(az network vnet subnet show --vnet-name "${CUSTOMER_VNET_NAME}" --name "${CUSTOMER_VNET_SUBNET1}" --resource-group "${CUSTOMER_RG_NAME}" --query id --output tsv)

# Retrieve hash for the multi-arch index for the Red Hat Catalog images
CERTIFIED_OPERATOR_INDEX_HASH="$(oc image info "$CERTIFIED_OPERATOR_INDEX_REPO:${OCP_VERSION}" -a "${PULL_SECRET}" --filter-by-os linux/amd64 -o json | jq -r .listDigest)"
COMMUNITY_OPERATOR_INDEX_HASH="$(oc image info "$COMMUNITY_OPERATOR_INDEX_REPO:${OCP_VERSION}" -a "${PULL_SECRET}" --filter-by-os linux/amd64 -o json | jq -r .listDigest)"
REDHAT_MARKETPLACE_INDEX_HASH="$(oc image info "$REDHAT_MARKETPLACE_INDEX_REPO:${OCP_VERSION}" -a "${PULL_SECRET}" --filter-by-os linux/amd64 -o json | jq -r .listDigest)"
REDHAT_OPERATOR_INDEX_HASH="$(oc image info "$REDHAT_OPERATOR_INDEX_REPO:${OCP_VERSION}" -a "${PULL_SECRET}" --filter-by-os linux/amd64 -o json | jq -r .listDigest)"

# Create the Hosted Cluster
# If you want to run a custom CPO image, add the following flag to your command below
# --control-plane-operator-image=${CUSTOM_IMAGE}" \
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
--annotations hypershift.openshift.io/certified-operators-catalog-image=registry.redhat.io/redhat/certified-operator-index@${CERTIFIED_OPERATOR_INDEX_HASH} \
--annotations hypershift.openshift.io/community-operators-catalog-image=registry.redhat.io/redhat/community-operator-index@${COMMUNITY_OPERATOR_INDEX_HASH} \
--annotations hypershift.openshift.io/redhat-marketplace-catalog-image=registry.redhat.io/redhat/redhat-marketplace-index@${REDHAT_MARKETPLACE_INDEX_HASH} \
--annotations hypershift.openshift.io/redhat-operators-catalog-image=registry.redhat.io/redhat/redhat-operator-index@${REDHAT_OPERATOR_INDEX_HASH} \
--fips=true

set +x