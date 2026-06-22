#!/bin/bash

set -euo pipefail

REGION="${HYPERSHIFT_REGION:-us-east-1}"
BUCKET_NAME="${HYPERSHIFT_BUCKET_NAME:-hypershift-ci-oidc}"
AWSCREDS="${HYPERSHIFT_AWS_CREDS:-$HOME/.aws/credentials}"

# Install hypershift
echo "Installing hypershift"
./bin/hypershift install \
  --oidc-storage-provider-s3-credentials "${AWSCREDS}" \
  --oidc-storage-provider-s3-region "${REGION}" \
  --oidc-storage-provider-s3-bucket-name "${BUCKET_NAME}" \
  --enable-defaulting-webhook=false \
  --enable-conversion-webhook=false \
  --enable-validating-webhook=false \
  --development=true