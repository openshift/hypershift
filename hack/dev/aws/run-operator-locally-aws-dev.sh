#!/bin/bash

set -euo pipefail
export METRICS_SET="Telemetry"

export MY_NAMESPACE=hypershift
export MY_NAME=operator

REGION="${HYPERSHIFT_REGION:-us-east-1}"
BUCKET_NAME="${HYPERSHIFT_BUCKET_NAME:-hypershift-ci-oidc}"
AWSCREDS="${HYPERSHIFT_AWS_CREDS:-$HOME/.aws/credentials}"

# Run hypershift-operator locally.
echo "Running hypershift-operator locally..."
./bin/hypershift-operator \
  run \
  --metrics-addr=0 \
  --enable-ocp-cluster-monitoring=false \
  --enable-ci-debug-output=true \
  --private-platform=AWS \
  --enable-ci-debug-output=true \
  --oidc-storage-provider-s3-credentials "${AWSCREDS}" \
  --oidc-storage-provider-s3-region "${REGION}" \
  --oidc-storage-provider-s3-bucket-name "${BUCKET_NAME}" \
  --control-plane-operator-image=quay.io/hypershift/hypershift:latest