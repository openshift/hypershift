---
title: Create Arm NodePools on AWS HostedClusters
---

# Create Arm NodePools on AWS HostedClusters

The `arch` field was added to the NodePool Spec in OCP 4.14. The `arch` field sets the required processor architecture for the NodePool (currently only supported on AWS).

!!! note 

    Currently, the only valid values for '--arch' are 'arm64' and 'amd64'. The HyperShift CLI will default to 'amd64' when the 'arch' field is not specified by the user.

## Creating Arm NodePools Through the API
The HostedCluster custom resource (CR) must utilize a multi-arch manifested image. Multi-arch nightly images can be found at https://multi.ocp.releases.ci.openshift.org/.

Here is an example of an OCP 4.14 multi-arch nightly image:
```
% oc image info quay.io/openshift-release-dev/ocp-release-nightly@sha256:9b992c71f77501678c091e3dc77c7be066816562efe3d352be18128b8e8fce94 -a ~/pull-secrets.json

error: the image is a manifest list and contains multiple images - use --filter-by-os to select from:

  OS            DIGEST
  linux/amd64   sha256:c9dc4d07788ebc384a3d399a0e17f80b62a282b2513062a13ea702a811794a60
  linux/ppc64le sha256:c59c99d6ff1fe7b85790e24166cfc448a3c2ac3ef3422fce3c7259e48d2c9aab
  linux/s390x   sha256:07fcd16d5bee95196479b1e6b5b3b8064dd5359dac75e3d81f0bd4be6b8fe208
  linux/arm64   sha256:1d93a6beccc83e2a4c56ecfc37e921fe73d8964247c1a3ec34c4d66f175d9b3d
```

The CPU architecture for a NodePool is specified through its CR spec: `spec.arch`. Here is an example:
```
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  creationTimestamp: null
  name: hypershift-arm-us-east-1a
  namespace: clusters
spec:
  arch: arm64
  clusterName: hypershift-arm
  management:
    autoRepair: false
    upgradeType: Replace
  nodeDrainTimeout: 0s
  nodeVolumeDetachTimeout: 0s
  platform:
    aws:
      instanceProfile: hypershift-arm-2m289-worker
      instanceType: m6g.large
      rootVolume:
        size: 120
        type: gp3
      securityGroups:
      - id: sg-064ea63968d258493
      subnet:
        id: subnet-02c74cf1cf1e7413f
    type: AWS
  release:
    image: quay.io/openshift-release-dev/ocp-release-nightly@sha256:390a33cebc940912a201a35ca03927ae5b058fbdae9626f7f4679786cab4fb1c
  replicas: 3
status:
  replicas: 0
```

## Creating a HostedCluster with Arm NodePool Through HyperShift CLI

Create a new cluster, specifying the `RELEASE_IMAGE` and `ARCH`:

```shell linenums="1"
REGION=us-east-1
CLUSTER_NAME=example
BASE_DOMAIN=example.com
AWS_CREDS="$HOME/.aws/credentials"
PULL_SECRET="$HOME/pull-secret"
RELEASE_IMAGE="quay.io/openshift-release-dev/ocp-release-nightly@sha256:390a33cebc940912a201a35ca03927ae5b058fbdae9626f7f4679786cab4fb1c"
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

The HostedCluster will spin up with an Arm NodePool. The default AWS Arm instance type is `m6g.large`.

## Creating Arm NodePools on Existing HostedClusters Through HyperShift CLI

As long as a HostedCluster was created with a manifest listed image in the `--release-image`, Arm NodePools can be added to the HostedCluster:

```shell linenums="1"
CLUSTER_NAME=example
NODE_POOLNAME=example-worker
ARCH="arm64"

hypershift create nodepool aws \
--cluster-name $CLUSTER_NAME \
--name $NODE_POOLNAME \
--replicas=3 \
--arch $ARCH \
```