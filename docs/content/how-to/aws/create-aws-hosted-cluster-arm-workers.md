---
title: Create ARM NodePools on AWS Hosted Cluster
---

# Create ARM NodePools on AWS Hosted Cluster

## Creating a Hosted Cluster with ARM NodePool

Create a new cluster, specifying the `RELEASE_IMAGE` and `ARCH`:

```shell linenums="1"
REGION=us-east-1
CLUSTER_NAME=example
BASE_DOMAIN=example.com
AWS_CREDS="$HOME/.aws/credentials"
PULL_SECRET="$HOME/pull-secret"
RELEASE_IMAGE="quay.io/openshift-release-dev/ocp-release@sha256:1a101ef5215da468cea8bd2eb47114e85b2b64a6b230d5882f845701f55d057f"
ARCH="arm64"

hypershift create cluster aws \
--name $CLUSTER_NAME \
--node-pool-replicas=3 \
--base-domain $BASE_DOMAIN \
--pull-secret $PULL_SECRET \
--aws-creds $AWS_CREDS \
--region $REGION \
--release-image $RELEASE_IMAGE \
--arch $ARCH \
--generate-ssh
```

!!! important

    The release image used needs to be a manifest listed image.

!!! note

    Currently, the only valid values for '--arch' are 'arm64' and 'amd64'. 'amd64' is the default when '--arch' isn't used.

The hosted cluster will spin up with an ARM NodePool. The default AWS ARM instance type is `m6g.large`.

## Creating ARM NodePools on Existing Hosted Cluster

As long as a hosted cluster was created with a manifest listed image in the `--release-image`, ARM NodePools can be added to the hosted cluster:

```shell linenums="1"
CLUSTER_NAME=example
NODE_POOLNAME=example-worker
ARCH="arm64"

hypershift create nodepool aws \
--cluster-name $CLUSTER_NAME \
--name $NODE_POOLNAME \
--node-count=3 \
--arch $ARCH \
```