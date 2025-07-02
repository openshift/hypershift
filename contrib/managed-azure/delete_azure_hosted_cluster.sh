#!/bin/bash
set -x

# Constants
HYPERSHIFT_BINARY_PATH="/User/hypershift/bin"
AZURE_CREDS="/Users/user/.azure/credentials"
HC_NAME="azure-hosted-cluster"

${HYPERSHIFT_BINARY_PATH}/hypershift destroy cluster azure \
--name $HC_NAME \
--azure-creds $AZURE_CREDS

set +x