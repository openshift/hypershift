---
title: Develop HyperShift components in-cluster
---

# How to develop HyperShift components in-cluster

Sometimes when developing HyperShift components it's useful to iterate on new
binary builds inside the cluster itself, especially when working on functionality
that depends on the Kubernetes or cloud environment for one reason or another.

Because such in-cluster build/image/publish/redeploy development workflows can be
very tedious and slow, the HyperShift project includes a few tools and techniques
to help make the feedback loop as fast as possible.

This guide makes use of the [ko](https://github.com/google/ko) tool to rapidly
build lightweight images which are then published directly into an OCP cluster's
internal registry. This approach has the following properties which can speed up
development:

- No local container runtime required to build images, and image builds are
  extremely fast.
- Resulting images are almost as small as the Go binary being published.
- Images are published directly into OCP's internal image registry, so images
  are immediately available on or near the machines that will be pulling them.

## Prerequisites

- An OCP 4.9+ cluster
- The `oc` CLI tool
- The [ko](https://github.com/google/ko) CLI tool

For this workflow, the OCP cluster must be configured to expose its internal
image registry externally so the `ko` tool can publish to it.

First, expose the cluster's image registry:

```shell
oc patch configs.imageregistry.operator.openshift.io/cluster --patch '{"spec":{"defaultRoute":true}}' --type=merge
```

Next, generate an authentication token for the registry and install it into the
local Docker config file so that `ko` can push images into the registry. Be sure
to replace `<password>` with your actual `kubeadmin` password.

```shell linenums="1"
oc login -u kubeadmin -p <password>
oc registry login --to=$HOME/.docker/config.json --skip-check --registry $(oc get routes --namespace openshift-image-registry default-route -o jsonpath='{.spec.host}')
```

Finally, configure OCP to allow any authenticated user to pull images from the
internal registry. This will enable HyperShift component pods to pull the custom
images you publish.

```shell
oc create clusterrolebinding authenticated-registry-viewer --clusterrole registry-viewer --group system:authenticated
```

## Build and publish a component

To build and publish a given component into the OCP cluster from local source,
use the `publish-ocp.sh` script. This tool uses `ko` to build and publish
the image, and will output to stdout a single line containing the _internal_ pullspec
suitable for use by any HyperShift component deployment.

For example, to build and publishing the `hypershift-operator`, run:

```shell
hack/publish-ocp.sh ./hypershift-operator
```

Here's what the output will look like:

```linenums="1" hl_lines="9 10"
2021/12/01 16:49:54 Using base gcr.io/distroless/static:nonroot for github.com/openshift/hypershift/hypershift-operator
2021/12/01 16:49:55 Building github.com/openshift/hypershift/hypershift-operator for linux/amd64
2021/12/01 16:50:02 Publishing default-route-openshift-image-registry.apps.dmace-7894.devcluster.openshift.com/hypershift/hypershift-operator-cd22693e35e87f2323fb625057793c02:latest
2021/12/01 16:50:02 existing blob: sha256:250c06f7c38e52dc77e5c7586c3e40280dc7ff9bb9007c396e06d96736cf8542
2021/12/01 16:50:02 existing blob: sha256:e8614d09b7bebabd9d8a450f44e88a8807c98a438a2ddd63146865286b132d1b
2021/12/01 16:50:02 existing blob: sha256:cde5c5024aed9d3daaa3cd7b87fa21a66b10d2f2e8a1b9d339e2fb505cbde8c0
2021/12/01 16:50:02 existing blob: sha256:a589e39bc5bc084dd0ec79f5492ff9dc2ac6dbbb5fb95eb200b319246a7b8207
2021/12/01 16:50:03 default-route-openshift-image-registry.apps.dmace-7894.devcluster.openshift.com/hypershift/hypershift-operator-cd22693e35e87f2323fb625057793c02:latest: digest: sha256:20b0baf90c58a92a5e384eaa8d40cd47cc1c8cabce27bedccd7bbc2f54ca4c5b size: 953
2021/12/01 16:50:03 Published default-route-openshift-image-registry.apps.dmace-7894.devcluster.openshift.com/hypershift/hypershift-operator-cd22693e35e87f2323fb625057793c02@sha256:20b0baf90c58a92a5e384eaa8d40cd47cc1c8cabce27bedccd7bbc2f54ca4c5b
image-registry.openshift-image-registry.svc:5000/hypershift/hypershift-operator-cd22693e35e87f2323fb625057793c02@sha256:20b0baf90c58a92a5e384eaa8d40cd47cc1c8cabce27bedccd7bbc2f54ca4c5b
```

The `publish-ocp.sh` script prints only the internal repo pullspec to stdout to
make it easy to incorporate the script into pipelines.

!!! note

    Notice on line 9 that public pullspec of the image is
    `default-route-openshift-image-registry.apps.dmace-7894.devcluster.openshift.com/hypershift/hypershift-operator-cd22...`.
    Pods in the cluster cannot pull the image using the public repo name because the
    host's certificate is likely self-signed, which would require additional
    configuration in the cluster to enable pods to pull it.

    Pods must reference the _internal repo pullspec_ as printed to stdout on line
    10: `image-registry.openshift-image-registry.svc:5000/hypershift/hypershift-operator-cd22...`.

## Launch a custom `hypershift-operator` image interactively

To iterate on the `hypershift-operator` binary in-cluster interactively, first
scale down the operator's deployment:

```shell
oc scale --replicas 0 --namespace hypershift deployments/operator
```

Alternatively, run the HyperShift CLI `install` command with the `--development`
flag which sets up the deployment with zero replicas:

```shell
go run . install \
  --oidc-storage-provider-s3-bucket-name=$BUCKET_NAME \
  --oidc-storage-provider-s3-region=$BUCKET_REGION \
  --oidc-storage-provider-s3-credentials=$AWS_CREDS \
  --development
```

Now, you can build and publish the `hypershift-operator` image and run it interactively
in a single shot using `publish-ocp.sh` together with the `oc debug` command:

```shell hl_lines="3 4"
oc debug --namespace hypershift deployments/operator --image $(hack/publish-ocp.sh ./hypershift-operator) -- \
  /ko-app/hypershift-operator run \
  --oidc-storage-provider-s3-region $BUCKET_REGION \
  --oidc-storage-provider-s3-bucket-name $BUCKET_NAME \
  --oidc-storage-provider-s3-credentials /etc/oidc-storage-provider-s3-creds/credentials \
  --namespace hypershift \
  --pod-name operator-debug
```

!!! note

    Make sure to replace `$BUCKET_NAME` and `$BUCKET_REGION` with the same values used to
    install HyperShift.

Your latest code should be deployed and logs should soon begin streaming. Just
press `ctrl-c` to terminate and delete the pod.

!!! note

    See [Use custom operator images](./custom-images.md) to use your own registry.

## Configure a HostedCluster for iterative control plane development

To iterate on control plane components which are deployed and managed in a
`HostedCluster` control plane namespace (e.g. the `control-plane-operator`
or `ignition-server`), it's possible to configure the `HostedCluster` resource to
scale down individual control plane components and facilitate various development
workflows.

The `hypershift.openshift.io/debug-deployments` annotation on a `HostedCluster`
is used to configure individual control plane components as targets for development
and debugging. The value of the annotation is a comma-delimited list of control
plane deployment names. Any control plane component in the list will always be
scaled to 0, enabling developers to replace the components with their own processes
(inside or outside the cluster) while preserving the `Deployment` resources to
use as templates for the replacement process environments.

For example, to scale the `control-plane-operator` and `ignition-server` deployments
to 0:

```shell
oc annotate -n clusters HostedCluster test-cluster hypershift.openshift.io/debug-deployments=control-plane-operator,ignition-server
```

!!! note

    Update the name of the HostedCluster to match your cluster.

This will result in a `HostedCluster` like so:

```yaml linenums="1" hl_lines="5"
apiVersion: hypershift.openshift.io/v1alpha1
kind: HostedCluster
metadata:
  annotations:
    hypershift.openshift.io/debug-deployments: control-plane-operator,ignition-server
  namespace: clusters
  name: test
spec:
  release:
    image: quay.io/openshift-release-dev/ocp-release:4.9.0-x86_64
# <remainder of resource omitted>
```

To scale back up a given component's original deployment simply remove the component's
deployment name from the list.

The `hypershift.openshift.io/pod-security-admission-label-override` annotation
may also need to be set in order to run debug pods locally.

```shell
oc annotate -n clusters HostedCluster test-cluster hypershift.openshift.io/pod-security-admission-label-override=baseline
```

## Launch a custom `control-plane-operator` image interactively

To iterate on the `control-plane-operator` binary in-cluster interactively, first
[configure the HostedCluster](#configure-a-hostedcluster-for-iterative-control-plane-development)
to scale down the `control-plane-operator` deployment.

Now, you can build and publish the `control-plane-operator` image and run it interactively
in a single shot using `publish-ocp.sh` together with the `oc debug` command. Be
sure to replace `$NAMESPACE` with the namespace of the control plane that was deployed
for the `HostedCluster`.

```shell
oc debug --namespace $NAMESPACE deployments/control-plane-operator --image $(hack/publish-ocp.sh ./control-plane-operator) -- /ko-app/control-plane-operator run
```

Your latest code should be deployed and logs should soon begin streaming. Just
press `ctrl-c` to terminate and delete the pod.

!!! note

    The default arguments to `control-plane-operator run` should be sufficient to
    get started.

## Launch a custom `ignition-server` interactively

To iterate on the ignition server in-cluster interactively, first
[configure the HostedCluster](#configure-a-hostedcluster-for-iterative-control-plane-development)
to scale down the `ignition-server` deployment.

Now, you can build and publish the `control-plane-operator` image and run the
`ignition-server` command interactively in a single shot using `publish-ocp.sh`
together with the `oc debug` command. Be sure to replace `$NAMESPACE` with the
namespace of the control plane that was deployed for the `HostedCluster`.

```shell
oc debug --namespace $NAMESPACE deployments/ignition-server --image $(hack/publish-ocp.sh ./control-plane-operator) -- /ko-app/control-plane-operator ignition-server
```

Your latest code should be deployed and logs should soon begin streaming. Just
press `ctrl-c` to terminate and delete the pod.

!!! note

    The default arguments to `ignition-server` should be sufficient to
    get started.
