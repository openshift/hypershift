---
title: Use custom operator images
---

# Use custom operator images

This guide explains how to build and deploy custom HyperShift Operator (HO) and Control Plane Operator (CPO) images for development and testing.

## Background

The HyperShift repository produces several binaries:

- **hypershift-operator** — manages `HostedCluster` and `NodePool` resources on the management cluster.
- **control-plane-operator** — manages control plane components for each hosted cluster. In production, this image comes from the OpenShift release payload.

The repository includes a `Dockerfile.dev` that builds an **all-in-one development image** containing both the HO and CPO binaries (plus `hypershift`, `hcp`, `karpenter-operator`, and `control-plane-pki-operator`). This is the easiest way to build a custom image for development.

## Building a custom image

### Option 1: Using Dockerfile.dev (recommended for development)

`Dockerfile.dev` compiles all binaries inside the container and produces a single image you can use for both the HO and CPO.

```shell
export IMG=quay.io/<your-quay-account>/hypershift
export TAG=my-feature-$(date +%Y-%m-%d)

podman build -f ./Dockerfile.dev --platform=linux/amd64 -t ${IMG}:${TAG} .
podman push ${IMG}:${TAG}
```

!!! tip

    Use a descriptive tag (e.g. the Jira ticket and date) so you can easily identify the image later. For example: `quay.io/rh_ee_jdoe/hypershift:CNTRLPLANE-1234-2026-04-02`

### Option 2: Using the Makefile

```shell
export QUAY_ACCOUNT=<your-quay-account>

make build
make RUNTIME=podman IMG=quay.io/${QUAY_ACCOUNT}/hypershift:latest docker-build docker-push
```

## Deploying a custom HyperShift Operator image

Use the `hypershift install` command with the `--hypershift-image` flag to deploy the management cluster operator with your custom image:

```shell
hypershift install \
  --hypershift-image quay.io/<your-quay-account>/hypershift:${TAG} \
  ... # other platform-specific flags
```

See the platform-specific installation guides for the full set of required flags.

### Private registries

If your image repository is private, create a pull secret and patch the operator ServiceAccount:

```shell
oc create secret --namespace hypershift generic hypershift-operator-pull-secret \
  --from-file=.dockerconfig=/path/to/pull-secret --type=kubernetes.io/dockerconfig

oc patch serviceaccount --namespace hypershift operator \
  -p '{"imagePullSecrets": [{"name": "hypershift-operator-pull-secret"}]}'
```

## Deploying a custom Control Plane Operator image

The CPO image is normally resolved from the OpenShift release payload. To override it with your custom image on an existing `HostedCluster`, use the `hypershift.openshift.io/control-plane-operator-image` annotation:

```shell
oc annotate hostedcluster <cluster-name> -n <namespace> \
  hypershift.openshift.io/control-plane-operator-image=quay.io/<your-quay-account>/hypershift:${TAG} \
  --overwrite
```

For example:

```shell
oc annotate hostedcluster my-hosted-cluster -n clusters \
  hypershift.openshift.io/control-plane-operator-image=quay.io/rh_ee_jdoe/hypershift:CNTRLPLANE-1234-2026-04-02 \
  --overwrite
```

This triggers a rollout of the control plane with your custom CPO image. You can verify the new image is running:

```shell
# Find the control plane namespace (usually clusters-<cluster-name>)
oc get pods -n clusters-<cluster-name> -l app=control-plane-operator -o jsonpath='{.items[0].spec.containers[0].image}'
```

To revert to the release payload CPO image, remove the annotation:

```shell
oc annotate hostedcluster <cluster-name> -n <namespace> \
  hypershift.openshift.io/control-plane-operator-image-
```

!!! note

    When using a `Dockerfile.dev` image, the same image works for both the HO and CPO because it contains all binaries.

## Full development workflow example

A typical workflow for testing a change across both operators:

```shell
# 1. Build and push the all-in-one dev image
export IMG=quay.io/rh_ee_jdoe/hypershift
export TAG=my-feature-$(date +%Y-%m-%d)
podman build -f ./Dockerfile.dev --platform=linux/amd64 -t ${IMG}:${TAG} .
podman push ${IMG}:${TAG}

# 2. Install HyperShift with the custom HO image
hypershift install \
  --hypershift-image ${IMG}:${TAG} \
  ... # other platform-specific flags

# 3. Create a HostedCluster (or use an existing one)

# 4. Override the CPO image on the HostedCluster
oc annotate hostedcluster my-cluster -n clusters \
  hypershift.openshift.io/control-plane-operator-image=${IMG}:${TAG} \
  --overwrite
```

## See also

- [Develop in cluster](develop_in_cluster.md) — for rapid in-cluster iteration using `ko`
- [Run HyperShift operator locally](run-hypershift-operator-locally.md) — for running the operator outside the cluster
- [CPO Overrides](cpo-overrides.md) — for production CPO image overrides by version and platform
