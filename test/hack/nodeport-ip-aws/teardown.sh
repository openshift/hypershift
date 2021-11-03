#!/bin/bash

set -euo pipefail
set -x

# Adjust to point to your AWS credentials file
AWSCREDS="${AWSCREDS:-${HOME}/.aws/credentials}" 

# Adjust to point to the right hypershift binary
HYPERSHIFT="${HYPERSHIFT:-./bin/hypershift}"

# Obtain the infra-id and region from the current management cluster
INFRAID="$(oc get infrastructure/cluster -o jsonpath='{ .status.infrastructureName }')"
REGION="$(oc get infrastructure/cluster -o jsonpath='{ .status.platformStatus.aws.region }')"

export AWS_SHARED_CREDENTIALS_FILE="${AWSCREDS}"

SGID="$(aws ec2 describe-security-groups --filters Name=tag:Name,Values=${INFRAID}-bastion-sg | jq -r '.SecurityGroups[].GroupId')"

if [[ ! -z "$SGID" ]]; then
  # Find the master machines security group
  MASTERSGID="$(aws ec2 describe-security-groups --filters "Name=tag:Name,Values=${INFRAID}-master-sg" | jq -r '.SecurityGroups[0].GroupId')"

  # Allow the master machines access from the bastion sg
  aws ec2 revoke-security-group-ingress \
    --group-id "${MASTERSGID}" \
    --protocol tcp \
    --port 30000-32767 \
    --source-group "${SGID}"
fi

${HYPERSHIFT} destroy bastion aws --aws-creds "${AWSCREDS}" --infra-id "${INFRAID}" --region "${REGION}"
