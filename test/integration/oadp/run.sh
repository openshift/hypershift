#!/bin/bash
# OADP Integration Test Runner
#
# Usage:
#   ./test/integration/oadp/run.sh cli          - Run OADP CLI integration tests
#   ./test/integration/oadp/run.sh upload        - Run etcd-upload integration tests (S3 + Azure)
#   ./test/integration/oadp/run.sh upload aws    - Run only S3 upload tests
#   ./test/integration/oadp/run.sh upload azure  - Run only Azure Blob upload tests
#
# Prerequisites:
#   - Build the CPO binary: make control-plane-operator
#   - For AWS tests: aws cli authenticated (aws sts get-caller-identity must succeed)
#   - For Azure tests: az cli authenticated (az account show must succeed)

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
SUFFIX="$(date +%s | tail -c 7)"

cd "${REPO_ROOT}"

# ── Helpers ──────────────────────────────────────────────────────────────────

function log()  { echo "==> $*"; }
function err()  { echo "ERROR: $*" >&2; exit 1; }

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

# ── Upload Tests: AWS Setup/Teardown ─────────────────────────────────────────

CREATED_AWS_BUCKET=""
CREATED_AWS_REGION=""
CREATED_AWS_KMS_KEY_ID=""
CREATED_AWS_CREDS_FILE=""

function setup_aws() {
    require_command aws
    require_command jq

    # Verify authentication
    if ! aws sts get-caller-identity &>/dev/null; then
        err "AWS CLI is not authenticated. Run 'aws configure' or set AWS credentials."
    fi

    # Determine region
    CREATED_AWS_REGION=$(aws configure get region 2>/dev/null || echo "us-east-1")
    log "Using AWS region: ${CREATED_AWS_REGION}"

    # Create temporary bucket
    CREATED_AWS_BUCKET="etcd-upload-test-${SUFFIX}"
    log "Creating temporary S3 bucket: ${CREATED_AWS_BUCKET}..."
    if [[ "${CREATED_AWS_REGION}" == "us-east-1" ]]; then
        aws s3api create-bucket \
            --bucket "${CREATED_AWS_BUCKET}" \
            --region "${CREATED_AWS_REGION}" \
            --output json >/dev/null 2>&1
    else
        aws s3api create-bucket \
            --bucket "${CREATED_AWS_BUCKET}" \
            --region "${CREATED_AWS_REGION}" \
            --create-bucket-configuration LocationConstraint="${CREATED_AWS_REGION}" \
            --output json >/dev/null 2>&1
    fi

    # Create temporary credentials file from current session
    CREATED_AWS_CREDS_FILE=$(mktemp /tmp/etcd-upload-aws-creds.XXXXXX)
    local access_key secret_key session_token
    access_key=$(aws configure get aws_access_key_id 2>/dev/null || echo "")
    secret_key=$(aws configure get aws_secret_access_key 2>/dev/null || echo "")
    session_token=$(aws configure get aws_session_token 2>/dev/null || echo "")

    # If not in config, try environment variables
    access_key="${access_key:-${AWS_ACCESS_KEY_ID:-}}"
    secret_key="${secret_key:-${AWS_SECRET_ACCESS_KEY:-}}"
    session_token="${session_token:-${AWS_SESSION_TOKEN:-}}"

    if [[ -z "${access_key}" || -z "${secret_key}" ]]; then
        # Fall back to default credentials file if it exists
        if [[ -f "${HOME}/.aws/credentials" ]]; then
            CREATED_AWS_CREDS_FILE="${HOME}/.aws/credentials"
            log "Using existing credentials file: ${CREATED_AWS_CREDS_FILE}"
        else
            err "Cannot determine AWS credentials. Ensure aws cli is configured."
        fi
    else
        cat > "${CREATED_AWS_CREDS_FILE}" <<CREDS_EOF
[default]
aws_access_key_id = ${access_key}
aws_secret_access_key = ${secret_key}
CREDS_EOF
        if [[ -n "${session_token}" ]]; then
            echo "aws_session_token = ${session_token}" >> "${CREATED_AWS_CREDS_FILE}"
        fi
        log "Created temporary credentials file: ${CREATED_AWS_CREDS_FILE}"
    fi

    # Create temporary KMS key
    log "Creating temporary KMS key..."
    local key_json
    key_json=$(aws kms create-key \
        --description "etcd-upload-integration-test-temp-${SUFFIX}" \
        --region "${CREATED_AWS_REGION}" \
        --output json 2>&1)
    CREATED_AWS_KMS_KEY_ID=$(echo "${key_json}" | jq -r '.KeyMetadata.KeyId')
    local key_arn
    key_arn=$(echo "${key_json}" | jq -r '.KeyMetadata.Arn')

    if [[ -z "${CREATED_AWS_KMS_KEY_ID}" || "${CREATED_AWS_KMS_KEY_ID}" == "null" ]]; then
        err "Failed to create KMS key: ${key_json}"
    fi
    log "KMS key created: ${CREATED_AWS_KMS_KEY_ID}"

    # Export env vars for tests
    export ETCD_UPLOAD_TEST_AWS_BUCKET="${CREATED_AWS_BUCKET}"
    export ETCD_UPLOAD_TEST_AWS_REGION="${CREATED_AWS_REGION}"
    export ETCD_UPLOAD_TEST_AWS_CREDENTIALS_FILE="${CREATED_AWS_CREDS_FILE}"
    export ETCD_UPLOAD_TEST_AWS_KMS_KEY_ARN="${key_arn}"
}

function teardown_aws() {
    if [[ -n "${CREATED_AWS_KMS_KEY_ID}" ]]; then
        log "Scheduling KMS key ${CREATED_AWS_KMS_KEY_ID} for deletion (7 days)..."
        aws kms schedule-key-deletion \
            --key-id "${CREATED_AWS_KMS_KEY_ID}" \
            --pending-window-in-days 7 \
            --region "${CREATED_AWS_REGION}" \
            --output json >/dev/null 2>&1 || true
    fi

    if [[ -n "${CREATED_AWS_BUCKET}" ]]; then
        log "Deleting S3 bucket ${CREATED_AWS_BUCKET} and its contents..."
        aws s3 rb "s3://${CREATED_AWS_BUCKET}" \
            --force \
            --region "${CREATED_AWS_REGION}" 2>/dev/null || true
    fi

    if [[ -n "${CREATED_AWS_CREDS_FILE}" && "${CREATED_AWS_CREDS_FILE}" == /tmp/* && -f "${CREATED_AWS_CREDS_FILE}" ]]; then
        rm -f "${CREATED_AWS_CREDS_FILE}"
    fi
}

# ── Upload Tests: Azure Setup/Teardown ───────────────────────────────────────

CREATED_AZURE_RG=""
CREATED_AZURE_STORAGE_ACCOUNT=""
CREATED_AZURE_CONTAINER=""
CREATED_AZURE_SP_ID=""
CREATED_AZURE_CREDS_FILE=""
CREATED_AZURE_ENCRYPTION_SCOPE=""
AZURE_SUBSCRIPTION_ID=""
AZURE_LOCATION="eastus"

function setup_azure() {
    require_command az
    require_command jq

    # Verify authentication
    if ! az account show &>/dev/null; then
        err "Azure CLI is not authenticated. Run 'az login'."
    fi

    AZURE_SUBSCRIPTION_ID=$(az account show --query id -o tsv)
    log "Using Azure subscription: ${AZURE_SUBSCRIPTION_ID}"

    # Create temporary resource group
    CREATED_AZURE_RG="etcd-upload-test-${SUFFIX}"
    log "Creating temporary resource group: ${CREATED_AZURE_RG}..."
    az group create \
        --name "${CREATED_AZURE_RG}" \
        --location "${AZURE_LOCATION}" \
        -o none

    # Create temporary storage account (no hyphens, max 24 chars)
    CREATED_AZURE_STORAGE_ACCOUNT="etcdtest${SUFFIX}"
    log "Creating temporary storage account: ${CREATED_AZURE_STORAGE_ACCOUNT}..."
    az storage account create \
        --name "${CREATED_AZURE_STORAGE_ACCOUNT}" \
        --resource-group "${CREATED_AZURE_RG}" \
        --location "${AZURE_LOCATION}" \
        --sku Standard_LRS \
        --kind StorageV2 \
        -o none

    # Create temporary container
    CREATED_AZURE_CONTAINER="etcd-upload-test"
    log "Creating temporary container: ${CREATED_AZURE_CONTAINER}..."
    az storage container create \
        --name "${CREATED_AZURE_CONTAINER}" \
        --account-name "${CREATED_AZURE_STORAGE_ACCOUNT}" \
        --auth-mode login \
        -o none

    # Create temporary service principal with Storage Blob Data Contributor role
    local scope="/subscriptions/${AZURE_SUBSCRIPTION_ID}/resourceGroups/${CREATED_AZURE_RG}/providers/Microsoft.Storage/storageAccounts/${CREATED_AZURE_STORAGE_ACCOUNT}"
    log "Creating temporary service principal..."
    local sp_json
    sp_json=$(az ad sp create-for-rbac \
        --name "etcd-upload-test-${SUFFIX}" \
        --role "Storage Blob Data Contributor" \
        --scopes "${scope}" \
        -o json 2>/dev/null)

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
  "subscriptionId": "${AZURE_SUBSCRIPTION_ID}",
  "tenantId": "${tenant_id}",
  "clientId": "${client_id}",
  "clientSecret": "${client_secret}"
}
CREDS_EOF
    log "Service principal created: ${CREATED_AZURE_SP_ID}"

    # Create temporary encryption scope
    CREATED_AZURE_ENCRYPTION_SCOPE="etcd-upload-test-scope"
    log "Creating temporary encryption scope: ${CREATED_AZURE_ENCRYPTION_SCOPE}..."
    az storage account encryption-scope create \
        --account-name "${CREATED_AZURE_STORAGE_ACCOUNT}" \
        --resource-group "${CREATED_AZURE_RG}" \
        --name "${CREATED_AZURE_ENCRYPTION_SCOPE}" \
        --key-source Microsoft.Storage \
        -o none 2>/dev/null

    # Wait for Azure role assignment propagation with retry loop
    # Use the CPO binary to probe with the SP credentials
    log "Waiting for Azure RBAC propagation (up to 200s)..."
    local max_retries=10
    local retry_interval=20
    local i=0
    local probe_file
    probe_file=$(mktemp /tmp/etcd-upload-probe.XXXXXX)
    echo "probe" > "${probe_file}"
    while [[ ${i} -lt ${max_retries} ]]; do
        i=$((i + 1))
        if bin/control-plane-operator etcd-upload \
            --snapshot-path "${probe_file}" \
            --storage-type AzureBlob \
            --azure-container "${CREATED_AZURE_CONTAINER}" \
            --azure-storage-account "${CREATED_AZURE_STORAGE_ACCOUNT}" \
            --key-prefix ".rbac-probe" \
            --credentials-file "${CREATED_AZURE_CREDS_FILE}" \
            2>/dev/null; then
            log "RBAC propagation confirmed (attempt ${i}/${max_retries})"
            break
        fi
        if [[ ${i} -eq ${max_retries} ]]; then
            rm -f "${probe_file}"
            err "Azure RBAC propagation timed out after $((max_retries * retry_interval))s"
        fi
        log "RBAC not ready yet, retrying in ${retry_interval}s (attempt ${i}/${max_retries})..."
        sleep ${retry_interval}
    done
    rm -f "${probe_file}"

    # Export env vars for tests
    export ETCD_UPLOAD_TEST_AZURE_CONTAINER="${CREATED_AZURE_CONTAINER}"
    export ETCD_UPLOAD_TEST_AZURE_STORAGE_ACCOUNT="${CREATED_AZURE_STORAGE_ACCOUNT}"
    export ETCD_UPLOAD_TEST_AZURE_CREDENTIALS_FILE="${CREATED_AZURE_CREDS_FILE}"
    export ETCD_UPLOAD_TEST_AZURE_ENCRYPTION_SCOPE="${CREATED_AZURE_ENCRYPTION_SCOPE}"
}

function teardown_azure() {
    if [[ -n "${CREATED_AZURE_SP_ID}" ]]; then
        log "Deleting service principal ${CREATED_AZURE_SP_ID}..."
        az ad sp delete --id "${CREATED_AZURE_SP_ID}" 2>/dev/null || true
        log "Deleting application registration ${CREATED_AZURE_SP_ID}..."
        az ad app delete --id "${CREATED_AZURE_SP_ID}" 2>/dev/null || true
    fi

    # Safety net: clean up any leftover SPs matching our naming pattern
    local leftover_sps
    leftover_sps=$(az ad sp list --display-name "etcd-upload-test-${SUFFIX}" --query "[].appId" -o tsv 2>/dev/null || echo "")
    for sp_id in ${leftover_sps}; do
        log "Cleaning up leftover service principal ${sp_id}..."
        az ad sp delete --id "${sp_id}" 2>/dev/null || true
        log "Cleaning up leftover application registration ${sp_id}..."
        az ad app delete --id "${sp_id}" 2>/dev/null || true
    done

    if [[ -n "${CREATED_AZURE_RG}" ]]; then
        log "Deleting resource group ${CREATED_AZURE_RG} (cascades to all resources)..."
        az group delete \
            --name "${CREATED_AZURE_RG}" \
            --yes \
            --no-wait \
            -o none 2>/dev/null || true
    fi

    if [[ -n "${CREATED_AZURE_CREDS_FILE}" && -f "${CREATED_AZURE_CREDS_FILE}" ]]; then
        rm -f "${CREATED_AZURE_CREDS_FILE}"
    fi
}

# ── Upload Tests: Run ────────────────────────────────────────────────────────

function teardown() {
    log "Cleaning up test resources..."
    teardown_aws 2>/dev/null || true
    teardown_azure 2>/dev/null || true
    log "Cleanup complete."
}

function run_upload() {
    local provider="${1:-all}"
    local aws_ok=false azure_ok=false
    local test_result=0

    # Detect available providers
    case "${provider}" in
        aws)
            aws_ok=true
            ;;
        azure)
            azure_ok=true
            ;;
        all)
            if command -v aws &>/dev/null && aws sts get-caller-identity &>/dev/null 2>&1; then
                aws_ok=true
            else
                log "AWS CLI not authenticated, skipping S3 tests"
            fi
            if command -v az &>/dev/null && az account show &>/dev/null 2>&1; then
                azure_ok=true
            else
                log "Azure CLI not authenticated, skipping Azure Blob tests"
            fi
            ;;
    esac

    if [[ "${aws_ok}" == "false" && "${azure_ok}" == "false" ]]; then
        err "No cloud provider authenticated. Run 'aws configure' and/or 'az login' first."
    fi

    # Ensure cleanup runs on exit
    trap 'teardown' EXIT

    # Setup
    if [[ "${aws_ok}" == "true" ]]; then
        setup_aws
    fi
    if [[ "${azure_ok}" == "true" ]]; then
        setup_azure
    fi

    # Run tests
    log "Running etcd-upload integration tests..."
    go test -tags integration -v ./test/integration/oadp/upload/... || test_result=$?

    return ${test_result}
}

# ── Main ─────────────────────────────────────────────────────────────────────

if [[ $# -eq 0 ]]; then
    echo "Usage: $0 {cli|upload [aws|azure]}"
    exit 1
fi

command="${1}"
shift
case "${command}" in
    "cli")    run_cli ;;
    "upload") run_upload "${1:-all}" ;;
    *)
        echo "Unknown command: ${command}"
        echo "Usage: $0 {cli|upload [aws|azure]}"
        exit 1
        ;;
esac
