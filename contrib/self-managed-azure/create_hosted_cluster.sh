#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/vars.sh"

echo "Creating self-managed Azure hosted cluster..."

# Verify required files exist
if [[ ! -f "${AZURE_CREDS}" ]]; then
    echo "Error: Azure credentials file not found at ${AZURE_CREDS}"
    exit 1
fi

if [[ ! -f "${PULL_SECRET}" ]]; then
    echo "Error: Pull secret file not found at ${PULL_SECRET}"
    exit 1
fi

if [[ ! -f "${SA_TOKEN_ISSUER_PRIVATE_KEY_PATH}" ]]; then
    echo "Error: Service account token issuer private key not found at ${SA_TOKEN_ISSUER_PRIVATE_KEY_PATH}"
    exit 1
fi

if [[ ! -f "${WORKLOAD_IDENTITIES_FILE}" ]]; then
    echo "Error: Workload identities file not found at ${WORKLOAD_IDENTITIES_FILE}"
    exit 1
fi

# For self-managed Azure, no pre-existing customer networking is required

# Verify infrastructure exists
echo "Verifying Azure infrastructure exists..."
if ! az storage account show --name "${OIDC_ISSUER_NAME}" --resource-group "${PERSISTENT_RG_NAME}" >/dev/null 2>&1; then
    echo "Error: Azure infrastructure not found. Please run setup with --first-time flag"
    exit 1
fi

# Federated credentials and networking are automatically created by the hypershift create infra command

# Create the hosted cluster
echo "Creating hosted cluster..."
"${HYPERSHIFT_BINARY_PATH}/hypershift" create cluster azure \
    --name "${CLUSTER_NAME}" \
    --namespace "${CLUSTER_NAMESPACE}" \
    --azure-creds "${AZURE_CREDS}" \
    --location "${LOCATION}" \
    --node-pool-replicas 2 \
    --base-domain "${PARENT_DNS_ZONE}" \
    --pull-secret "${PULL_SECRET}" \
    --generate-ssh \
    --release-image "${RELEASE_IMAGE}" \
    --external-dns-domain "${DNS_ZONE_NAME}" \
    --resource-group-name "${MANAGED_RG_NAME}" \
    --sa-token-issuer-private-key-path "${SA_TOKEN_ISSUER_PRIVATE_KEY_PATH}" \
    --oidc-issuer-url "${OIDC_ISSUER_URL}" \
    --marketplace-publisher azureopenshift \
    --marketplace-offer aro4 \
    --marketplace-sku aro_419 \
    --marketplace-version 419.6.20250523 \
    --dns-zone-rg-name "${PERSISTENT_RG_NAME}" \
    --assign-service-principal-roles \
    --workload-identities-file "${WORKLOAD_IDENTITIES_FILE}" \
    --diagnostics-storage-account-type Managed \

echo "Hosted cluster creation initiated successfully!"
echo "Monitor the cluster status with: kubectl get hostedcluster ${CLUSTER_NAME} -n ${CLUSTER_NAMESPACE}"