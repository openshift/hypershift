---
title: Manage AWS Spot instances in NodePools
---

AWS Spot instances allow you to run node pools using spare EC2 capacity at significantly reduced costs compared to On-Demand pricing. HyperShift supports configuring node pools to use AWS Spot instances through the `spotMarketOptions` field in the node pool specification.

This guide demonstrates how to configure and manage NodePools with AWS Spot instances in an existing hosted cluster.

## Prerequisites

- An existing HyperShift hosted cluster on AWS
- Access to the management cluster where the NodePool resources are created
- Ensure that the AWS service-linked role for Spot is enabled in the account where the hosted cluster is installed. This is a one-time setup per account.
    - You can verify if the role already exists using the following command:
  ```sh
  aws iam get-role --role-name AWSServiceRoleForEC2Spot
  ```
    - If the role does not exist, create it with:
  ```sh
  aws iam create-service-linked-role --aws-service-name spot.amazonaws.com
  ```

## Configure NodePool with Spot instances

### Option 1: Create a new NodePool with Spot instances

Create a new NodePool configured to use AWS Spot instances:

```sh
cat << EOF | oc apply -f -
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: spot-nodepool
  namespace: clusters
spec:
  clusterName: <your-cluster-name>
  replicas: 2
  management:
    autoRepair: true
    upgradeType: Replace
  platform:
    aws:
      instanceType: m5.large
      subnet:
        id: subnet-xxxxxxxxx  # Replace with your subnet ID
      placement:
        spotMarketOptions:
          maxPrice: "0.10"  # Maximum price per hour in USD (optional)
  release:
    image: <your-release-image>
EOF
```

### Option 2: Update existing NodePool to use Spot instances

You can also modify an existing NodePool to use Spot instances:

```sh
oc patch nodepool <nodepool-name> -n clusters --type='merge' -p='
{
  "spec": {
    "platform": {
      "aws": {
        "placement": {
          "spotMarketOptions": {
            "maxPrice": "0.10"
          }
        }
      }
    }
  }
}'
```

### Option 3: Remove Spot configuration from NodePool

To convert a Spot instance NodePool back to On-Demand instances:

```sh
oc patch nodepool <nodepool-name> -n clusters --type='json' -p='
[
  {
    "op": "remove",
    "path": "/spec/platform/aws/placement/spotMarketOptions"
  }
]'
```

## Spot instance configuration options

The `spotMarketOptions` field supports the following configuration:

- **maxPrice** (optional): The maximum price per hour that you're willing to pay for a Spot instance, specified in USD. If omitted, the On-Demand price is used as the maximum price.

### Compatibility constraints

Spot instances have the following compatibility requirements:

- **Not compatible** with `spec.platform.aws.placement.capacityReservation` - Spot instances cannot use capacity reservations
- **Requires** `spec.platform.aws.placement.tenancy` to be `default` - Spot instances are not supported with `dedicated` or `host` tenancy

These constraints are enforced through validation rules. Attempting to create a NodePool with incompatible configurations will result in a validation error.

## Understanding Spot instance behavior

### Cost savings

AWS Spot instances can provide significant cost savings, often 50-90% less than On-Demand pricing, depending on the instance type and availability.

### Availability and interruptions

- Spot instances are subject to interruption when AWS needs the capacity back for On-Demand customers
- Instances receive a 2-minute warning before termination
- HyperShift automatically handles instance replacement when Spot instances are terminated through the NodePool's `autoRepair` functionality

### Automatic instance replacement

When a Spot instance is terminated:

1. The Kubernetes node becomes `NotReady`
2. The NodePool controller detects the failed node
3. If `autoRepair: true` is set, a replacement instance is automatically requested
4. Pods are automatically rescheduled to available nodes

## Best practices

### 1. Set appropriate max price

Consider setting a maximum price to control costs, but be aware that setting it too low may result in frequent interruptions:

```yaml
spotMarketOptions:
  maxPrice: "0.15"  # Set based on your cost tolerance
```

### 2. Use diverse instance types

Consider using multiple NodePools with different instance types to increase availability and reduce the likelihood of simultaneous interruptions across all nodes.

### 3. Design for fault tolerance

Ensure your applications can tolerate node interruptions:

- Use appropriate pod disruption budgets
- Configure adequate replica counts for critical workloads
- Implement proper readiness and liveness probes
- Design stateless applications when possible

### 4. Enable auto-repair

Always enable `autoRepair` in your NodePool management configuration to ensure automatic replacement of interrupted instances:

```yaml
management:
  autoRepair: true
  upgradeType: Replace
```

### 5. Monitor costs and interruptions

- Regularly monitor your Spot instance usage and costs through the AWS console
- Set up CloudWatch alarms for Spot instance interruptions
- Track NodePool events to understand interruption patterns

## Verification

Verify that your nodes are running as Spot instances:

```sh
# Check the NodePool status
oc get nodepool -n clusters

# Check the nodes in the hosted cluster
oc get nodes -o wide

# Verify instance types and Spot status in AWS console or CLI
aws ec2 describe-instances --region <your-region> --filters "Name=instance-lifecycle,Values=spot" --query 'Reservations[].Instances[].{InstanceId:InstanceId,InstanceType:InstanceType,SpotInstanceRequestId:SpotInstanceRequestId,State:State.Name}'
```

## Troubleshooting

### Spot instance unavailability

If Spot instances are frequently unavailable:

1. Consider increasing your maximum price
2. Try different instance types or sizes
3. Use multiple availability zones
4. Check AWS Spot instance pricing history and availability

### Node replacement issues

If nodes are not being replaced after Spot interruptions:

1. Check the NodePool status: `oc describe nodepool <nodepool-name> -n clusters`
2. Verify that `autoRepair` is enabled in the NodePool management configuration
3. Check the HyperShift operator logs for any errors
4. Ensure the AWS service-linked role for Spot exists in your account

### Validation errors

If you encounter validation errors when creating Spot NodePools:

1. Ensure you're not using dedicated or host tenancy with Spot instances
2. Remove any capacity reservation configuration when using Spot instances
3. Check that your `maxPrice` format is valid (numeric string in USD)

### Monitoring Spot interruptions

Monitor Spot instance interruption events:

```sh
# Check NodePool events
oc describe nodepool <nodepool-name> -n clusters

# Check for node replacement events
oc get events -n clusters --field-selector involvedObject.kind=NodePool
```

For more troubleshooting information, see the [general troubleshooting guide](../troubleshooting-general.md).