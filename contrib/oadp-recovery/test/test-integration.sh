#!/usr/bin/env bash
#
# OADP Recovery Integration Test
#
# This script runs a complete integration test for the OADP recovery functionality:
# 1. Scales down HyperShift Operator for isolation
# 2. Creates test clusters (some paused by OADP)
# 3. Runs the recovery script (real changes)
# 4. Verifies all clusters are unpaused and annotations removed
# 5. Cleans up completely
# 6. Scales HyperShift Operator back up

set -euo pipefail

# Configuration
NUM_CLUSTERS=${NUM_CLUSTERS:-5}
BASE_NAME=${BASE_NAME:-test-cluster}
NAMESPACE="test-oadp-recovery"
TIMEOUT_OPERATOR=120
TIMEOUT_CLEANUP=300

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

log_step() {
    echo -e "${BLUE}[STEP]${NC} $*"
}

# Cleanup function to run on exit
cleanup_on_exit() {
    local exit_code=$?
    if [[ $exit_code -ne 0 ]]; then
        log_error "Integration test failed! Cleaning up..."
        cleanup_test_resources
        scale_operator_up
    fi
    exit $exit_code
}

# Set trap for cleanup on failure
trap cleanup_on_exit ERR INT TERM

# Function to scale operator down
scale_operator_down() {
    log_step "🛑 Scaling down HyperShift Operator for test isolation..."
    kubectl scale deployment operator -n hypershift --replicas=0

    log_info "⏳ Waiting for operator pods to terminate..."
    kubectl wait --for=delete pod -l name=operator -n hypershift --timeout=${TIMEOUT_OPERATOR}s || true
    log_info "✅ HyperShift Operator scaled down"
}

# Function to scale operator up
scale_operator_up() {
    log_step "🚀 Scaling HyperShift Operator back up..."
    kubectl scale deployment operator -n hypershift --replicas=1

    log_info "⏳ Waiting for operator to be ready..."
    kubectl wait --for=condition=available deployment operator -n hypershift --timeout=${TIMEOUT_OPERATOR}s
    log_info "✅ HyperShift Operator scaled back up"
}

# Function to clean up test resources
cleanup_test_resources() {
    log_step "🧹 Cleaning up test resources..."

    # Delete resources in proper order
    kubectl delete np -n $NAMESPACE --all --ignore-not-found=true >/dev/null 2>&1 || true
    kubectl delete hc -n $NAMESPACE --all --ignore-not-found=true >/dev/null 2>&1 || true

    # Wait a bit for finalizers
    log_info "⏳ Waiting for resources to be deleted..."
    sleep 5

    # Delete namespace
    kubectl delete namespace $NAMESPACE --ignore-not-found=true >/dev/null 2>&1 || true

    # Wait for namespace deletion
    log_info "⏳ Waiting for namespace cleanup to complete..."
    kubectl wait --for=delete namespace $NAMESPACE --timeout=${TIMEOUT_CLEANUP}s 2>/dev/null || true

    log_info "✅ Test environment cleaned up"
}

# Function to create test clusters
create_test_clusters() {
    log_step "📦 Creating test clusters..."

    log_info "Environment variables:"
    log_info "  NUM_CLUSTERS=$NUM_CLUSTERS"
    log_info "  BASE_NAME=$BASE_NAME"
    echo ""

    NUM_CLUSTERS=$NUM_CLUSTERS BASE_NAME=$BASE_NAME ./test/create-test-clusters.sh
}

# Function to run recovery test
run_recovery_test() {
    log_step "🚀 Testing OADP recovery functionality..."

    log_info "🔍 Before recovery - checking cluster states..."
    kubectl get hostedcluster -n $NAMESPACE -o custom-columns="NAME:.metadata.name,PAUSED:.spec.pausedUntil,OADP_BY:.metadata.annotations.oadp\.openshift\.io/paused-by" --no-headers || true
    echo ""

    log_info "🚀 Running OADP recovery (REAL execution - no dry-run)..."
    ./oadp-recovery.sh --log-level verbose

    log_info "✅ OADP recovery test completed"
}

# Function to verify results
verify_results() {
    log_step "🔍 Verifying integration test results..."

    local failed=false

    log_info "📊 HostedClusters status (checking for successful recovery):"

    while IFS=' ' read -r name paused oadp; do
        if [[ -z "$name" ]]; then
            continue
        fi

        if [[ "$paused" != "<none>" ]]; then
            log_error "  ❌ $name - Still paused: $paused"
            failed=true
        elif [[ "$oadp" != "<none>" ]]; then
            log_error "  ❌ $name - Still has OADP annotations: $oadp"
            failed=true
        else
            log_info "  ✅ $name - Successfully unpaused and no OADP annotations"
        fi
    done <<< "$(kubectl get hostedcluster -n $NAMESPACE -o custom-columns="NAME:.metadata.name,PAUSED:.spec.pausedUntil,OADP_ANNOTATION:.metadata.annotations.oadp\.openshift\.io/paused-by" --no-headers 2>/dev/null || true)"

    echo ""
    log_info "📊 NodePools status (checking for successful recovery):"

    while IFS=' ' read -r name paused oadp; do
        if [[ -z "$name" ]]; then
            continue
        fi

        if [[ "$paused" != "<none>" ]]; then
            log_error "  ❌ $name - Still paused: $paused"
            failed=true
        elif [[ "$oadp" != "<none>" ]]; then
            log_error "  ❌ $name - Still has OADP annotations: $oadp"
            failed=true
        else
            log_info "  ✅ $name - Successfully unpaused and no OADP annotations"
        fi
    done <<< "$(kubectl get nodepool -n $NAMESPACE -o custom-columns="NAME:.metadata.name,PAUSED:.spec.pausedUntil,OADP_ANNOTATION:.metadata.annotations.oadp\.openshift\.io/paused-by" --no-headers 2>/dev/null || true)"

    echo ""
    log_info "🎯 Expected results:"
    log_info "  - All clusters should be unpaused (pausedUntil: <none>)"
    log_info "  - No clusters should have OADP annotations"
    log_info "  - This verifies the OADP recovery script worked correctly"

    if [[ "$failed" == "true" ]]; then
        log_error "❌ Verification failed! Some resources were not properly recovered."
        return 1
    fi

    log_info "✅ Verification completed successfully"
}

# Main function
main() {
    log_info "🎬 Starting OADP Recovery Integration Test..."
    echo ""

    # Pre-flight checks
    if ! command -v kubectl &> /dev/null; then
        log_error "kubectl is not available. Please install it."
        exit 1
    fi

    if ! kubectl cluster-info &>/dev/null; then
        log_error "Cannot connect to Kubernetes cluster. Please check your kubeconfig."
        exit 1
    fi

    # Step 1: Scale operator down
    scale_operator_down
    echo ""

    # Step 2: Clean up any existing test resources
    cleanup_test_resources
    echo ""

    # Step 3: Create test clusters
    create_test_clusters
    echo ""

    # Step 4: Run recovery test
    run_recovery_test
    echo ""

    # Step 5: Verify results
    verify_results
    echo ""

    # Step 6: Clean up test resources
    cleanup_test_resources
    echo ""

    # Step 7: Scale operator back up
    scale_operator_up
    echo ""

    # Success summary
    log_info "🎉 Full integration test completed successfully!"
    log_info "📊 Results:"
    log_info "  ✅ HyperShift Operator scaled down for isolation"
    log_info "  ✅ Test clusters created with OADP annotations"
    log_info "  ✅ OADP Recovery script executed successfully (real changes)"
    log_info "  ✅ Verification completed - all clusters unpaused and no OADP annotations"
    log_info "  ✅ Test resources cleaned up"
    log_info "  ✅ HyperShift Operator scaled back up"
    echo ""
    log_info "✨ The test environment is clean and ready for the next run!"
}

# Execute main function
main "$@"