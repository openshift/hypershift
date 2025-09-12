#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"


# Default values (can be overridden by environment variables)
E2E_ARTIFACT_DIR="${E2E_ARTIFACT_DIR:-./artifacts}"
E2E_LATEST_RELEASE_IMAGE="${E2E_LATEST_RELEASE_IMAGE:-}"
E2E_BASE_DOMAIN="${E2E_BASE_DOMAIN:-}"
E2E_CONTROL_PLANE_OPERATOR_IMAGE="${E2E_CONTROL_PLANE_OPERATOR_IMAGE:-}"
E2E_EXTERNAL_DNS_DOMAIN="${E2E_EXTERNAL_DNS_DOMAIN:-}"
E2E_PULL_SECRET_FILE="${E2E_PULL_SECRET_FILE:-}"
E2E_HYPERSHIFT_OPERATOR_LATEST_IMAGE="${E2E_HYPERSHIFT_OPERATOR_LATEST_IMAGE:-quay.io/hypershift/hypershift-operator:latest}"

# AWS specific defaults
E2E_AWS_CREDENTIALS_FILE="${E2E_AWS_CREDENTIALS_FILE:-}"
E2E_AWS_PRIVATE_CREDENTIALS_FILE="${E2E_AWS_PRIVATE_CREDENTIALS_FILE:-/etc/hypershift-pool-aws-credentials/credentials}"
E2E_AWS_PRIVATE_REGION="${E2E_AWS_PRIVATE_REGION:-us-east-1}"
E2E_AWS_OIDC_S3_CREDENTIALS="${E2E_AWS_OIDC_S3_CREDENTIALS:-/etc/hypershift-pool-aws-credentials/credentials}"
E2E_AWS_OIDC_S3_REGION="${E2E_AWS_OIDC_S3_REGION:-us-east-1}"
E2E_AWS_OIDC_S3_BUCKET_NAME="${E2E_AWS_OIDC_S3_BUCKET_NAME:-}"
E2E_AWS_REGION="${E2E_AWS_REGION:-us-east-1}"

# External DNS defaults
E2E_EXTERNAL_DNS_PROVIDER="${E2E_EXTERNAL_DNS_PROVIDER:-aws}"
E2E_EXTERNAL_DNS_DOMAIN_FILTER="${E2E_EXTERNAL_DNS_DOMAIN_FILTER:-service.ci.hypershift.devcluster.openshift.com}"
E2E_EXTERNAL_DNS_CREDENTIALS="${E2E_EXTERNAL_DNS_CREDENTIALS:-/etc/hypershift-pool-aws-credentials/credentials}"

# Platform monitoring default
E2E_PLATFORM_MONITORING="${E2E_PLATFORM_MONITORING:-All}"

# Test execution options
E2E_DRY_RUN="${E2E_DRY_RUN:-false}"
E2E_TEST_TIMEOUT="${E2E_TEST_TIMEOUT:-60m}"
E2E_VERBOSE="${E2E_VERBOSE:-false}"

# Print usage information
usage() {
    cat << EOF
Usage: $0 [OPTIONS]

This script runs the HyperShift request serving e2e tests.

Environment Variables:
  E2E_ARTIFACT_DIR                      Directory for test artifacts (default: ./artifacts)
  E2E_LATEST_RELEASE_IMAGE              Latest OCP release image
  E2E_BASE_DOMAIN                       Ingress base domain for the cluster
  E2E_CONTROL_PLANE_OPERATOR_IMAGE      Control plane operator image
  E2E_EXTERNAL_DNS_DOMAIN               External DNS domain
  E2E_PULL_SECRET_FILE                  Path to pull secret file
  E2E_HYPERSHIFT_OPERATOR_LATEST_IMAGE  HyperShift operator image (default: quay.io/hypershift/hypershift-operator:latest)
  
  AWS specific:
  E2E_AWS_CREDENTIALS_FILE              Path to AWS credentials
  E2E_AWS_PRIVATE_CREDENTIALS_FILE      Path to AWS private credentials (default: /etc/hypershift-pool-aws-credentials/credentials)
  E2E_AWS_PRIVATE_REGION                AWS region for private clusters (default: us-east-1)
  E2E_AWS_OIDC_S3_CREDENTIALS           AWS S3 credentials for OIDC provider (default: /etc/hypershift-pool-aws-credentials/credentials)
  E2E_AWS_OIDC_S3_REGION                AWS S3 region for OIDC provider (default: us-east-1)
  E2E_AWS_OIDC_S3_BUCKET_NAME           AWS S3 bucket name for OIDC provider
  E2E_AWS_REGION                        AWS region for clusters (default: us-east-1)
  E2E_AWS_ZONES                         AWS zones for clusters (default: us-east-1a,us-east-1b,us-east-1c)
  
  External DNS:
  E2E_EXTERNAL_DNS_PROVIDER             External DNS provider (default: aws)
  E2E_EXTERNAL_DNS_DOMAIN_FILTER        External DNS domain filter (default: service.ci.hypershift.devcluster.openshift.com)
  E2E_EXTERNAL_DNS_CREDENTIALS          External DNS credentials path (default: /etc/hypershift-pool-aws-credentials/credentials)
  
  Other:
  E2E_PLATFORM_MONITORING               Platform monitoring option (default: All)
  E2E_DRY_RUN                           Dry run mode (default: false)
  E2E_TEST_TIMEOUT                      Test timeout (default: 60m)
  E2E_VERBOSE                           Verbose output (default: false)

Options:
  -h, --help                            Show this help message
  -v, --verbose                         Enable verbose output
  -d, --dry-run                         Enable dry run mode
  -t, --timeout TIMEOUT                 Set test timeout (e.g., 30m, 1h)

Examples:
  # Basic run with minimal configuration
  E2E_LATEST_RELEASE_IMAGE=quay.io/openshift-release-dev/ocp-release:4.14.0-x86_64 \\
  E2E_BASE_DOMAIN=example.com \\
  E2E_PULL_SECRET_FILE=./pull-secret.json \\
  ./run-reqserving-e2e.sh

  # Dry run to see what would be created
  E2E_DRY_RUN=true ./run-reqserving-e2e.sh

  # With custom artifact directory
  E2E_ARTIFACT_DIR=/tmp/test-artifacts ./run-reqserving-e2e.sh

EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            usage
            exit 0
            ;;
        -v|--verbose)
            E2E_VERBOSE="true"
            shift
            ;;
        -d|--dry-run)
            E2E_DRY_RUN="true"
            shift
            ;;
        -t|--timeout)
            E2E_TEST_TIMEOUT="$2"
            shift 2
            ;;
        *)
            echo "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

# Create artifact directory if it doesn't exist
mkdir -p "$E2E_ARTIFACT_DIR"

# Build the test command
TEST_CMD="${PROJECT_ROOT}/bin/test-reqserving -test.timeout $E2E_TEST_TIMEOUT"

if [[ "$E2E_VERBOSE" == "true" ]]; then
    TEST_CMD="$TEST_CMD -test.v"
fi

# Add flags
TEST_CMD="$TEST_CMD -e2e.artifact-dir=$E2E_ARTIFACT_DIR"

[[ -n "$E2E_LATEST_RELEASE_IMAGE" ]] && TEST_CMD="$TEST_CMD -e2e.latest-release-image=$E2E_LATEST_RELEASE_IMAGE"
[[ -n "$E2E_BASE_DOMAIN" ]] && TEST_CMD="$TEST_CMD -e2e.base-domain=$E2E_BASE_DOMAIN"
[[ -n "$E2E_CONTROL_PLANE_OPERATOR_IMAGE" ]] && TEST_CMD="$TEST_CMD -e2e.control-plane-operator-image=$E2E_CONTROL_PLANE_OPERATOR_IMAGE"
[[ -n "$E2E_EXTERNAL_DNS_DOMAIN" ]] && TEST_CMD="$TEST_CMD -e2e.external-dns-domain=$E2E_EXTERNAL_DNS_DOMAIN"
[[ -n "$E2E_PULL_SECRET_FILE" ]] && TEST_CMD="$TEST_CMD -e2e.pull-secret-file=$E2E_PULL_SECRET_FILE"

TEST_CMD="$TEST_CMD -e2e.hypershift-operator-latest-image=$E2E_HYPERSHIFT_OPERATOR_LATEST_IMAGE"

# AWS specific flags
[[ -n "$E2E_AWS_CREDENTIALS_FILE" ]] && TEST_CMD="$TEST_CMD -e2e.aws-credentials-file=$E2E_AWS_CREDENTIALS_FILE"
TEST_CMD="$TEST_CMD -e2e.aws-private-credentials-file=$E2E_AWS_PRIVATE_CREDENTIALS_FILE"
TEST_CMD="$TEST_CMD -e2e.aws-private-region=$E2E_AWS_PRIVATE_REGION"
TEST_CMD="$TEST_CMD -e2e.aws-oidc-s3-credentials=$E2E_AWS_OIDC_S3_CREDENTIALS"
TEST_CMD="$TEST_CMD -e2e.aws-oidc-s3-region=$E2E_AWS_OIDC_S3_REGION"
[[ -n "$E2E_AWS_OIDC_S3_BUCKET_NAME" ]] && TEST_CMD="$TEST_CMD -e2e.aws-oidc-s3-bucket-name=$E2E_AWS_OIDC_S3_BUCKET_NAME"
TEST_CMD="$TEST_CMD -e2e.aws-region=$E2E_AWS_REGION"
TEST_CMD="$TEST_CMD -e2e.availability-zones=$E2E_AWS_ZONES"

# External DNS flags
TEST_CMD="$TEST_CMD -e2e.external-dns-provider=$E2E_EXTERNAL_DNS_PROVIDER"
TEST_CMD="$TEST_CMD -e2e.external-dns-domain-filter=$E2E_EXTERNAL_DNS_DOMAIN_FILTER"
TEST_CMD="$TEST_CMD -e2e.external-dns-credentials=$E2E_EXTERNAL_DNS_CREDENTIALS"

# Platform monitoring flag
TEST_CMD="$TEST_CMD -e2e.platform-monitoring=$E2E_PLATFORM_MONITORING"

# Dry run flag
[[ "$E2E_DRY_RUN" == "true" ]] && TEST_CMD="$TEST_CMD -e2e.dry-run"

# Add the test package path
TEST_CMD="$TEST_CMD ./test/reqserving-e2e"

echo "Running request serving e2e tests..."
echo "Command: $TEST_CMD"
echo "Artifact directory: $E2E_ARTIFACT_DIR"
echo "Dry run: $E2E_DRY_RUN"
echo ""

# Change to the project root directory
cd "$(dirname "$0")"

# Execute the test command
echo "Executing: $TEST_CMD"
eval $TEST_CMD 
