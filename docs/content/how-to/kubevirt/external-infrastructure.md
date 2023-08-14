# External Infrastructure

By default, the HyperShift operator hosts both the HostedCluster's control
plane pods and KubeVirt worker VMs within the same cluster.

With the external infrastructure feature, it possible to place the worker node
VMs on a separate cluster from the control plane pods.

## Understanding Hosting Cluster Types

**Management Cluster:** This is the OpenShift cluster that runs the HyperShift
operator and hosts the control plane pods for a HostedCluster.

**Infrastructure Cluster:** This is the OpenShift cluster that runs the
KubeVirt worker VMs for a HostedCluster.

By default, the management cluster also acts as the infrastructure cluster
that hosts VMs. However, for the external infrastructure use case, the
management and infrastructure clusters are distinctly different.

## Create a HostedCluster using External Infrastructure

**Prerequisites**
 * Creation of a namespace on the external infrastructure cluster for the KubeVirt worker nodes to be hosted in.
 * A kubeconfig for the external infrastructure cluster

Once the prerequisites are met, the `hcp` cli tool can be used to create
the guest cluster. In order to place the KubeVirt worker VMs on the
infrastructure cluster, use the `--infra-kubeconfig-file` and `--infra-namespace`
arguments.

Below is an example of creating a guest cluster using external infrastructure.

```shell linenums="1"
export CLUSTER_NAME=example
export PULL_SECRET="$HOME/pull-secret"
export MEM="6Gi"
export CPU="2"
export WORKER_COUNT="2"

hcp create cluster kubevirt \
--name $CLUSTER_NAME \
--node-pool-replicas $WORKER_COUNT \
--pull-secret $PULL_SECRET \
--memory $MEM \
--cores $CPU \
--infra-namespace=clusters-example \
--infra-kubeconfig-file=$HOME/external-infra-kubeconfig
```

This command will result in the control plane pods being hosted on the
management cluster that the HyperShift Operator runs on, while the KubeVirt
VMs will be hosted on a separate infrastructure cluster.

