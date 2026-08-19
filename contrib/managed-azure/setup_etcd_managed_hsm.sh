#!/bin/bash
set +x

# Prerequisites: define these constants
LOCATION="eastus"
HSM_RG_NAME="example-kms"
HSM_NAME="example-managed-hsm"
KEY_NAME="example-key"
SUBSCRIPTION_ID="<your-subscription-id-here>"

# This is the object ID of your signed-in user or service principal that will administer the HSM.
# Used to assign the initial HSM administrator role and create keys.
# Find it with: az ad signed-in-user show --query id -o tsv
USER_OBJECT_ID="<fill-me-out>"

# This is the object ID of the KMS Managed Identity. This object ID can be found under the enterprise application for
# your KMS Managed Identity.
OBJECT_ID="<fill-me-out>"

# Number of security domain key shares and required quorum for HSM activation.
# At least 3 shares with a quorum of 2 is recommended.
SD_SHARES=3
SD_QUORUM=2

# Create a resource group to hold the Managed HSM
az group create --name "$HSM_RG_NAME" --location "$LOCATION"

# Create the Managed HSM
# Note: provisioning takes approximately 20+ minutes.
az keyvault create \
    --hsm-name "$HSM_NAME" \
    --resource-group "$HSM_RG_NAME" \
    --location "$LOCATION" \
    --administrators "$USER_OBJECT_ID"

echo "Waiting for Managed HSM provisioning to complete..."
az keyvault wait --hsm-name "$HSM_NAME" --resource-group "$HSM_RG_NAME" --created

# Activate the Managed HSM security domain.
# Generate RSA key pairs that will protect the security domain download.
SD_DIR=$(mktemp -d)
for i in $(seq 0 $((SD_SHARES - 1))); do
    openssl req -newkey rsa:2048 -nodes \
        -keyout "${SD_DIR}/sd_key_${i}.pem" \
        -x509 -days 365 \
        -out "${SD_DIR}/sd_cert_${i}.pem" \
        -subj "/CN=SecurityDomain${i}" 2>/dev/null
done

SD_CERT_ARGS=""
for i in $(seq 0 $((SD_SHARES - 1))); do
    SD_CERT_ARGS="${SD_CERT_ARGS} --sd-wrapping-keys ${SD_DIR}/sd_cert_${i}.pem"
done

# Download and activate the security domain
# shellcheck disable=SC2086
az keyvault security-domain download \
    --hsm-name "$HSM_NAME" \
    --security-domain-file "${SD_DIR}/security_domain.json" \
    ${SD_CERT_ARGS} \
    --sd-quorum "$SD_QUORUM"

echo "Security domain downloaded to ${SD_DIR}/security_domain.json"
echo "IMPORTANT: Back up the security domain file and key pairs — they are required to restore the HSM."

# Assign the Managed HSM Crypto Officer role (key management) and
# Crypto User role at /keys (data-plane key operations). Both are
# needed to create keys — Administrator alone is not sufficient.
az keyvault role assignment create \
    --hsm-name "$HSM_NAME" \
    --assignee "$USER_OBJECT_ID" \
    --role "Managed HSM Crypto Officer" \
    --scope "/"

az keyvault role assignment create \
    --hsm-name "$HSM_NAME" \
    --assignee "$USER_OBJECT_ID" \
    --role "Managed HSM Crypto User" \
    --scope "/keys"

# Wait for RBAC to propagate (~2-5 minutes for Managed HSM data-plane)
echo "Waiting for RBAC propagation..."
sleep 300

# Create a key in the Managed HSM
# Note: --protection is not used; all Managed HSM keys are HSM-backed.
export KEY_ID
KEY_ID=$(az keyvault key create \
    --hsm-name "$HSM_NAME" \
    --name "$KEY_NAME" \
    --kty RSA-HSM \
    --query key.kid \
    -o tsv)

echo ""
echo "Encryption key created: $KEY_ID"
echo ""

# Assign the Managed HSM Crypto User role to the KMS Managed Identity
az keyvault role assignment create \
    --hsm-name "$HSM_NAME" \
    --assignee "$OBJECT_ID" \
    --role "Managed HSM Crypto User" \
    --scope "/keys/${KEY_NAME}"

echo ""
echo "Setup complete. Pass the following flag when creating your Azure HostedCluster:"
echo "  --encryption-key-id $KEY_ID"
echo ""
echo "Managed HSM requires OpenShift 4.22 or later."
