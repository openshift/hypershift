#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/vars.sh"

echo "Creating Azure infrastructure and workload identities..."

# Verify required files exist
if [[ ! -f "${AZURE_CREDS}" ]]; then
    echo "Error: Azure credentials file not found at ${AZURE_CREDS}"
    exit 1
fi

if [[ ! -f "${HYPERSHIFT_BINARY_PATH}/hypershift" ]]; then
    echo "Error: HyperShift binary not found at ${HYPERSHIFT_BINARY_PATH}/hypershift"
    exit 1
fi

# Verify OIDC issuer exists (should be created by ../managed-azure/setup_oidc_provider.sh)
if ! az storage account show --name "${OIDC_ISSUER_NAME}" --resource-group "${PERSISTENT_RG_NAME}" >/dev/null 2>&1; then
    echo "Error: OIDC issuer not found. Please run ../managed-azure/setup_oidc_provider.sh first"
    exit 1
fi

# Create resource group for managed identities
echo "Creating resource group: ${MANAGED_RG_NAME}"
if ! az group show --name "${MANAGED_RG_NAME}" >/dev/null 2>&1; then
    az group create --name "${MANAGED_RG_NAME}" --location "${LOCATION}"
    echo "Successfully created resource group: ${MANAGED_RG_NAME}"
else
    echo "Resource group ${MANAGED_RG_NAME} already exists"
fi

# Create Azure infrastructure using HyperShift
echo "Creating Azure infrastructure and workload identities..."
"${HYPERSHIFT_BINARY_PATH}/hypershift" create infra azure \
    --name "${CLUSTER_NAME}" \
    --infra-id "${CLUSTER_NAME}-infra" \
    --azure-creds "${AZURE_CREDS}" \
    --location "${LOCATION}" \
    --resource-group-name "${MANAGED_RG_NAME}" \
    --oidc-issuer-url "${OIDC_ISSUER_URL}" \
    --generate-managed-identities \
    --workload-identities-output-file "${WORKLOAD_IDENTITIES_FILE}"

echo "Azure infrastructure setup completed!"
echo "Workload identities configuration written to: ${WORKLOAD_IDENTITIES_FILE}"