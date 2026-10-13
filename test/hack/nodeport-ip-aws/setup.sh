#!/bin/bash

set -euo pipefail

# Adjust to point to your AWS credentials file
AWSCREDS="${AWSCREDS:-${HOME}/.aws/credentials}"

# Adjust to point to your desired SSH public and private key file
SSHPUBLICKEY="${SSHPUBLICKEY:-${HOME}/.ssh/id_rsa.pub}"

# Adjust to point to the right hypershift binary
HYPERSHIFT="${HYPERSHIFT:-./bin/hypershift}"

# Obtain the infra-id and region from the current management cluster
INFRAID="$(oc get infrastructure/cluster -o jsonpath='{ .status.infrastructureName }')"
REGION="$(oc get infrastructure/cluster -o jsonpath='{ .status.platformStatus.aws.region }')"

export AWS_SHARED_CREDENTIALS_FILE="${AWSCREDS}"

# Create a bastion machine for the management cluster
${HYPERSHIFT} create bastion aws \
   --aws-creds "${AWSCREDS}" \
   --infra-id "${INFRAID}" \
   --region "${REGION}" \
   --ssh-key-file "${SSHPUBLICKEY}"

# Query a description of the bastion machine
INSTANCEFILE="$(mktemp)"
aws ec2 describe-instances --filters "Name=tag:Name,Values=${INFRAID}-bastion" > "${INSTANCEFILE}"

# Obtain the machine's public IP
PUBLICIP="$(cat "${INSTANCEFILE}" | jq -r '.Reservations[].Instances[] | select(.State.Name == "running") | .PublicIpAddress')"

# Obtain the machine's security group
SGID="$(cat "${INSTANCEFILE}" | jq -r '.Reservations[].Instances[] | select(.State.Name == "running") | .SecurityGroups[0].GroupId')"

# Add permission for service port range to security group
aws ec2 authorize-security-group-ingress \
   --group-id "${SGID}" \
   --protocol tcp \
   --port 30000-32767 \
   --cidr "0.0.0.0/0"

# Find the master machines security group
MASTERSGID="$(aws ec2 describe-security-groups --filters "Name=tag:Name,Values=${INFRAID}-master-sg" | jq -r '.SecurityGroups[0].GroupId')"

# Allow the master machines access from the bastion sg
aws ec2 authorize-security-group-ingress \
   --group-id "${MASTERSGID}" \
   --protocol tcp \
   --port 30000-32767 \
   --source-group "${SGID}"

# Find IP of a master machine
MASTERIP="$(aws ec2 describe-instances --filters Name=tag:Name,Values=${INFRAID}-master-0 | jq -r '.Reservations[].Instances[0].PrivateIpAddress')"

# Enable IP forwarding on bastion machine
ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no "ec2-user@${PUBLICIP}" \
  sudo sysctl -w net.ipv4.ip_forward=1

# Install HA Proxy on bastion machine
ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no "ec2-user@${PUBLICIP}" \
  sudo yum -y install haproxy

# Configure HA Proxy on bastion machine
ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no "ec2-user@${PUBLICIP}" \
  "sudo cat << EOF | sudo tee /etc/haproxy/haproxy.cfg
listen  router *:30000-32767
    timeout connect         10s
    timeout client          1m
    timeout server          1m
    mode tcp
    server master ${MASTERIP}
EOF"

# Restart haproxy on bastion machine
ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no "ec2-user@${PUBLICIP}" \
  sudo systemctl restart haproxy

echo "Setup successful."
echo "Your nodeport address is ${PUBLICIP}"
