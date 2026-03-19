#!/bin/bash
# OADP Integration Test Runner
#
# Usage:
#   ./test/integration/oadp/run.sh cli    - Run OADP CLI integration tests
#   ./test/integration/oadp/run.sh upload - Run etcd-upload integration tests (S3 + Azure)
#
# Prerequisites:
#   - Build the CPO binary: make control-plane-operator
#   - For upload tests: copy upload/.env.example to upload/.env and fill in values
#   - For upload tests: aws cli and az cli must be installed and authenticated

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
UPLOAD_DIR="${SCRIPT_DIR}/upload"
ENV_FILE="${UPLOAD_DIR}/.env"

cd "${REPO_ROOT}"

# ── Helpers ──────────────────────────────────────────────────────────────────

function log()  { echo "==> $*"; }
function err()  { echo "ERROR: $*" >&2; exit 1; }

function require_var() {
    local var_name="$1"
    if [[ -z "${!var_name:-}" ]]; then
        err "${var_name} is not set. Check your ${ENV_FILE} file."
    fi
}

function require_command() {
    if ! command -v "$1" &>/dev/null; then
        err "$1 is required but not found in PATH"
    fi
}

# ── CLI Tests ────────────────────────────────────────────────────────────────

function run_cli() {
    log "Running OADP CLI integration tests..."
    go test -tags integration -v ./test/integration/oadp/cli/...
}

# ── Upload Tests: Setup/Teardown ─────────────────────────────────────────────

CREATED_KMS_KEY_ID=""
CREATED_AZURE_SP_ID=""
CREATED_AZURE_CREDS_FILE=""
CREATED_AZURE_ENCRYPTION_SCOPE=""

function load_env() {
    if [[ ! -f "${ENV_FILE}" ]]; then
        err "Environment file not found: ${ENV_FILE}\n  Copy ${UPLOAD_DIR}/.env.example to ${ENV_FILE} and fill in the values."
    fi
    # shellcheck source=/dev/null
    source "${ENV_FILE}"
}

function setup_aws() {
    require_var ETCD_UPLOAD_TEST_AWS_BUCKET
    require_var ETCD_UPLOAD_TEST_AWS_REGION
    require_var ETCD_UPLOAD_TEST_AWS_CREDENTIALS_FILE
    require_command aws

    log "Creating temporary KMS key for S3 encryption test..."
    local key_json
    key_json=$(aws kms create-key \
        --description "etcd-upload-integration-test-temp" \
        --region "${ETCD_UPLOAD_TEST_AWS_REGION}" \
        --output json 2>&1)
    CREATED_KMS_KEY_ID=$(echo "${key_json}" | jq -r '.KeyMetadata.KeyId')
    local key_arn
    key_arn=$(echo "${key_json}" | jq -r '.KeyMetadata.Arn')

    if [[ -z "${CREATED_KMS_KEY_ID}" || "${CREATED_KMS_KEY_ID}" == "null" ]]; then
        err "Failed to create KMS key: ${key_json}"
    fi

    export ETCD_UPLOAD_TEST_AWS_KMS_KEY_ARN="${key_arn}"
    log "KMS key created: ${CREATED_KMS_KEY_ID}"
}

function teardown_aws() {
    if [[ -n "${CREATED_KMS_KEY_ID}" ]]; then
        log "Scheduling KMS key ${CREATED_KMS_KEY_ID} for deletion (7 days)..."
        aws kms schedule-key-deletion \
            --key-id "${CREATED_KMS_KEY_ID}" \
            --pending-window-in-days 7 \
            --region "${ETCD_UPLOAD_TEST_AWS_REGION}" \
            --output json >/dev/null 2>&1 || true
    fi

    log "Cleaning up S3 test objects..."
    aws s3 rm "s3://${ETCD_UPLOAD_TEST_AWS_BUCKET}/integration-test/" \
        --recursive \
        --region "${ETCD_UPLOAD_TEST_AWS_REGION}" 2>/dev/null || true
}

function setup_azure() {
    require_var ETCD_UPLOAD_TEST_AZURE_STORAGE_ACCOUNT
    require_var ETCD_UPLOAD_TEST_AZURE_CONTAINER
    require_var ETCD_UPLOAD_TEST_AZURE_RESOURCE_GROUP
    require_var ETCD_UPLOAD_TEST_AZURE_SUBSCRIPTION_ID
    require_command az
    require_command jq

    local storage_account="${ETCD_UPLOAD_TEST_AZURE_STORAGE_ACCOUNT}"
    local resource_group="${ETCD_UPLOAD_TEST_AZURE_RESOURCE_GROUP}"
    local subscription_id="${ETCD_UPLOAD_TEST_AZURE_SUBSCRIPTION_ID}"
    local scope="/subscriptions/${subscription_id}/resourceGroups/${resource_group}/providers/Microsoft.Storage/storageAccounts/${storage_account}"

    log "Creating temporary Azure service principal..."
    local sp_json
    sp_json=$(az ad sp create-for-rbac \
        --name "etcd-upload-integration-test-temp" \
        --role "Storage Blob Data Contributor" \
        --scopes "${scope}" \
        -o json 2>&1)

    local client_id tenant_id client_secret
    client_id=$(echo "${sp_json}" | jq -r '.appId')
    tenant_id=$(echo "${sp_json}" | jq -r '.tenant')
    client_secret=$(echo "${sp_json}" | jq -r '.password')

    if [[ -z "${client_id}" || "${client_id}" == "null" ]]; then
        err "Failed to create service principal: ${sp_json}"
    fi
    CREATED_AZURE_SP_ID="${client_id}"

    CREATED_AZURE_CREDS_FILE=$(mktemp /tmp/etcd-upload-azure-creds.XXXXXX.json)
    cat > "${CREATED_AZURE_CREDS_FILE}" <<CREDS_EOF
{
  "subscriptionId": "${subscription_id}",
  "tenantId": "${tenant_id}",
  "clientId": "${client_id}",
  "clientSecret": "${client_secret}"
}
CREDS_EOF
    export ETCD_UPLOAD_TEST_AZURE_CREDENTIALS_FILE="${CREATED_AZURE_CREDS_FILE}"
    log "Service principal created: ${CREATED_AZURE_SP_ID}"

    log "Creating temporary encryption scope..."
    CREATED_AZURE_ENCRYPTION_SCOPE="etcd-upload-test-scope"
    az storage account encryption-scope create \
        --account-name "${storage_account}" \
        --resource-group "${resource_group}" \
        --name "${CREATED_AZURE_ENCRYPTION_SCOPE}" \
        --key-source Microsoft.Storage \
        -o none 2>&1
    export ETCD_UPLOAD_TEST_AZURE_ENCRYPTION_SCOPE="${CREATED_AZURE_ENCRYPTION_SCOPE}"
    log "Encryption scope created: ${CREATED_AZURE_ENCRYPTION_SCOPE}"

    # Give Azure a moment to propagate the role assignment
    log "Waiting 10s for Azure role assignment propagation..."
    sleep 10
}

function teardown_azure() {
    local storage_account="${ETCD_UPLOAD_TEST_AZURE_STORAGE_ACCOUNT:-}"
    local resource_group="${ETCD_UPLOAD_TEST_AZURE_RESOURCE_GROUP:-}"

    if [[ -n "${CREATED_AZURE_ENCRYPTION_SCOPE}" && -n "${storage_account}" ]]; then
        log "Disabling encryption scope ${CREATED_AZURE_ENCRYPTION_SCOPE}..."
        az storage account encryption-scope update \
            --account-name "${storage_account}" \
            --resource-group "${resource_group}" \
            --name "${CREATED_AZURE_ENCRYPTION_SCOPE}" \
            --state Disabled \
            -o none 2>/dev/null || true
    fi

    if [[ -n "${CREATED_AZURE_SP_ID}" ]]; then
        log "Deleting service principal ${CREATED_AZURE_SP_ID}..."
        az ad sp delete --id "${CREATED_AZURE_SP_ID}" 2>/dev/null || true
    fi

    if [[ -n "${CREATED_AZURE_CREDS_FILE}" && -f "${CREATED_AZURE_CREDS_FILE}" ]]; then
        rm -f "${CREATED_AZURE_CREDS_FILE}"
    fi

    if [[ -n "${storage_account}" ]]; then
        log "Cleaning up Azure test blobs..."
        az storage blob delete-batch \
            --source "${ETCD_UPLOAD_TEST_AZURE_CONTAINER:-}" \
            --account-name "${storage_account}" \
            --pattern "integration-test/*" \
            --auth-mode key 2>/dev/null || true
    fi
}

# ── Upload Tests: Run ────────────────────────────────────────────────────────

function run_upload() {
    load_env

    local aws_ok=true azure_ok=true
    local test_result=0

    # Check which providers are configured
    if [[ -z "${ETCD_UPLOAD_TEST_AWS_BUCKET:-}" ]]; then
        log "AWS not configured, skipping S3 tests"
        aws_ok=false
    fi
    if [[ -z "${ETCD_UPLOAD_TEST_AZURE_STORAGE_ACCOUNT:-}" ]]; then
        log "Azure not configured, skipping Azure Blob tests"
        azure_ok=false
    fi

    if [[ "${aws_ok}" == "false" && "${azure_ok}" == "false" ]]; then
        err "No cloud provider configured. Fill in at least one provider in ${ENV_FILE}"
    fi

    # Setup
    if [[ "${aws_ok}" == "true" ]]; then
        setup_aws
    fi
    if [[ "${azure_ok}" == "true" ]]; then
        setup_azure
    fi

    # Ensure cleanup runs on exit
    trap 'teardown' EXIT

    # Run tests
    log "Running etcd-upload integration tests..."
    go test -tags integration -v ./test/integration/oadp/upload/... || test_result=$?

    return ${test_result}
}

function teardown() {
    log "Cleaning up test resources..."
    teardown_aws 2>/dev/null || true
    teardown_azure 2>/dev/null || true
    log "Cleanup complete."
}

# ── Main ─────────────────────────────────────────────────────────────────────

if [[ $# -eq 0 ]]; then
    echo "Usage: $0 {cli|upload}"
    exit 1
fi

for arg in "$@"; do
    case "${arg}" in
        "cli")    run_cli ;;
        "upload") run_upload ;;
        *)
            echo "Unknown command: ${arg}"
            echo "Usage: $0 {cli|upload}"
            exit 1
            ;;
    esac
done
