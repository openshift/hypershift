#!/bin/bash
set -o nounset

# Verifies that all required Azure resources exist for the managed-azure HyperShift setup.
# Run from your dev/ directory (where user-vars.sh lives).
#
# Usage:
#   ../contrib/managed-azure/check_resources.sh [--first-time] [--cluster]
#
# Flags:
#   --first-time   Check one-time setup resources (Key Vault, SPs, OIDC, managed identities)
#   --cluster      Check per-cluster resources (AKS, DNS, HyperShift operator, hosted cluster)
#   (no flags)     Check everything

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ -f "${SCRIPT_DIR}/vars.sh" ]]; then
    source "${SCRIPT_DIR}/vars.sh"
else
    echo "Error: vars.sh not found. Run this from your dev/ directory."
    exit 1
fi

# Check Azure login
if ! az account show >/dev/null 2>&1; then
    echo "Error: Not logged into Azure. Run 'az login' first."
    exit 1
fi

PASS=0
FAIL=0
WARN=0

pass() {
    echo "  ✓ $1"
    PASS=$((PASS + 1))
}
fail() {
    echo "  ✗ $1"
    FAIL=$((FAIL + 1))
}
warn() {
    echo "  ⚠ $1"
    WARN=$((WARN + 1))
}

check_resource() {
    local label="$1"
    shift
    if "$@" >/dev/null 2>&1; then
        pass "$label"
    else
        fail "$label"
    fi
}

check_local_file() {
    local label="$1"
    local path="$2"
    if [[ -f "$path" ]]; then
        pass "$label ($path)"
    else
        fail "$label ($path)"
    fi
}

CHECK_FIRST_TIME=false
CHECK_CLUSTER=false

for arg in "$@"; do
    case "$arg" in
    --first-time) CHECK_FIRST_TIME=true ;;
    --cluster) CHECK_CLUSTER=true ;;
    *)
        echo "Unknown flag: $arg"
        exit 1
        ;;
    esac
done

# Default: check everything
if ! $CHECK_FIRST_TIME && ! $CHECK_CLUSTER; then
    CHECK_FIRST_TIME=true
    CHECK_CLUSTER=true
fi

echo ""
echo "=== Azure Credentials (setup_azure_creds.sh) ==="
check_local_file "Azure credentials" "${AZURE_CREDS}"

if $CHECK_FIRST_TIME; then
    echo ""
    echo "=== Key Vault (setup_MIv3_kv.sh) ==="
    check_resource "Key Vault '${PREFIX}'" \
        az keyvault show --name "${PREFIX}" --resource-group "${PERSISTENT_RG_NAME}"

    echo ""
    echo "=== Control Plane Service Principals (setup_MIv3_kv.sh) ==="
    for sp in "cloud-provider-${PREFIX}" "cpo-${PREFIX}" "azure-disk-${PREFIX}" \
        "azure-file-${PREFIX}" "ciro-${PREFIX}" "ingress-${PREFIX}" \
        "cncc-${PREFIX}" "nodepool-mgmt-${PREFIX}" "velero-${PREFIX}"; do
        SP_MATCHES=$(az ad sp list --display-name "$sp" --query "[?displayName=='$sp'].appId" -o tsv 2>/dev/null)
        SP_COUNT=$(echo "$SP_MATCHES" | grep -c . 2>/dev/null || echo 0)
        if [[ "$SP_COUNT" -eq 1 ]]; then
            pass "SP '$sp'"
        elif [[ "$SP_COUNT" -gt 1 ]]; then
            fail "SP '$sp' (multiple matches found — expected exactly 1)"
        else
            fail "SP '$sp'"
        fi
    done

    echo ""
    echo "=== Key Vault Certificates & Secrets (setup_MIv3_kv.sh) ==="
    if az keyvault show --name "${PREFIX}" --resource-group "${PERSISTENT_RG_NAME}" >/dev/null 2>&1; then
        KV_CERTS=$(az keyvault certificate list --vault-name "${PREFIX}" --query "[].name" -o tsv 2>&1)
        if [[ $? -ne 0 ]]; then
            fail "Key Vault certificates (query failed: $KV_CERTS)"
        else
            for cert in "cloud-provider-${PREFIX}" "cpo-${PREFIX}" "azure-disk-${PREFIX}" \
                "azure-file-${PREFIX}" "ciro-${PREFIX}" "ingress-${PREFIX}" \
                "cncc-${PREFIX}" "nodepool-mgmt-${PREFIX}" "velero-${PREFIX}"; do
                if echo "$KV_CERTS" | grep -qx "$cert"; then
                    pass "Certificate '$cert'"
                else
                    fail "Certificate '$cert'"
                fi
            done
        fi

        KV_SECRETS=$(az keyvault secret list --vault-name "${PREFIX}" --query "[].name" -o tsv 2>&1)
        if [[ $? -ne 0 ]]; then
            fail "Key Vault secrets (query failed: $KV_SECRETS)"
        else
            for secret in "cloud-provider-${PREFIX}-json" "cpo-${PREFIX}-json" "azure-disk-${PREFIX}-json" \
                "azure-file-${PREFIX}-json" "ciro-${PREFIX}-json" "ingress-${PREFIX}-json" \
                "cncc-${PREFIX}-json" "nodepool-mgmt-${PREFIX}-json" "velero-${PREFIX}-json"; do
                if echo "$KV_SECRETS" | grep -qx "$secret"; then
                    pass "Secret '$secret'"
                else
                    fail "Secret '$secret'"
                fi
            done
        fi
    else
        fail "Key Vault not accessible — cannot check secrets"
    fi

    echo ""
    echo "=== Control Plane Output File (setup_MIv3_kv.sh) ==="
    check_local_file "cp-output.json" "${CP_OUTPUT_FILE}"

    echo ""
    echo "=== OIDC Provider (setup_oidc_provider.sh) ==="
    check_resource "Storage Account '${OIDC_ISSUER_NAME}'" \
        az storage account show --name "${OIDC_ISSUER_NAME}" --resource-group "${PERSISTENT_RG_NAME}"

    BLOB_COUNT=$(az storage blob list --account-name "${OIDC_ISSUER_NAME}" --container-name "${OIDC_ISSUER_NAME}" --query "length([])" -o tsv 2>/dev/null)
    if [[ -n "$BLOB_COUNT" && "$BLOB_COUNT" -ge 2 ]] 2>/dev/null; then
        pass "OIDC discovery documents ($BLOB_COUNT blobs)"
    else
        fail "OIDC discovery documents (expected >= 2, got ${BLOB_COUNT:-0})"
    fi

    check_local_file "SA signing private key" "${SA_TOKEN_ISSUER_PRIVATE_KEY_PATH}"
    check_local_file "SA signing public key" "${SA_TOKEN_ISSUER_PUBLIC_KEY_PATH}"

    echo ""
    echo "=== Data Plane Managed Identities (setup_dataplane_identities.sh) ==="
    for mi in "azure-disk-MI-${PREFIX}" "azure-file-MI-${PREFIX}" "image-registry-MI-${PREFIX}"; do
        check_resource "MI '$mi'" \
            az identity show --name "$mi" --resource-group "${PERSISTENT_RG_NAME}"
    done

    echo ""
    echo "=== Data Plane Federated Credentials (setup_dataplane_identities.sh) ==="
    for mi in "azure-disk-MI-${PREFIX}" "azure-file-MI-${PREFIX}" "image-registry-MI-${PREFIX}"; do
        FC_COUNT=$(az identity federated-credential list --identity-name "$mi" --resource-group "${PERSISTENT_RG_NAME}" --query "length([])" -o tsv 2>/dev/null)
        if [[ -n "$FC_COUNT" && "$FC_COUNT" -ge 1 ]] 2>/dev/null; then
            pass "Federated credential on '$mi'"
        else
            fail "Federated credential on '$mi'"
        fi
    done

    echo ""
    echo "=== Data Plane Output File (setup_dataplane_identities.sh) ==="
    check_local_file "dp-output.json" "${DP_OUTPUT_FILE}"

    echo ""
    echo "=== AKS Managed Identities (setup_aks_mi.sh) ==="
    check_resource "MI '${PREFIX}-aks-mi'" \
        az identity show --name "${PREFIX}-aks-mi" --resource-group "${PERSISTENT_RG_NAME}"
    check_resource "MI '${PREFIX}-aks-kubelet-mi'" \
        az identity show --name "${PREFIX}-aks-kubelet-mi" --resource-group "${PERSISTENT_RG_NAME}"
fi

if $CHECK_CLUSTER; then
    echo ""
    echo "=== AKS Cluster (setup_aks_cluster.sh) ==="
    check_resource "Resource Group '${AKS_RG}'" \
        az group show --name "${AKS_RG}"
    check_resource "AKS Cluster '${AKS_CLUSTER_NAME}'" \
        az aks show --name "${AKS_CLUSTER_NAME}" --resource-group "${AKS_RG}"

    echo ""
    echo "=== DNS (setup_external_dns.sh) ==="
    check_resource "DNS Zone '${MGMT_DNS_ZONE_NAME}'" \
        az network dns zone show --name "${MGMT_DNS_ZONE_NAME}" --resource-group "${PERSISTENT_RG_NAME}"

    DNS_SP_NAME="${PREFIX}-ExternalDnsServicePrincipal"
    SP_MATCHES=$(az ad sp list --display-name "$DNS_SP_NAME" --query "[?displayName=='$DNS_SP_NAME'].appId" -o tsv 2>/dev/null)
    SP_COUNT=$(echo "$SP_MATCHES" | grep -c . 2>/dev/null || echo 0)
    if [[ "$SP_COUNT" -eq 1 ]]; then
        pass "SP '$DNS_SP_NAME'"
    elif [[ "$SP_COUNT" -gt 1 ]]; then
        fail "SP '$DNS_SP_NAME' (multiple matches found — expected exactly 1)"
    else
        fail "SP '$DNS_SP_NAME'"
    fi

    check_local_file "External DNS credentials" "${EXTERNAL_DNS_SERVICE_PRINCIPAL_FILEPATH}"

    echo ""
    echo "=== HyperShift Operator (setup_install_ho_on_aks.sh) ==="
    if kubectl cluster-info >/dev/null 2>&1; then
        HO_PODS=$(kubectl get pods -n hypershift --no-headers 2>/dev/null | grep -c "Running")
        if [[ "$HO_PODS" -ge 1 ]]; then
            pass "HyperShift operator pods running ($HO_PODS)"
        else
            fail "HyperShift operator pods not running"
        fi
    else
        fail "Cannot reach cluster — unable to check operator"
    fi

    echo ""
    echo "=== Hosted Cluster (create_basic_hosted_cluster.sh) ==="
    CLUSTER_NAME="${PREFIX}-hc"
    if kubectl cluster-info >/dev/null 2>&1; then
        HC_STATUS=$(kubectl get hostedcluster "${CLUSTER_NAME}" -n clusters -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null)
        if [[ "$HC_STATUS" == "True" ]]; then
            pass "HostedCluster '${CLUSTER_NAME}' is Available"
        elif [[ -n "$HC_STATUS" ]]; then
            warn "HostedCluster '${CLUSTER_NAME}' exists but not yet Available (status: $HC_STATUS)"
        else
            HC_EXISTS=$(kubectl get hostedcluster "${CLUSTER_NAME}" -n clusters 2>/dev/null)
            if [[ -n "$HC_EXISTS" ]]; then
                warn "HostedCluster '${CLUSTER_NAME}' exists but status unknown"
            else
                fail "HostedCluster '${CLUSTER_NAME}' not found"
            fi
        fi
    else
        fail "Cannot reach cluster — unable to check hosted cluster"
    fi
fi

echo ""
echo "==============================="
echo "Results: $PASS passed, $FAIL failed, $WARN warnings"
echo "==============================="

if [[ $FAIL -gt 0 ]]; then
    exit 1
fi
