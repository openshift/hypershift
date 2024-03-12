---
title: Create Heterogeneous NodePools on AWS HostedClusters
---

# Create Heterogeneous NodePools on AWS HostedClusters
## Create Heterogeneous NodePools Through the HyperShift CLI
The `multi-arch` flag was added to the HyperShift CLI in OCP 4.16. The `multi-arch` flag indicates an expectation that the Hosted Cluster will support both amd64 and arm64 NodePools.

When this flag is set, it will ensure:

* if a release image is supplied, the release image must be a multi-arch release image
* if a release stream is supplied, the release stream must be a multi-arch stream


!!! note 

    An individual NodePool only supports one CPU architecture and cannot support multiple CPU archiectures within the same NodePool.


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
