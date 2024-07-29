---
title: Multi-arch on AWS HostedClusters
---

## Create Heterogeneous NodePools on AWS HostedClusters
The `multi-arch` flag was added to the HyperShift CLI in OCP 4.16. The `multi-arch` flag indicates an expectation that 
the Hosted Cluster will support both amd64 and arm64 NodePools.

When this flag is set, it will ensure:

* if a release image is supplied, the release image must be a multi-arch release image
* if a release stream is supplied, the release stream must be a multi-arch stream


!!! note 

    An individual NodePool only supports one CPU architecture and cannot support multiple CPU archiectures within the 
    same NodePool.


!!! warning

    If a multi-arch release image or stream is used and the multi-arch flag is not set, if the management cluster and 
    NodePool CPU arches do not match, the CLI will throw a validation error and not create the Hosted Cluster.


!!! note

    If a multi-arch release image or stream is used and the multi-arch flag is not set, the CLI will automatically set 
    the multi-arch flag true and notify the user through a log message.

Here is an example command to create a HostedCluster capable of creating both amd64 and arm64 NodePools. This example 
will create a default amd64 NodePool with 3 worker nodes.

```shell linenums="1"
REGION=us-east-1
CLUSTER_NAME=example
BASE_DOMAIN=example.com
AWS_CREDS="$HOME/.aws/credentials"
PULL_SECRET="$HOME/pull-secret"
RELEASE_IMAGE=quay.io/openshift-release-dev/ocp-release:4.16.0-ec.3-multi

hypershift create cluster aws \
  --name $CLUSTER_NAME \
  --node-pool-replicas=3 \
  --base-domain $BASE_DOMAIN \
  --pull-secret $PULL_SECRET \
  --aws-creds $AWS_CREDS \
  --region $REGION \
  --release-image $RELEASE_IMAGE \
  --generate-ssh \
  --multi-arch
```

Once the HCP is available, you can add an arm64 NodePool through the CLI as well.

```shell linenums="1"
hypershift create nodepool aws \
--cluster-name $CLUSTER_NAME \
--name $ARM64_NODEPOOL_NAME \
--node-count=$NODEPOOL_REPLICAS \
--arch arm64
```

## Migrating a HostedCluster from a Single Arch Payload Release Image to a Multi-arch Payload Release
Migrating a HostedCluster from a single arch payload release image to a multi-arch payload release image is as simple as
a normal release image upgrade for a HostedCluster. Follow the steps for upgrading your HostedCluster,
[outlined here](../../how-to/upgrades.md#hostedcluster), and set the `hostedCluster.spec.platform.aws.multiarch` flag to
true.

!!! note

    It is recommended to upgrade to a multi-arch payload release image that is the same version of your single arch 
    payload release image. For example, upgrading from `quay.io/openshift-release-dev/ocp-release:4.16.5-x86_64` to 
    `quay.io/openshift-release-dev/ocp-release:4.16.5-multi` before upgrading to any OCP version past 4.16.5.

After the upgrade to the multi-arch payload release image is complete, you will be able to create Arm NodePools from the
HostedCluster.
