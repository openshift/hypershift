#!/bin/bash
set -o nounset
set -o pipefail

# This script creates the azure-creds.json file from an existing Azure Service Principal.
# It resets the SP credentials to generate a new client secret.
#
# Prerequisites:
#   - Azure CLI logged in (az login)
#   - AZURE_SP_NAME environment variable set to the display name of your SP
#
# Usage:
#   export AZURE_SP_NAME="my-prefix-sp"
#   ./setup_azure_creds.sh
#
# Output:
#   ./azure-creds.json

AZURE_CREDS_OUTPUT="${AZURE_CREDS_OUTPUT:-./azure-creds.json}"

# Check prerequisites
if ! command -v jq >/dev/null 2>&1; then
  echo "Error: jq is required but not found. Install it and re-run."
  exit 1
fi

# Check Azure login
if ! az account show >/dev/null 2>&1; then
  echo "Error: Not logged into Azure. Run 'az login' first."
  exit 1
fi

# Check AZURE_SP_NAME is set
if [[ -z "${AZURE_SP_NAME:-}" ]]; then
  echo "Error: AZURE_SP_NAME is not set."
  echo ""
  echo "Usage:"
  echo "  export AZURE_SP_NAME=\"your-sp-name\""
  echo "  ./setup_azure_creds.sh"
  echo ""
  echo "If you don't have a service principal yet, create one:"
  echo ""
  echo "  SUBSCRIPTION_ID=\$(az account show --query id -o tsv)"
  echo "  az ad sp create-for-rbac --name \"your-sp-name\" \\"
  echo "    --role Contributor --scopes \"/subscriptions/\$SUBSCRIPTION_ID\""
  echo ""
  echo "Then request these permissions from your admin (DPTP request for Red Hat):"
  echo "  1. Microsoft Graph 'Application.ReadWrite.OwnedBy' on the SP"
  echo "  2. 'User Access Administrator' role at the subscription level"
  echo ""
  echo "Once the SP is ready, re-run this script with AZURE_SP_NAME set."
  exit 1
fi

# Look up the SP — require an exact display name match and exactly one result.
# Split az and jq so we can check each exit code independently.
SP_LIST_STDERR=$(mktemp)
SP_LIST_JSON=$(az ad sp list --display-name "${AZURE_SP_NAME}" -o json 2>"$SP_LIST_STDERR")
if [[ $? -ne 0 ]]; then
  echo "Error: Failed to look up service principal '${AZURE_SP_NAME}'."
  cat "$SP_LIST_STDERR"
  rm -f "$SP_LIST_STDERR"
  exit 1
fi
rm -f "$SP_LIST_STDERR"

SP_MATCHES=$(echo "$SP_LIST_JSON" | jq -r --arg name "${AZURE_SP_NAME}" '.[] | select(.displayName == $name) | .appId')
if [[ $? -ne 0 ]]; then
  echo "Error: Failed to parse service principal lookup response."
  exit 1
fi

SP_COUNT=$(echo "$SP_MATCHES" | grep -c . 2>/dev/null || echo 0)

if [[ -z "$SP_MATCHES" ]]; then
  echo "Error: Service principal '${AZURE_SP_NAME}' not found."
  echo ""
  echo "To create it:"
  echo ""
  echo "  SUBSCRIPTION_ID=\$(az account show --query id -o tsv)"
  echo "  az ad sp create-for-rbac --name \"${AZURE_SP_NAME}\" \\"
  echo "    --role Contributor --scopes \"/subscriptions/\$SUBSCRIPTION_ID\""
  echo ""
  echo "Then request these permissions from your admin (DPTP request for Red Hat):"
  echo "  1. Microsoft Graph 'Application.ReadWrite.OwnedBy' on the SP"
  echo "  2. 'User Access Administrator' role at the subscription level"
  echo ""
  echo "Once the SP is ready, re-run this script."
  exit 1
fi

if [[ "$SP_COUNT" -gt 1 ]]; then
  echo "Error: Multiple service principals found matching '${AZURE_SP_NAME}':"
  echo "$SP_MATCHES"
  echo ""
  echo "Please ensure the display name is unique or delete duplicates."
  exit 1
fi

CLIENT_ID="$SP_MATCHES"

# Get subscription and tenant info
SUBSCRIPTION_ID=$(az account show --query id -o tsv)
TENANT_ID=$(az account show --query tenantId -o tsv)

echo "Found service principal '${AZURE_SP_NAME}' (appId: ${CLIENT_ID})"

# Preflight checks before confirmation — fail early before any destructive action.
CREDS_OUTPUT_DIR=$(dirname "${AZURE_CREDS_OUTPUT}")
if [[ ! -d "$CREDS_OUTPUT_DIR" ]]; then
  echo "Error: Output directory '${CREDS_OUTPUT_DIR}' does not exist."
  exit 1
fi

CREDS_TMPFILE=$(mktemp "${AZURE_CREDS_OUTPUT}.XXXXXX")
if [ $? -ne 0 ]; then
  echo "Error: Cannot create temporary file in '${CREDS_OUTPUT_DIR}'. Check permissions."
  exit 1
fi
chmod 0600 "$CREDS_TMPFILE"
trap 'rm -f "$CREDS_TMPFILE"' EXIT

echo ""
echo "WARNING: This will reset the SP credentials, invalidating any existing client secret."
read -p "Continue? (yes/no): " -r
if [[ ! $REPLY =~ ^[Yy][Ee][Ss]$ ]]; then
  echo "Aborted."
  exit 0
fi

echo "Resetting credentials to generate a new client secret..."

# Reset credentials — redirect stderr to a temp file so Azure CLI warnings
# (e.g. "The output includes credentials...") don't break JSON parsing,
# but we can still show the error if the command fails.
SP_STDERR=$(mktemp)
trap 'rm -f "$SP_STDERR" "$CREDS_TMPFILE"' EXIT
SP_DETAILS=$(az ad sp credential reset --id "$CLIENT_ID" -o json 2>"$SP_STDERR")
if [ $? -ne 0 ]; then
  echo "Error: Failed to reset credentials for SP '${AZURE_SP_NAME}'."
  cat "$SP_STDERR"
  echo ""
  echo "You may not have permission to reset this SP's credentials."
  echo "Check that you own this SP or ask your admin for help."
  exit 1
fi

CLIENT_SECRET=$(echo "$SP_DETAILS" | jq -r '.password')

if [[ -z "$CLIENT_SECRET" || "$CLIENT_SECRET" == "null" ]]; then
  echo "Error: Failed to extract client secret from credential reset response."
  exit 1
fi

# Write the credentials file through the pre-created temp file.
jq -n \
  --arg subscriptionId "${SUBSCRIPTION_ID}" \
  --arg tenantId "${TENANT_ID}" \
  --arg clientId "${CLIENT_ID}" \
  --arg clientSecret "${CLIENT_SECRET}" \
  '{subscriptionId: $subscriptionId, tenantId: $tenantId, clientId: $clientId, clientSecret: $clientSecret}' \
  >"$CREDS_TMPFILE"

if [ $? -ne 0 ]; then
  rm -f "$CREDS_TMPFILE"
  echo "Error: Failed to write credentials file."
  exit 1
fi

mv "$CREDS_TMPFILE" "${AZURE_CREDS_OUTPUT}"
if [ $? -ne 0 ]; then
  rm -f "$CREDS_TMPFILE"
  echo "Error: Failed to move credentials file to ${AZURE_CREDS_OUTPUT}."
  exit 1
fi

echo ""
echo "Azure credentials written to: ${AZURE_CREDS_OUTPUT}"
echo ""
echo "Set this in your user-vars.sh:"
echo "  export AZURE_CREDS=\"${AZURE_CREDS_OUTPUT}\""
