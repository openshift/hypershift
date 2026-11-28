---
title: Develop HyperShift components in-cluster
---

# How to develop HyperShift components in-cluster

For in-cluster development, build and push a custom image as described in
[Use custom operator images](custom-images.md). The `Dockerfile.dev` image
includes the HyperShift binaries used in the examples below. Set `IMAGE` to the
image pullspec you pushed before running them:

```shell
export IMAGE=quay.io/<your-quay-account>/hypershift:<tag>
```

## Run a custom `hypershift-operator` interactively

Scale down the operator deployment before starting a debug pod:

```shell
oc scale --replicas 0 --namespace hypershift deployments/operator
```

Alternatively, install HyperShift with the `--development` flag to create the
operator deployment with zero replicas:

```shell
go run . install \
  --oidc-storage-provider-s3-bucket-name=$BUCKET_NAME \
  --oidc-storage-provider-s3-region=$BUCKET_REGION \
  --oidc-storage-provider-s3-credentials=$AWS_CREDS \
  --development
```

Start the operator from your custom image:

```shell
oc debug --namespace hypershift deployments/operator --image "$IMAGE" -- \
  /usr/bin/hypershift-operator run \
  --oidc-storage-provider-s3-region "$BUCKET_REGION" \
  --oidc-storage-provider-s3-bucket-name "$BUCKET_NAME" \
  --oidc-storage-provider-s3-credentials /etc/oidc-storage-provider-s3-creds/credentials \
  --namespace hypershift \
  --pod-name operator-debug
```

Use the same bucket and region values as the HyperShift installation. Press
`ctrl-c` to stop and delete the debug pod.

## Configure a HostedCluster for control plane development

The `hypershift.openshift.io/debug-deployments` annotation on a `HostedCluster`
scales the named control plane deployments to zero while keeping their
`Deployment` resources available as templates for debug pods. Set it to a
comma-separated list of deployment names, for example:

```shell
oc annotate -n clusters HostedCluster test-cluster \
  hypershift.openshift.io/debug-deployments=control-plane-operator,ignition-server
```

Change `test-cluster` to the name of your HostedCluster. Remove a component
from the annotation value to restore its deployment. The
`hypershift.openshift.io/pod-security-admission-label-override=baseline`
annotation may also be needed to run debug pods locally:

```shell
oc annotate -n clusters HostedCluster test-cluster \
  hypershift.openshift.io/pod-security-admission-label-override=baseline
```

## Run custom control plane components interactively

Set `NAMESPACE` to the HostedCluster's control plane namespace, usually
`clusters-<cluster-name>`. Start the control plane operator from your custom
image with:

```shell
oc debug --namespace "$NAMESPACE" deployments/control-plane-operator --image "$IMAGE" -- \
  /usr/bin/control-plane-operator run
```

To run the ignition server from the same image, use:

```shell
oc debug --namespace "$NAMESPACE" deployments/ignition-server --image "$IMAGE" -- \
  /usr/bin/ignition-server
```

Press `ctrl-c` to stop and delete either debug pod. The default arguments for
both components should be sufficient to get started.
