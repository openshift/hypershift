---
title: Getting started
---

# Getting started
HyperShift is middleware for hosting OpenShift control planes at scale that
solves for cost and time to provision, as well as portability across cloud service providers with
strong separation of concerns between management and workloads. Clusters are
fully compliant OpenShift Container Platform (OCP) clusters and are compatible
with standard OCP and Kubernetes toolchains.

This guide will lead you through the process of creating a new hosted cluster.
Throughout the instructions, shell variables are used to indicate values that
you should adjust to your own environment.

## Prerequisites
1. Install the HyperShift CLI (`hypershift`) using Go 1.19:
        ```shell linenums="1"
        git clone https://github.com/openshift/hypershift.git
        cd hypershift
        make build
        sudo install -m 0755 bin/hypershift /usr/local/bin/hypershift
        ```
2. Admin access to an OpenShift cluster (version 4.12+) specified by the `KUBECONFIG` environment variable.
3. The OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`). 
4. A valid [pull secret](https://cloud.redhat.com/openshift/install/aws/installer-provisioned) file for the `quay.io/openshift-release-dev` repository. 
5. AWS credentials with [permissions](/reference/infrastructure/aws.md) to create infrastructure for the cluster. You will need:
     - An IAM role ARN with the required permissions
     - STS credentials (session token) that can be generated using `aws sts get-session-token` 
6. A Route53 public zone for cluster DNS records. To create a public zone:
        ```shell linenums="1"
        BASE_DOMAIN=www.example.com
        aws route53 create-hosted-zone --name $BASE_DOMAIN --caller-reference $(whoami)-$(date --rfc-3339=date)
        ```

    !!! important

        To access applications in your guest clusters, the public zone must be routable. If the public zone exists, skip 
        this step. Otherwise, the public zone will affect the existing functions.

7. An S3 bucket with public access to host OIDC discovery documents for your clusters. To create the bucket in *us-east-1*:
        ```shell linenums="1"
        export BUCKET_NAME=your-bucket-name
        aws s3api create-bucket --bucket $BUCKET_NAME
        aws s3api delete-public-access-block --bucket $BUCKET_NAME
        echo '{
          "Version": "2012-10-17",
          "Statement": [
            {
              "Effect": "Allow",
              "Principal": "*",
              "Action": "s3:GetObject",
              "Resource": "arn:aws:s3:::${BUCKET_NAME}/*"
            }
          ]
        }' | envsubst > policy.json
        aws s3api put-bucket-policy --bucket $BUCKET_NAME --policy file://policy.json
        ```

    To create the bucket in a region other than us-east-1:
        ```shell linenums="1"
        export BUCKET_NAME=your-bucket-name
        REGION=us-east-2
        aws s3api create-bucket --bucket $BUCKET_NAME \
          --create-bucket-configuration LocationConstraint=$REGION \
          --region $REGION
        aws s3api delete-public-access-block --bucket $BUCKET_NAME
        echo '{
          "Version": "2012-10-17",
          "Statement": [
            {
              "Effect": "Allow",
              "Principal": "*",
              "Action": "s3:GetObject",
              "Resource": "arn:aws:s3:::${BUCKET_NAME}/*"
            }
          ]
        }' | envsubst > policy.json
        aws s3api put-bucket-policy --bucket $BUCKET_NAME --policy file://policy.json
        ```

## Install HyperShift Operator
Install the HyperShift Operator into the management cluster, specifying the OIDC bucket, its region and credentials to access it (see [Prerequisites](#prerequisites)):

```shell linenums="1"
REGION=us-east-1
BUCKET_NAME=your-bucket-name
AWS_CREDS="$HOME/.aws/credentials"

hypershift install \
  --oidc-storage-provider-s3-bucket-name $BUCKET_NAME \
  --oidc-storage-provider-s3-credentials $AWS_CREDS \
  --oidc-storage-provider-s3-region $REGION \
  --enable-defaulting-webhook true
```

!!! note 

    `enable-defaulting-webhook` is only for OCP version 4.14 and higher.

## Create a Hosted Cluster
Create a new hosted cluster, specifying the domain of the public zone provided in the
[Prerequisites](#prerequisites).

### Set Up AWS Credentials

The HyperShift CLI uses AWS STS (Security Token Service) for authentication. You need:

1. An IAM role with the [required permissions](./reference/infrastructure/aws.md) to create cluster infrastructure
2. Temporary STS credentials to authenticate

Create an IAM role using the HyperShift CLI. The `--aws-creds` flag points to the credentials file for your
personal IAM user (the one used by the AWS CLI), which HyperShift uses to create the new role:

```shell linenums="1"
# Path to your existing AWS credentials file (for your personal IAM user)
AWS_CREDS="$HOME/.aws/credentials"

# Create a unique role name for your user
hypershift create iam cli-role --aws-creds $AWS_CREDS --name my-hypershift-cli-role
```

The command will output the role ARN. Save this value for use in subsequent commands:

```shell
ROLE_ARN="arn:aws:iam::123456789012:role/my-hypershift-cli-role"
```

!!! note

    The `hypershift create iam cli-role` command creates an IAM role with all required permissions.
    The role's trust policy is configured to allow only your IAM identity to assume it. If the role
    already exists, the command will reuse it and update its permissions policy.

!!! important

    Each user should create their own role with a unique `--name` to avoid conflicts.
    The role's trust policy is tied to the IAM identity that created it, so sharing a role
    between users with different IAM identities will not work.

Generate temporary STS credentials:

```shell
aws sts get-session-token --output json > sts-creds.json
```

### Create the Cluster

Create the hosted cluster using the STS credentials:

```shell linenums="1"
REGION=us-east-1
CLUSTER_NAME=example
BASE_DOMAIN=example.com
ROLE_ARN="arn:aws:iam::123456789012:role/your-hypershift-role"
STS_CREDS="/tmp/sts-creds.json"
PULL_SECRET="$HOME/pull-secret"

hypershift create cluster aws \
  --name $CLUSTER_NAME \
  --node-pool-replicas=3 \
  --base-domain $BASE_DOMAIN \
  --pull-secret $PULL_SECRET \
  --role-arn $ROLE_ARN \
  --sts-creds $STS_CREDS \
  --region $REGION \
  --generate-ssh
```

!!! note

    The `--role-arn` flag specifies the ARN of an IAM role with permissions to create cluster infrastructure.
    The `--sts-creds` flag points to a JSON file containing temporary STS credentials generated by
    `aws sts get-session-token`. These credentials are used to assume the role specified by `--role-arn`.

!!! important

    The cluster name (`--name`) _must be unique within the base domain_ to
    avoid unexpected and conflicting cluster management behavior.

    The cluster name must also adhere to the [RFC1123 standard](https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#dns-label-names).

!!! important

    You must include either flag, `release-image` or `release-stream`, when the `enable-defaulting-webhook` is not enabled on the installation of the HyperShift operator.

!!! note

    A default NodePool will be created for the cluster with 3 replicas per the
    `--node-pool-replicas` flag. 

!!! note 

    The default NodePool name will be a combination of your cluster name and zone name for 
    AWS (example, `example-us-east-1a`). For other providers, the default NodePool 
    name will be the same as the cluster name.

!!! note

    The `--generate-ssh` flag is not strictly necessary but it will help in
    debugging why a node has not joined your cluster.

After a few minutes, check the `hostedclusters` resources in the `clusters`
namespace and when ready it will look similar to the following:

```
oc get --namespace clusters hostedclusters
NAME      VERSION   KUBECONFIG                 PROGRESS    AVAILABLE   PROGRESSING   MESSAGE
example   4.12.0    example-admin-kubeconfig   Completed   True        False         The hosted control plane is available

oc get nodepools --namespace clusters
NAME                 CLUSTER   DESIRED NODES   CURRENT NODES   AUTOSCALING   AUTOREPAIR   VERSION   UPDATINGVERSION   UPDATINGCONFIG   MESSAGE
example-us-east-1a   example   2               2               False         False        4.12.0
```

Eventually the cluster's kubeconfig will become available and can be printed to
standard out using the `hypershift` CLI:

```shell
hypershift create kubeconfig
```

## Create Additional NodePools
Create additional NodePools for a cluster by specifying a name, number of replicas
and additional information such as instance type.

```shell linenums="1"
NODEPOOL_NAME=${CLUSTER_NAME}-work
INSTANCE_TYPE=m5.2xlarge
NODEPOOL_REPLICAS=2

hypershift create nodepool aws \
  --cluster-name $CLUSTER_NAME \
  --namespace clusters \
  --name $NODEPOOL_NAME \
  --replicas $NODEPOOL_REPLICAS \
  --instance-type $INSTANCE_TYPE
```

!!! important

    The default infrastructure created for the cluster during [Create a HostedCluster](#create-a-hostedcluster)
    lives in a single availability zone. Any additional NodePool created for that
    cluster must be in the same availability zone and subnet.

Check the status of the NodePool by listing `nodepool` resources in the `clusters`
namespace:

```shell
oc get nodepools --namespace clusters
```

## Scale a NodePool
Manually scale a NodePool using the `oc scale` command:

```shell linenums="1"
NODEPOOL_NAME=${CLUSTER_NAME}-work
NODEPOOL_REPLICAS=5

oc scale nodepool/$NODEPOOL_NAME \
  --namespace clusters \
  --replicas=$NODEPOOL_REPLICAS
```

!!! note

    See the [Scale Down](./how-to/automated-machine-management/nodepool-lifecycle.md#scale-down) section of the [NodePool lifecycle page](./how-to/automated-machine-management/nodepool-lifecycle.md) for more details when scaling down NodePools.

## Delete a Hosted Cluster
To delete a Hosted Cluster:

```shell
hypershift destroy cluster aws \
  --name $CLUSTER_NAME \
  --role-arn $ROLE_ARN \
  --sts-creds $STS_CREDS
```

!!! note

    The same `--role-arn` and `--sts-creds` flags used during cluster creation should be provided
    when destroying the cluster. If your STS credentials have expired, generate new ones using
    `aws sts get-session-token --output json > /tmp/sts-creds.json`.
