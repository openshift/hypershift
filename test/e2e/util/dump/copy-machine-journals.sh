#!/bin/bash

set -euo pipefail

DEST_DIR="${1:-.}"

# Script Variables
# ----------------

# INFRAID is only used if the BASTION_IP and INSTANCE_IPS are not provided. 
# It is used to query AWS for those.
INFRAID="${INFRAID:-}"

# BASTION_IP is the external IP of the bastion machine. If not present, 
BASTION_IP="${BASTION_IP:-}"

# INSTANCE_IPS is a space-separated list of machine IPs from which to download 
# the journal logs
INSTANCE_IPS="${INSTANCE_IPS:-}"

# SSH_PRIVATE_KEY is the path to a SSH private key to use for the ssh/scp commands.
# If empty, it is assumed that any required private keys have been added to the ssh 
# agent via ssh-add
SSH_PRIVATE_KEY="${SSH_PRIVATE_KEY:-}"

# AWS_MACHINE_PUBLIC_IPS is true if the cluster is using public IPs. If empty, it is assumed that the cluster is not using public IPs.
AWS_MACHINE_PUBLIC_IPS="${AWS_MACHINE_PUBLIC_IPS:-}"

if [[ -z "${BASTION_IP}" && -z "${AWS_MACHINE_PUBLIC_IPS}" ]]; then
  if [[ -z "${INFRAID}" ]]; then
    echo "INFRAID must be set when BASTION_IP is not set."
    exit 1
  fi
  BASTION_IP="$(aws ec2 describe-instances --filters "Name=tag:Name,Values=${INFRAID}-bastion" | jq -r '.Reservations[].Instances[].PublicIpAddress')"
  if [[ -z "${BASTION_IP}" ]]; then
    echo "Could not determine BASTION_IP. Ensure that a bastion has been created for your target cluster"
    exit 1
  fi
fi

if [[ -z "${INSTANCE_IPS}" ]]; then
  if [[ -z "${INFRAID}" ]]; then
    echo "INFRAID must be set when INSTANCE_IPS is not set."
    exit 1
  fi
  INSTANCE_IPS="$(aws ec2 describe-instances --filters "Name=tag:kubernetes.io/cluster/${INFRAID},Values=owned" \
    | jq -r "[ (.Reservations[].Instances[] | select(.Tags[] | select(.Key == \"Name\") | select(.Value != \"${INFRAID}-bastion\")) | .PrivateIpAddress) ] | join(\" \")")"
  if [[ -z "${INSTANCE_IPS}" ]]; then
    echo "Could not determine INSTANCE_IPS. Ensure that INFRAID is valid and that the cluster has worker nodes"
  fi
fi

function retryonfailure {
  for i in $(seq 1 10); do
    #echo "Attempt #${i}: $@"
    if ! "$@"; then
      sleep 1
    else 
      return 0
    fi
  done
  return 1
}

function dump_config {
  machine_ip="${1:-}"
  config_file="${2:-}"
  echo "Failed to ssh to AWS instance ${machine_ip}, dumping ssh configuration"
  cat "${config_file}" > "${DEST_DIR}/ssh-config-${machine_ip}.txt"
  return 1
}

failed_copy=0

function copylog {
  local machine_ip="${1}"
  local config_file="$(mktemp)"
  if [[ -z "${AWS_MACHINE_PUBLIC_IPS}" ]]; then
    cat << EOF > "${config_file}"
    Host bastion
        User                   ec2-user
        ForwardAgent           yes
        Hostname               ${BASTION_IP}
        StrictHostKeyChecking  no
        PasswordAuthentication no
        UserKnownHostsFile     /dev/null
        LogLevel               ERROR
    Host machine
        User                   core
        Hostname               ${machine_ip}
        StrictHostKeyChecking  no
        PasswordAuthentication no
        UserKnownHostsFile     /dev/null
        ProxyJump              bastion
        LogLevel               ERROR
EOF
  else
    cat << EOF > "${config_file}"
    Host machine
      User                   core
      Hostname               ${machine_ip}
      StrictHostKeyChecking  no
      PasswordAuthentication no
      UserKnownHostsFile     /dev/null
      LogLevel               ERROR
EOF
  fi
  if retryonfailure ssh -F "${config_file}" -n machine "sudo journalctl > /tmp/journal.log && gzip -f /tmp/journal.log" || dump_config "${machine_ip}" "${config_file}"; then
    retryonfailure scp -F "${config_file}" machine:/tmp/journal.log.gz "${DEST_DIR}/journal_${machine_ip}.log.gz"
  else
    failed_copy=1
  fi
}

eval "$(ssh-agent)"
if [[ -n "${SSH_PRIVATE_KEY}" ]]; then
  ssh-add "${SSH_PRIVATE_KEY}"
fi

IFS=' ' read -a IP_ARRAY <<< "${INSTANCE_IPS}"
mkdir -p "${DEST_DIR}"

for ip in "${IP_ARRAY[@]}"; do 
  copylog "${ip}"
done

if [[ "${failed_copy}" == "1" ]]; then
  exit 1
fi