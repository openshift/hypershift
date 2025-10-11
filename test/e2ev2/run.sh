#!/bin/bash

# HyperShift E2E v2 Test Runner
# This script provides a convenient way to run the e2ev2 test suite

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# Default values
PLATFORM="${PLATFORM:-aws}"
ARTIFACT_DIR="${ARTIFACT_DIR:-/tmp/hypershift-e2e-artifacts}"
PARALLEL="${PARALLEL:-false}"
FOCUS="${FOCUS:-}"
SKIP="${SKIP:-}"
JUNIT_REPORT="${JUNIT_REPORT:-true}"
VERBOSE="${VERBOSE:-true}"

# Usage function
usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Run HyperShift E2E v2 tests with Ginkgo

OPTIONS:
    -p, --platform PLATFORM     Platform to test (aws, azure, kubevirt) [default: aws]
    -a, --artifact-dir DIR       Directory for test artifacts [default: /tmp/hypershift-e2e-artifacts]
    -f, --focus REGEX           Run only tests matching this regex
    -s, --skip REGEX            Skip tests matching this regex
    -j, --junit                 Generate JUnit XML report [default: true]
    -v, --verbose               Verbose output [default: true]
    --parallel                  Run tests in parallel
    --dry-run                   Show what would be run without executing
    -h, --help                  Show this help message

EXAMPLES:
    # Run all tests on AWS
    $0 --platform aws

    # Run only basic cluster tests
    $0 --focus "Basic Cluster"

    # Run tests with custom artifact directory
    $0 --artifact-dir ./test-results

    # Run tests in parallel (experimental)
    $0 --parallel

    # Skip slow tests
    $0 --skip "slow"

ENVIRONMENT VARIABLES:
    PLATFORM                    Same as --platform
    ARTIFACT_DIR               Same as --artifact-dir
    FOCUS                      Same as --focus
    SKIP                       Same as --skip
    PARALLEL                   Set to 'true' to enable parallel execution
    JUNIT_REPORT              Set to 'false' to disable JUnit reporting
    VERBOSE                   Set to 'false' to disable verbose output

REQUIRED ENVIRONMENT VARIABLES (platform-specific):
    AWS:
        AWS_CREDENTIALS_FILE    Path to AWS credentials file
        AWS_REGION             AWS region [default: us-east-1]
        AWS_OIDC_S3_BUCKET_NAME S3 bucket for OIDC provider

    Azure:
        AZURE_CREDENTIALS_FILE  Path to Azure credentials file
        AZURE_LOCATION         Azure location [default: eastus]

    KubeVirt:
        KUBEVIRT_INFRA_KUBECONFIG Path to infra cluster kubeconfig
        KUBEVIRT_INFRA_NAMESPACE  Infra cluster namespace

    All platforms:
        PULL_SECRET_FILE        Path to pull secret file
        SSH_KEY_FILE           Path to SSH public key file
        BASE_DOMAIN            Base domain for cluster ingress
        LATEST_RELEASE_IMAGE   Latest OCP release image

EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -p|--platform)
            PLATFORM="$2"
            shift 2
            ;;
        -a|--artifact-dir)
            ARTIFACT_DIR="$2"
            shift 2
            ;;
        -f|--focus)
            FOCUS="$2"
            shift 2
            ;;
        -s|--skip)
            SKIP="$2"
            shift 2
            ;;
        -j|--junit)
            JUNIT_REPORT="true"
            shift
            ;;
        -v|--verbose)
            VERBOSE="true"
            shift
            ;;
        --parallel)
            PARALLEL="true"
            shift
            ;;
        --dry-run)
            DRY_RUN="true"
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

# Validate platform
case $PLATFORM in
    aws|azure|kubevirt|openstack|powervs|none)
        ;;
    *)
        echo "Error: Unsupported platform '$PLATFORM'"
        echo "Supported platforms: aws, azure, kubevirt, openstack, powervs, none"
        exit 1
        ;;
esac

# Create artifact directory
mkdir -p "$ARTIFACT_DIR"

# Build test binary and ginkgo if they don't exist
echo "Building e2ev2 test binary..."
cd "$ROOT_DIR"
make e2ev2

# Construct ginkgo command
GINKGO_CMD=("$ROOT_DIR/bin/ginkgo")

# Add ginkgo flags
if [[ "$VERBOSE" == "true" ]]; then
    GINKGO_CMD+=("-v")
fi

if [[ "$PARALLEL" == "true" ]]; then
    GINKGO_CMD+=("-p")
fi

if [[ -n "$FOCUS" ]]; then
    GINKGO_CMD+=("--focus" "$FOCUS")
fi

if [[ -n "$SKIP" ]]; then
    GINKGO_CMD+=("--skip" "$SKIP")
fi

if [[ "$JUNIT_REPORT" == "true" ]]; then
    JUNIT_FILE="$ARTIFACT_DIR/junit-e2ev2.xml"
    GINKGO_CMD+=("--junit-report" "$JUNIT_FILE")
fi

# Add output directory for other artifacts
GINKGO_CMD+=("--output-dir" "$ARTIFACT_DIR")

# Add test binary
GINKGO_CMD+=("$ROOT_DIR/bin/test-e2ev2")

# Add test-specific flags
GINKGO_CMD+=("--")
GINKGO_CMD+=("-e2e.platform" "$PLATFORM")
GINKGO_CMD+=("-e2e.artifact-dir" "$ARTIFACT_DIR")

# Add platform-specific flags
case $PLATFORM in
    aws)
        if [[ -n "${AWS_CREDENTIALS_FILE:-}" ]]; then
            GINKGO_CMD+=("-e2e.aws-credentials-file" "$AWS_CREDENTIALS_FILE")
        fi
        if [[ -n "${AWS_REGION:-}" ]]; then
            GINKGO_CMD+=("-e2e.aws-region" "$AWS_REGION")
        fi
        if [[ -n "${AWS_OIDC_S3_BUCKET_NAME:-}" ]]; then
            GINKGO_CMD+=("-e2e.aws-oidc-s3-bucket-name" "$AWS_OIDC_S3_BUCKET_NAME")
        fi
        ;;
    azure)
        if [[ -n "${AZURE_CREDENTIALS_FILE:-}" ]]; then
            GINKGO_CMD+=("-e2e.azure-credentials-file" "$AZURE_CREDENTIALS_FILE")
        fi
        if [[ -n "${AZURE_LOCATION:-}" ]]; then
            GINKGO_CMD+=("-e2e.azure-location" "$AZURE_LOCATION")
        fi
        ;;
    kubevirt)
        if [[ -n "${KUBEVIRT_INFRA_KUBECONFIG:-}" ]]; then
            GINKGO_CMD+=("-e2e.kubevirt-infra-kubeconfig" "$KUBEVIRT_INFRA_KUBECONFIG")
        fi
        if [[ -n "${KUBEVIRT_INFRA_NAMESPACE:-}" ]]; then
            GINKGO_CMD+=("-e2e.kubevirt-infra-namespace" "$KUBEVIRT_INFRA_NAMESPACE")
        fi
        ;;
esac

# Add common optional flags
if [[ -n "${PULL_SECRET_FILE:-}" ]]; then
    GINKGO_CMD+=("-e2e.pull-secret-file" "$PULL_SECRET_FILE")
fi

if [[ -n "${SSH_KEY_FILE:-}" ]]; then
    GINKGO_CMD+=("-e2e.ssh-key-file" "$SSH_KEY_FILE")
fi

if [[ -n "${BASE_DOMAIN:-}" ]]; then
    GINKGO_CMD+=("-e2e.base-domain" "$BASE_DOMAIN")
fi

if [[ -n "${LATEST_RELEASE_IMAGE:-}" ]]; then
    GINKGO_CMD+=("-e2e.latest-release-image" "$LATEST_RELEASE_IMAGE")
fi

if [[ -n "${PREVIOUS_RELEASE_IMAGE:-}" ]]; then
    GINKGO_CMD+=("-e2e.previous-release-image" "$PREVIOUS_RELEASE_IMAGE")
fi

# Print command if dry run
if [[ "${DRY_RUN:-}" == "true" ]]; then
    echo "Would run:"
    printf '%q ' "${GINKGO_CMD[@]}"
    echo
    exit 0
fi

# Print configuration
echo "=========================================="
echo "HyperShift E2E v2 Test Configuration"
echo "=========================================="
echo "Platform: $PLATFORM"
echo "Artifact Directory: $ARTIFACT_DIR"
echo "Parallel Execution: $PARALLEL"
echo "JUnit Report: $JUNIT_REPORT"
echo "Verbose Output: $VERBOSE"
if [[ -n "$FOCUS" ]]; then
    echo "Focus Pattern: $FOCUS"
fi
if [[ -n "$SKIP" ]]; then
    echo "Skip Pattern: $SKIP"
fi
echo "=========================================="

# Run the tests
echo "Starting e2ev2 tests..."
echo "Command: ${GINKGO_CMD[*]}"
echo

exec "${GINKGO_CMD[@]}"