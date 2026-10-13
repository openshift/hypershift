#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/vars.sh"

echo "Installing HyperShift Operator for self-managed Azure..."

# Verify required files exist
if [[ ! -f "${EXTERNAL_DNS_SERVICE_PRINCIPAL_FILEPATH}" ]]; then
    echo "Error: External DNS service principal file not found at ${EXTERNAL_DNS_SERVICE_PRINCIPAL_FILEPATH}"
    echo "Please run ../managed-azure/setup_external_dns.sh first"
    exit 1
fi

if [[ ! -f "${PULL_SECRET}" ]]; then
    echo "Error: Pull secret file not found at ${PULL_SECRET}"
    exit 1
fi

# Install HyperShift Operator
echo "Installing HyperShift Operator with Azure External DNS..."
"${HYPERSHIFT_BINARY_PATH}/hypershift" install \
    --external-dns-provider=azure \
    --external-dns-credentials "${EXTERNAL_DNS_SERVICE_PRINCIPAL_FILEPATH}" \
    --pull-secret "${PULL_SECRET}" \
    --external-dns-domain-filter "${DNS_ZONE_NAME}" \
    --limit-crd-install Azure \

# Wait for HyperShift Operator to be ready
echo "Waiting for HyperShift Operator to be ready..."
kubectl wait --for=condition=available --timeout=300s deployment/operator -n hypershift

echo "HyperShift Operator installation completed successfully!"
echo "Operator is ready to create hosted clusters."