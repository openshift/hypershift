# Create a Kubevirt cluster

Install a nested OCP cluster running on VMs within a management OCP cluster

## Prerequisites

* The HyperShift CLI (`hypershift`).

    Install it using Go 1.17+:
       ```shell
       go get -u github.com/openshift/hypershift@latest
       ```

* Admin access to an OpenShift cluster (version 4.8+) specified by the `KUBECONFIG` environment variable.
* The management OCP cluster must have Openshift Virtualization installed on it (More info:
  https://docs.openshift.com/container-platform/4.9/virt/about-virt.html?extIdCarryOver=true&sc_cid=7013a0000026OSTAA2)
* The management OCP cluster must have valid storage class (More info:
  https://docs.openshift.com/container-platform/4.9/post_installation_configuration/storage-configuration.html)
* The OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`).
* A valid [pull secret](https://cloud.redhat.com/openshift/install/aws/installer-provisioned) file for the `quay.io/openshift-release-dev` repository.
* A valid RHCOS container disk image. Currently there is no officially supported RHCOS container disk image, therefore
  it must be provided when creating the cluster (The following image can be used, but currently there is no guarenty
that it works properly quay.io/containerdisks/rhcos:4.9 - Work in progress)

## Before you begin

Install HyperShift into the management cluster:

```shell linenums="1"
hypershift install
```

## Create a HostedCluster

Create a new cluster, specifying the RHCOS container disk image and the pull secret
provided in the [Prerequisites](#prerequisites):

```shell linenums="1"
export CLUSTER_NAME=example
export PULL_SECRET="$HOME/pull-secret"
export CONTAINER_DISK=quay.io/containerdisks/rhcos:4.9

hypershift create cluster kubevirt \
--name $CLUSTER_NAME \
--node-pool-replicas=3 \
--pull-secret $PULL_SECRET \
--containerdisk ${CONTAINER_DISK} \
```

!!! note

    A default NodePool will be created for the cluster with 3 replicas per the
    `--node-pool-replicas` flag.

After a few minutes, check the `hostedclusters` resources in the `clusters`
namespace and when ready it will look similar to the following:

```
oc get --namespace clusters hostedclusters
NAME      VERSION   KUBECONFIG                 AVAILABLE
example   4.8.0     example-admin-kubeconfig   True
```

Eventually the cluster's kubeconfig will become available and can be printed to
standard out using the `hypershift` CLI:

```shell
hypershift create kubeconfig
```

## Add NodePools

Create additional NodePools for a cluster by specifying a name, number of replicas
and additional information such as instance type.

Create a NodePool:

```shell linenums="1"
export NODEPOOL_NAME=${CLUSTER_NAME}-work
export NODEPOOL_REPLICAS=2

hypershift create nodepool kubevirt \
  --cluster-name $CLUSTER_NAME \
  --name $NODEPOOL_NAME \
  --node-count $NODEPOOL_REPLICAS \
  --containerdisk ${CONTAINER_DISK} \
```

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

oc scale nodepool/$NODEPOOL_NAME --namespace clusters --replicas=$NODEPOOL_REPLICAS
```

## Delete a HostedCluster

To delete a HostedCluster:

```shell
hypershift destroy cluster kubevirt --name $CLUSTER_NAME
```
