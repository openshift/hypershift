---
name: Create HC AWS
description: "Create a HyperShift HostedCluster on AWS for development and testing, with optional custom CPO/HO images."
---

# Create HostedCluster

This skill creates a HyperShift HostedCluster on AWS for development and testing purposes. The clusters created are intended for local development workflows, not for production use.

## When to Use This Skill

Use this skill when:
- You need to create a dev/test HostedCluster for manual verification
- You want to test HyperShift features against a live cluster
- You need a HostedCluster with custom CPO or HO images
- You are iterating on code changes and need a cluster to validate them

## Prerequisites

Source the environment file before using this skill:

```bash
source dev/claude-env.sh
```

Additional requirements:
- AWS credentials loaded (source `$AWS_CREDS_SOURCE`)
- KUBECONFIG pointing to management cluster (`$MGMT_KUBECONFIG`)
- hypershift binary built (`./bin/hypershift` or run `make hypershift`)
- Pull secret available (`$PULL_SECRET`)

## Environment Configuration

Environment variables from `dev/claude-env.sh`:

| Variable | Description |
|----------|-------------|
| `AWS_CREDENTIALS` | Path to AWS credentials file |
| `AWS_CREDS_SOURCE` | Script to source AWS env vars |
| `BASE_DOMAIN` | Base DNS domain for clusters |
| `PULL_SECRET` | Path to pull secret file |
| `AWS_REGION` | AWS region |
| `MGMT_KUBECONFIG` | Path to management cluster kubeconfig |
| `CPO_IMAGE_REPO` | Custom CPO image repository |

## Basic Command

```bash
source $AWS_CREDS_SOURCE && \
KUBECONFIG=$MGMT_KUBECONFIG \
./bin/hypershift create cluster aws \
  --name <CLUSTER_NAME> \
  --namespace clusters \
  --base-domain $BASE_DOMAIN \
  --aws-creds $AWS_CREDENTIALS \
  --pull-secret $PULL_SECRET \
  --region $AWS_REGION \
  --release-image quay.io/openshift-release-dev/ocp-release:4.21.0-multi \
  --node-pool-replicas 2
```

## Common Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `--name` | Name of the HostedCluster | Required |
| `--namespace` | Namespace for the HostedCluster | `clusters` |
| `--base-domain` | Base DNS domain | `$BASE_DOMAIN` |
| `--aws-creds` | Path to AWS credentials file | `$AWS_CREDENTIALS` |
| `--pull-secret` | Path to pull secret file | `$PULL_SECRET` |
| `--region` | AWS region | `$AWS_REGION` |
| `--release-image` | OCP release image | Latest 4.21.0 multi-arch |
| `--node-pool-replicas` | Initial node count | `0` (add nodes later) |
| `--control-plane-operator-image` | Custom CPO image | Optional |

## With Custom CPO Image

When testing CPO changes, add the custom image:

```bash
source $AWS_CREDS_SOURCE && \
KUBECONFIG=$MGMT_KUBECONFIG \
./bin/hypershift create cluster aws \
  --name my-test-cluster \
  --namespace clusters \
  --base-domain $BASE_DOMAIN \
  --aws-creds $AWS_CREDENTIALS \
  --pull-secret $PULL_SECRET \
  --region $AWS_REGION \
  --release-image quay.io/openshift-release-dev/ocp-release:4.21.0-multi \
  --node-pool-replicas 2 \
  --control-plane-operator-image $CPO_IMAGE_REPO:YOUR_TAG
```

## What Gets Created

The command creates:
- AWS VPC with public and private subnets
- NAT gateway and internet gateway
- Route tables
- Private hosted zones (Route53)
- OIDC provider for STS
- IAM roles for control plane components
- Worker instance profile
- HostedCluster and NodePool resources

## Post-Creation Steps

1. **Check HostedCluster status:**
   ```bash
   KUBECONFIG=$MGMT_KUBECONFIG kubectl get hostedcluster -n clusters
   ```

2. **Wait for control plane to be available:**
   ```bash
   KUBECONFIG=$MGMT_KUBECONFIG kubectl wait --for=condition=Available \
     hostedcluster/<CLUSTER_NAME> -n clusters --timeout=10m
   ```

3. **Scale NodePool to add nodes:**
   ```bash
   KUBECONFIG=$MGMT_KUBECONFIG kubectl scale nodepool <NODEPOOL_NAME> \
     -n clusters --replicas=1
   ```

4. **Get guest cluster kubeconfig:**
   ```bash
   KUBECONFIG=$MGMT_KUBECONFIG kubectl get secret <CLUSTER_NAME>-admin-kubeconfig \
     -n clusters -o jsonpath='{.data.kubeconfig}' | base64 -d > /tmp/guest-kubeconfig.yaml
   ```

## Cleanup

Use the `dev:destroy-hc-aws` skill or run:

```bash
source $AWS_CREDS_SOURCE && \
KUBECONFIG=$MGMT_KUBECONFIG \
./bin/hypershift destroy cluster aws \
  --name <CLUSTER_NAME> \
  --namespace clusters \
  --aws-creds $AWS_CREDENTIALS \
  --region $AWS_REGION
```

## Troubleshooting

### Cluster Creation Fails
- Check AWS credentials are valid
- Verify base domain exists in Route53
- Ensure OIDC S3 bucket is accessible

### Control Plane Not Available
- Check HCP pods: `kubectl get pods -n clusters-<CLUSTER_NAME>`
- Check HCP conditions: `kubectl get hcp -n clusters-<CLUSTER_NAME> -o yaml`

### Nodes Not Joining
- Check machines: `kubectl get machines -n clusters-<CLUSTER_NAME>`
- Check NodePool conditions: `kubectl get nodepool -n clusters -o yaml`
