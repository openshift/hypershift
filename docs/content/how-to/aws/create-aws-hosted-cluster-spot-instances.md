---
title: Create hosted clusters with AWS Spot instances
---

AWS Spot instances allow you to run node pools using spare EC2 capacity at significantly reduced costs compared to On-Demand pricing. HyperShift supports configuring node pools to use AWS Spot instances through the `spotMarketOptions` field in the node pool specification.

This guide demonstrates how to create a hosted cluster with node pools configured to use AWS Spot instances.

## Prerequisites

- [Install the latest HyperShift CLI](https://hypershift-docs.netlify.app/getting-started/#prerequisites).
    - Make sure all prerequisites have been satisfied (Pull Secret, Hosted Zone, OIDC Bucket, etc.)
- Ensure that the AWS service-linked role for Spot is enabled in the account where the hosted cluster will be installed. This is a one-time setup per account.
    - You can verify if the role already exists using the following command:
```sh
aws iam get-role --role-name AWSServiceRoleForEC2Spot
```
    - If the role does not exist, create it with:
```sh
aws iam create-service-linked-role --aws-service-name spot.amazonaws.com
```
- Export environment variables, adjusting according to your setup:
```sh
# AWS config
export AWS_CREDS="$HOME/.aws/credentials"
export AWS_REGION=us-east-1

# OpenShift credentials and configuration
export CLUSTER_PREFIX=hcp-aws
export CLUSTER_BASE_DOMAIN=devcluster.openshift.com
export PULL_SECRET_FILE="${HOME}/.openshift/pull-secret-latest.json"
export SSH_PUB_KEY_FILE=$HOME/.ssh/id_rsa.pub

## S3 Bucket name hosting the OIDC discovery documents
# You must have set it up, see Getting Started for more information:
# https://hypershift-docs.netlify.app/getting-started/
export OIDC_BUCKET_NAME="${CLUSTER_PREFIX}-oidc"
```

## Create hosted cluster

Create the hosted cluster with HyperShift:

Choose the desired target release image name ([release controller](https://openshift-release.apps.ci.l2s4.p1.openshiftapps.com/)).

```sh
HOSTED_CLUSTER_NAME=${CLUSTER_PREFIX}-wl
OCP_RELEASE_IMAGE=<CHANGE_ME_TO_LATEST_RELEASE_IMAGE>
# Example of image: quay.io/openshift-release-dev/ocp-release:4.19.0-rc.5-x86_64

./hypershift create cluster aws \
  --name="${HOSTED_CLUSTER_NAME}" \
  --region="${AWS_REGION}" \
  --zones="${AWS_REGION}a" \
  --node-pool-replicas=1 \
  --base-domain="${CLUSTER_BASE_DOMAIN}" \
  --pull-secret="${PULL_SECRET_FILE}" \
  --aws-creds="${AWS_CREDS}" \
  --ssh-key="${SSH_PUB_KEY_FILE}" \
  --release-image="${OCP_RELEASE_IMAGE}"
```

Check the cluster information:

```sh
oc get --namespace clusters hostedclusters
oc get --namespace clusters nodepools
```

When completed, extract the credentials for workload cluster:

```sh
./hypershift create kubeconfig --name ${HOSTED_CLUSTER_NAME} > kubeconfig-${HOSTED_CLUSTER_NAME}

# kubeconfig for workload cluster
export KUBECONFIG=$PWD/kubeconfig-${HOSTED_CLUSTER_NAME}
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
  clusterName: ${HOSTED_CLUSTER_NAME}
  replicas: 2
  management:
    autoRepair: true
    upgradeType: Replace
  platform:
    aws:
      instanceType: m5.large
      subnet:
        id: subnet-xxxxxxxxx  # Replace with your subnet ID
      spotMarketOptions:
        maxPrice: "0.10"  # Maximum price per hour in USD (optional)
  release:
    image: ${OCP_RELEASE_IMAGE}
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
        "spotMarketOptions": {
          "maxPrice": "0.10"
        }
      }
    }
  }
}'
```

## Spot instance configuration options

The `spotMarketOptions` field supports the following configuration:

- **maxPrice** (optional): The maximum price per hour that you're willing to pay for a Spot instance, specified in USD. If omitted, the On-Demand price is used as the maximum price.
- Not compatible with `spec.platform.aws.placement.capacityReservation`.
- Requires `spec.platform.aws.placement.tenancy` to be `default` (Spot is not supported with `dedicated` or `host`).

## Understanding Spot instance behavior

### Cost savings

AWS Spot instances can provide significant cost savings, often 50-90% less than On-Demand pricing, depending on the instance type and availability.

### Availability and interruptions

- Spot instances are subject to interruption when AWS needs the capacity back for On-Demand customers
- Instances receive a 2-minute warning before termination
- HyperShift automatically handles instance replacement when Spot instances are terminated

### Best practices

1. **Set appropriate max price**: Consider setting a maximum price to control costs, but be aware that setting it too low may result in frequent interruptions.

2. **Use diverse instance types**: Consider using multiple instance types in your node pools to increase availability:

```sh
cat << EOF | oc apply -f -
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: diverse-spot-nodepool
  namespace: clusters
spec:
  clusterName: ${HOSTED_CLUSTER_NAME}
  replicas: 3
  management:
    autoRepair: true
    upgradeType: Replace
  platform:
    aws:
      instanceType: m5.large  # You can also specify alternate instance types in CAPI templates
      subnet:
        id: subnet-xxxxxxxxx
      spotMarketOptions:
        maxPrice: "0.15"
  release:
    image: ${OCP_RELEASE_IMAGE}
EOF
```

3. **Design for fault tolerance**: Ensure your applications can tolerate node interruptions by using appropriate pod disruption budgets and replica counts.

4. **Monitor costs**: Regularly monitor your Spot instance usage and costs through the AWS console.

## Verification

Verify that your nodes are running as Spot instances:

```sh
# Check the NodePool status
oc get nodepool -n clusters

# Check the nodes in the hosted cluster
oc get nodes -o wide

# Verify instance types and Spot status in AWS console or CLI
aws ec2 describe-instances --region ${AWS_REGION} --filters "Name=instance-lifecycle,Values=spot" --query 'Reservations[].Instances[].{InstanceId:InstanceId,InstanceType:InstanceType,SpotInstanceRequestId:SpotInstanceRequestId,State:State.Name}'
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

For more troubleshooting information, see the [troubleshooting guide](../troubleshooting/).