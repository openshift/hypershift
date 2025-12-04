#!/bin/bash
# Example: Create AWS cluster with inline etcd sharding configuration
#
# This script demonstrates using the --etcd-shard flag to configure
# etcd sharding directly from the command line without a config file.
# Suitable for simple 2-3 shard configurations.

set -e

# Cluster parameters
CLUSTER_NAME="my-sharded-cluster"
PULL_SECRET="/path/to/pull-secret.json"
BASE_DOMAIN="example.com"
AWS_REGION="us-east-1"
ROLE_ARN="arn:aws:iam::123456789012:role/hypershift-role"
STS_CREDS="/path/to/sts-creds.json"

echo "Creating cluster ${CLUSTER_NAME} with etcd sharding..."

# Create cluster with 3-shard inline configuration
hypershift create cluster aws \
  --name "${CLUSTER_NAME}" \
  --pull-secret "${PULL_SECRET}" \
  --base-domain "${BASE_DOMAIN}" \
  --region "${AWS_REGION}" \
  --role-arn "${ROLE_ARN}" \
  --sts-creds "${STS_CREDS}" \
  --etcd-shard name=main,prefixes=/,priority=Critical,replicas=3,storage-size=16Gi,backup-schedule="*/30 * * * *" \
  --etcd-shard name=events,prefixes=/events#,priority=Low,replicas=1,storage-size=8Gi \
  --etcd-shard name=leases,prefixes=/coordination.k8s.io/leases#,priority=Low,replicas=1,storage-size=4Gi

echo "✓ Cluster ${CLUSTER_NAME} creation initiated with etcd sharding"
echo ""
echo "Sharding configuration:"
echo "  - main:   Critical priority, 3 replicas, 16Gi, backup every 30min"
echo "  - events: Low priority, 1 replica, 8Gi, no backup"
echo "  - leases: Low priority, 1 replica, 4Gi, no backup"
