---
title: Create AWS Hosted Cluster in Multiple Zones
---

# Create AWS Hosted Cluster in Multiple Zones

## Prerequisites

Complete the [Prerequisites](../../../getting-started/#prerequisites) and [Before you begin](../../../getting-started/#before-you-begin).

## Creating the Hosted Cluster

Create a new cluster, specifying the `BASE_DOMAIN` of the public zone provided in the
[Prerequisites](../../../getting-started/#prerequisites):

```shell linenums="1"  hl_lines="15"
REGION=us-east-1
ZONES=us-east-1a,us-east-1b
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
--zones $ZONES
```

!!! important

    The `--zones` flag must specify Availability Zones (AZs) within the region specified by the `--region` flag

The `--zones` flag is also available on the  `hypershift create infra aws` command used to [create infrastructure separately](../create-infra-iam-separately/#creating-the-aws-infra).

The following per-zone infrastructure is created for all specified zones:

* Public subnet
* Private subnet
* NAT gateway
* Private route table (Public route table is shared across public subnets)

Two `NodePool` resources are created, one for each zone.  The `NodePool` name is suffixed by the zone name.  The private subnet for zone is set in `spec.platform.aws.subnet.id`.
