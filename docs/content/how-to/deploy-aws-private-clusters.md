---
title: Deploy AWS private clusters
---

# Deploying AWS private clusters

By default, HyperShift guest clusters are publicly accessible through public DNS
and the management cluster's default router.

For private clusters on AWS, all communication with the guest cluster occur over
[AWS PrivateLink](https://aws.amazon.com/privatelink). This guide will lead you
through the process of configuring HyperShift for private cluster support on AWS.

## Before you begin

To enable private hosted clusters, HyperShift must be installed with private
cluster support. This guide assumes you have performed all the
[Getting started guide prerequisites](../getting-started.md#prerequisites). The
following steps will reference elements of the steps you already performed.

1. Create the private cluster IAM policy document.
    
    === "Shell"

        ```shell
        cat << EOF >> policy.json
        {
          "Version": "2012-10-17",
          "Statement": [
            {
              "Effect": "Allow",
              "Action": [
                "ec2:CreateVpcEndpointServiceConfiguration",
                "ec2:DescribeVpcEndpointServiceConfigurations",
                "ec2:DeleteVpcEndpointServiceConfigurations",
                "elasticloadbalancing:DescribeLoadBalancers"
              ],
              "Resource": "*"
            }
          ]
        }
        EOF
        ```

    === "JSON"

        ```json
        {
          "Version": "2012-10-17",
          "Statement": [
            {
              "Effect": "Allow",
              "Action": [
                "ec2:CreateVpcEndpointServiceConfiguration",
                "ec2:DescribeVpcEndpointServiceConfigurations",
                "ec2:DeleteVpcEndpointServiceConfigurations",
                "elasticloadbalancing:DescribeLoadBalancers"
              ],
              "Resource": "*"
            }
          ]
        }
        ```

2. Create the IAM policy in AWS.

    ```shell
    aws iam create-policy --policy-name=hypershift-operator-policy --policy-document=file://policy.json
    ```

3. Create a `hypershift-operator` IAM user.

    ```shell
    aws iam create-user --user-name=hypershift-operator
    ```

4. Attach the policy to the `hypershift-operator` user, replacing `$POLICY_ARN` with the ARN of the policy
   created in step 2.

    ```shell
    aws iam attach-user-policy --user-name=hypershift-operator --policy-arn=$POLICY_ARN
    ```

5. Create an IAM access key for the user.

    ```shell
    aws iam create-access-key --user-name=hypershift-operator
    ```

6. Create a credentials file (`$AWS_PRIVATE_CREDS`) with the access ID and key for the user
   created in step 5.

    ```shell
    cat << EOF >> $AWS_PRIVATE_CREDS
    [default]
    aws_access_key_id = <secret>
    aws_secret_access_key = <secret>
    EOF
    ```

7. Now you can install HyperShift with private cluster support.

    ```shell linenums="1"
    REGION=us-east-1
    BUCKET_NAME=your-bucket-name
    AWS_CREDS="$HOME/.aws/credentials"

    hypershift install \
    --oidc-storage-provider-s3-bucket-name $BUCKET_NAME \
    --oidc-storage-provider-s3-credentials $AWS_CREDS \
    --oidc-storage-provider-s3-region $REGION \
    --private-platform=AWS \
    --aws-private-creds=$AWS_PRIVATE_CREDS \
    --aws-private-region=$REGION
    ```

    !!! note

        Even if you already installed HyperShift using the [Getting started guide](../getting-started.md), you
        can safely run `hypershift install` again with private cluster support to update the existing installation.

    !!! important

        Although public clusters can be created in any region, **private clusters can only be created in the same
        region specified by `--aws-private-region`**.

## Create a private HostedCluster

Create a new private cluster, specifying values used in the [Before you
begin](#before-you-begin) section.

```shell linenums="1" hl_lines="13"
CLUSTER_NAME=example
BASE_DOMAIN=example.com
AWS_CREDS="$HOME/.aws/credentials"
PULL_SECRET="$HOME/pull-secret"

hypershift create cluster aws \
--name $CLUSTER_NAME \
--node-pool-replicas=3 \
--base-domain $BASE_DOMAIN \
--pull-secret $PULL_SECRET \
--aws-creds $AWS_CREDS \
--region $REGION \
--endpoint-access Private
```

!!! note

    The `--endpoint-access` flag is used to designate whether a cluster is public or private.

The cluster's API endpoints will be accessible through a private DNS zone:

- `api.$CLUSTER_NAME.hypershift.local`
- `*.apps.$CLUSTER_NAME.hypershift.local`

## Access a private HostedCluster

Use a bastion host to access a private cluster.

Start a bastion instance, replacing `$SSH_KEY` with credentials to use for connecting to the bastion.

```shell
hypershift create bastion aws --aws-creds=$AWS_CREDS --infra-id=$INFRA_ID --region=$REGION --ssh-key-file=$SSH_KEY
```

Find the private IPs of nodes in the cluster's NodePool.

```shell
aws ec2 describe-instances --filter="Name=tag:kubernetes.io/cluster/$CLUSTER_NAME,Values=owned" | jq '.Reservations[] | .Instances[] | select(.PublicDnsName=="") | .PrivateIpAddress'
```

Create a kubeconfig for the cluster which can be copied to a node.

```shell
hypershift create kubeconfig > $CLUSTER_KUBECONFIG
```

SSH into one of the nodes via the bastion using the IP printed from the `create bastion` command.

```shell
ssh -o ProxyCommand="ssh ec2-user@$BASTION_IP -W %h:%p" core@$NODE_IP
```

From the SSH shell, copy the kubeconfig contents to a file on the node.

```shell
cat << EOF >> kubeconfig
<paste kubeconfig contents>
EOF
export KUBECONFIG=$PWD/kubeconfig
```

From the SSH shell, observe the guest cluster status or run other `oc` commands.

```shell
oc get clusteroperators
oc get clusterversion
# ...
```
