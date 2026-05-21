---
title: Spot Instances
---

# Spot Instances

AWS Spot instances use spare EC2 capacity at significantly reduced prices compared to on-demand instances, but may be interrupted with a 2-minute warning when EC2 needs the capacity back. HyperShift supports Spot instances for NodePools on AWS, with built-in graceful termination handling via SQS queues.

!!! important

    Spot instances are suitable for fault-tolerant, stateless, and flexible workloads. They are **not recommended** for workloads that cannot tolerate interruptions.

## Prerequisites

Before creating a Spot instance NodePool, you must set up an SQS queue and EventBridge rules to receive EC2 interruption events. The AWS Node Termination Handler (NTH) deployed by HyperShift polls this queue and cordons/drains nodes before they are terminated, providing best-effort graceful shutdown.

### 1. Create the SQS queue

Create an SQS queue to receive Spot interruption notifications:

```shell
export CLUSTER_NAME="my-cluster"
export AWS_REGION="us-east-1"

aws sqs create-queue \
  --queue-name "${CLUSTER_NAME}-spot-interruption-queue" \
  --region "${AWS_REGION}"
```

Note the queue URL from the output — you will need it when creating the HostedCluster.

### 2. Create EventBridge rules

Create EventBridge rules to route EC2 Spot interruption warnings and rebalance recommendations to the SQS queue:

```shell
QUEUE_ARN=$(aws sqs get-queue-attributes \
  --queue-url "https://sqs.${AWS_REGION}.amazonaws.com/$(aws sts get-caller-identity --query Account --output text)/${CLUSTER_NAME}-spot-interruption-queue" \
  --attribute-names QueueArn \
  --query 'Attributes.QueueArn' --output text)

aws events put-rule \
  --name "${CLUSTER_NAME}-spot-interruption-warning" \
  --event-pattern '{"source":["aws.ec2"],"detail-type":["EC2 Spot Instance Interruption Warning"]}' \
  --region "${AWS_REGION}"

aws events put-targets \
  --rule "${CLUSTER_NAME}-spot-interruption-warning" \
  --targets "Id=1,Arn=${QUEUE_ARN}" \
  --region "${AWS_REGION}"

aws events put-rule \
  --name "${CLUSTER_NAME}-rebalance-recommendation" \
  --event-pattern '{"source":["aws.ec2"],"detail-type":["EC2 Instance Rebalance Recommendation"]}' \
  --region "${AWS_REGION}"

aws events put-targets \
  --rule "${CLUSTER_NAME}-rebalance-recommendation" \
  --targets "Id=1,Arn=${QUEUE_ARN}" \
  --region "${AWS_REGION}"
```

### 3. Configure SQS queue policy

Allow EventBridge to send messages to the queue:

```shell
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
QUEUE_URL="https://sqs.${AWS_REGION}.amazonaws.com/${ACCOUNT_ID}/${CLUSTER_NAME}-spot-interruption-queue"

aws sqs set-queue-attributes \
  --queue-url "${QUEUE_URL}" \
  --attributes '{
    "Policy": "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Principal\":{\"Service\":\"events.amazonaws.com\"},\"Action\":\"sqs:SendMessage\",\"Resource\":\"'${QUEUE_ARN}'\"}]}"
  }'
```

## Creating a Spot Instance NodePool

### Step 1: Set the SQS queue URL on the HostedCluster

The SQS queue URL is configured at the HostedCluster level in `spec.platform.aws.terminationHandlerQueueURL`. This enables the AWS Node Termination Handler component for all Spot NodePools in the cluster:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: my-cluster
  namespace: clusters
spec:
  platform:
    type: AWS
    aws:
      region: us-east-1
      terminationHandlerQueueURL: "https://sqs.us-east-1.amazonaws.com/123456789012/my-cluster-spot-interruption-queue"
      # ... other AWS configuration
  # ... other spec fields
```

If you already have a running HostedCluster, you can patch it:

```shell
oc patch hostedcluster my-cluster -n clusters --type merge -p '{
  "spec": {
    "platform": {
      "aws": {
        "terminationHandlerQueueURL": "https://sqs.us-east-1.amazonaws.com/123456789012/my-cluster-spot-interruption-queue"
      }
    }
  }
}'
```

### Step 2: Create a NodePool with Spot market type

Create a NodePool with `spec.platform.aws.placement.marketType` set to `Spot`:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: spot-workers
  namespace: clusters
spec:
  clusterName: my-cluster
  replicas: 3
  release:
    image: quay.io/openshift-release-dev/ocp-release:4.18.0-x86_64
  management:
    autoRepair: true
    upgradeType: Replace
  platform:
    type: AWS
    aws:
      instanceType: m5.xlarge
      instanceProfile: my-cluster-worker
      rootVolume:
        size: 120
        type: gp3
      placement:
        marketType: Spot
```

### Setting a maximum price (optional)

You can optionally set a maximum hourly price for Spot instances. When omitted, you pay the current Spot price (capped at the on-demand price). AWS recommends **not** setting a maximum price to reduce interruption frequency:

```yaml
spec:
  platform:
    aws:
      placement:
        marketType: Spot
        spot:
          maxPrice: "0.50"
```

The value is a decimal string representing the price per hour in USD.

## Behavior

When a NodePool is created with `marketType: Spot`, HyperShift labels all Machines and Nodes with `hypershift.openshift.io/interruptible-instance` and tags the EC2 instances with `aws-node-termination-handler/managed` so they can be identified by the termination handling components.

### Graceful termination with SQS (recommended)

When `terminationHandlerQueueURL` is set on the HostedCluster and at least one NodePool has `marketType: Spot`, HyperShift automatically deploys the Node Termination Handler (NTH) as a control plane component. The termination flow is:

1. AWS sends a Spot interruption warning (2-minute notice) or rebalance recommendation to the SQS queue via EventBridge
2. NTH polls the queue and identifies the affected node
3. NTH cordons the node and drains it, respecting PodDisruptionBudgets
4. NTH taints the node (e.g., `aws-node-termination-handler/spot-itn`)
5. The spot remediation controller detects the taint, annotates the corresponding Machine with `hypershift.openshift.io/spot-interruption-signal`, and deletes it
6. The machine controller provisions a replacement Spot instance

### Fallback without SQS

Without the SQS queue, AWS terminates the instance abruptly with no graceful drain. In this case, the CAPI Machine enters a `Failed` state and the MachineHealthCheck triggers remediation to create a replacement. This path is slower and does not provide graceful pod shutdown.

### MachineHealthCheck

HyperShift creates a dedicated MachineHealthCheck (`<nodepool-name>-spot`) for each Spot NodePool. This MachineHealthCheck:

- Targets only Machines with the `hypershift.openshift.io/interruptible-instance` label
- Sets `maxUnhealthy: 100%` because Spot reclamation can affect all instances simultaneously
- Uses an 8-minute unhealthy timeout (accounts for the 2-minute AWS notice plus shutdown time)
- Serves as a safety net in case the NTH + remediation controller path does not trigger

### Constraints

- Spot instances **cannot** be combined with Capacity Reservations
- Spot instances require **default** tenancy (dedicated tenancy is not supported)
- `spot` options (e.g., `maxPrice`) can only be specified when `marketType` is `Spot`
- Replacement instances are always Spot — if Spot capacity is unavailable, the replacement Machine will go `Failed` and the MachineHealthCheck will continue to retry remediation
