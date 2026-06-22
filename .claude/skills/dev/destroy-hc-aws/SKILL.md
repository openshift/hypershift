---
name: Destroy HC AWS
description: "Destroy a HyperShift HostedCluster and all associated AWS infrastructure (VPC, IAM, Route53, etc.)."
---

# Destroy HostedCluster

This skill destroys a HyperShift HostedCluster and all associated AWS infrastructure.

## When to Use This Skill

Use this skill when:
- You need to clean up a test HostedCluster
- You want to destroy a HostedCluster and its AWS resources (VPC, subnets, NAT gateways, IAM roles, etc.)
- You need to remove orphaned HostedClusters from previous test runs

## Prerequisites

Source the environment file before using this skill:

```bash
source dev/claude-env.sh
```

Additional requirements:
- AWS credentials loaded (source `$AWS_CREDS_SOURCE`)
- KUBECONFIG pointing to management cluster (`$MGMT_KUBECONFIG`)
- hypershift binary built (`./bin/hypershift`)

## Environment Configuration

Environment variables from `dev/claude-env.sh`:

| Variable | Description |
|----------|-------------|
| `AWS_CREDENTIALS` | Path to AWS credentials file |
| `AWS_CREDS_SOURCE` | Script to source AWS env vars |
| `AWS_REGION` | AWS region |
| `MGMT_KUBECONFIG` | Path to management cluster kubeconfig |

## Command

```bash
source $AWS_CREDS_SOURCE && \
KUBECONFIG=$MGMT_KUBECONFIG \
./bin/hypershift destroy cluster aws \
  --name <CLUSTER_NAME> \
  --namespace <NAMESPACE> \
  --aws-creds $AWS_CREDENTIALS \
  --region $AWS_REGION
```

## Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `--name` | Name of the HostedCluster to destroy | Required |
| `--namespace` | Namespace where the HostedCluster exists | `clusters` |
| `--aws-creds` | Path to AWS credentials file | `$AWS_CREDENTIALS` |
| `--region` | AWS region | `$AWS_REGION` |

## What Gets Destroyed

The command destroys:
- HostedCluster and NodePool resources
- HostedControlPlane namespace and all resources
- AWS VPC and subnets
- NAT gateways and internet gateways
- Route tables
- Security groups
- DHCP options
- Private hosted zones (Route53)
- OIDC provider
- IAM roles and instance profiles
- VPC endpoints
- Elastic IPs

## Quick Cleanup for Orphaned HCs

If a HostedCluster is stuck deleting, you can force remove it:

```bash
# Remove cleanup annotation to skip cloud resource cleanup
KUBECONFIG=$MGMT_KUBECONFIG kubectl annotate hostedcluster <NAME> -n <NAMESPACE> \
  hypershift.openshift.io/cleanup-cloud-resources-
```

## Example Usage

```bash
# List existing HostedClusters
KUBECONFIG=$MGMT_KUBECONFIG kubectl get hostedclusters -A

# Destroy a specific HostedCluster
source $AWS_CREDS_SOURCE && \
KUBECONFIG=$MGMT_KUBECONFIG \
./bin/hypershift destroy cluster aws \
  --name my-test-cluster \
  --namespace clusters \
  --aws-creds $AWS_CREDENTIALS \
  --region $AWS_REGION
```

## Troubleshooting

### NAT Gateway Still Deleting
NAT gateways take time to delete. The command will retry automatically.

### VPC Has Dependencies
The command will retry until all dependencies (subnets, gateways, etc.) are deleted.

### HostedCluster Stuck in Deleting
1. Check if there are stuck finalizers
2. Remove the cleanup-cloud-resources annotation if you want to skip AWS cleanup
3. Manually delete AWS resources if needed
