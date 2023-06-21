# Configuring Storage

By default, if no advanced configuration is provided, the default storageclass
is used for the etcd volumes, the KubeVirt VM images, and the kubevirt csi
mappings.

## KubeVirt CSI StorageClass Mapping

KubeVirt CSI permits any infra storage class with the `ReadWriteMany` access
mode to be exposed to the guest cluster. This mapping of infra cluster storage
class to guest cluster storage class can be configured during cluster creation
using the `hypershift` cli tool and the `--infra-storage-class-mapping` cli
argument.

Below is an example of how to map two infra storage classes called `infra-sc1`
and `infra-sc2` to guest storage classes called `guest-sc1` and `guest-sc2`.
Note that `--infra-storage-class-mapping` argument can be used multiple times
within the create command.

```shell linenums="1"
export CLUSTER_NAME=example
export PULL_SECRET="$HOME/pull-secret"
export MEM="6Gi"
export CPU="2"
export WORKER_COUNT="2"

hypershift create cluster kubevirt \
--name $CLUSTER_NAME \
--node-pool-replicas $WORKER_COUNT \
--pull-secret $PULL_SECRET \
--memory $MEM \
--cores $CPU \
--infra-storage-class-mapping=infra-sc1/guest-sc1 \
--infra-storage-class-mapping=infra-sc2/guest-sc2
```

Once the guest cluster is created, the `guest-sc1` and `guest-sc2` storage
classes will be visible within the guest cluster. When users creat a PVC within
the guest cluster that utilizes one of these storage classes, KubeVirt CSI will
facilitate provisioning that volume using the infra storage class mapping
that was configured during cluster creation.

!!! note

    KubeVirt CSI only supports mapping an infrastructure storage class that is
    capable of `ReadWriteMany` (RWX) access.

## KubeVirt VM Root Volume Configuration

The storage class used to host the KubeVirt Virtual Machine root volumes can be
configured at cluster creation time using the `hypershift` cli tool and the
`--root-volume-storage-class` argument. Likewise, the size of the volume can be
configured using the `--root-volume-size` cli argument.

Below is an example of setting a custom storage class and volume size for the
KubeVirt VMs. The result will be a guest cluster with VMs hosted on 64Gi PVCs
hosted by the ocs-storagecluster-ceph-rdb storage class

```shell linenums="1"
export CLUSTER_NAME=example
export PULL_SECRET="$HOME/pull-secret"
export MEM="6Gi"
export CPU="2"
export WORKER_COUNT="2"

hypershift create cluster kubevirt \
--name $CLUSTER_NAME \
--node-pool-replicas $WORKER_COUNT \
--pull-secret $PULL_SECRET \
--memory $MEM \
--cores $CPU \
--root-volume-storage-class ocs-storagecluster-ceph-rbd \
--root-volume-size 64
```

## KubeVirt VM Image Caching

KubeVirt VM image caching is an advanced feature that can be used to optimize
both cluster startup times and storage utilization. This feature requires the
usage of a storage class capable of [smart cloning](https://docs.openshift.com/container-platform/4.13/virt/virtual_machines/virtual_disks/virt-cloning-a-datavolume-using-smart-cloning.html) and the `ReadWriteMany`
access mode.

Image caching works by importing the VM image once to a single PVC associated
with the HostedCluster, and then making a unique clone of that PVC for every
KubeVirt VM added as a worker node to the cluster. This reduces VM startup time
by only requiring a single image import, and can further reduce overall cluster
storage usage when the storage class supports copy on write cloning.

Image caching can be enabled during cluster creation using the `hypershift` cli
tool with the `--root-volume-cache-strategy=PVC` argument. Below is an example.


```shell linenums="1"
export CLUSTER_NAME=example
export PULL_SECRET="$HOME/pull-secret"
export MEM="6Gi"
export CPU="2"
export WORKER_COUNT="2"

hypershift create cluster kubevirt \
--name $CLUSTER_NAME \
--node-pool-replicas $WORKER_COUNT \
--pull-secret $PULL_SECRET \
--memory $MEM \
--cores $CPU \
--root-volume-cache-strategy=PVC
```

## ETCD Storage Configuration

The storage class used to host the etcd data can be customized at cluster
creation time using the `hypershift` cli and the `--etcd-storage-class` cli
argument. When no `--etcd-storage-class` argument is provided, the default
storage class will be used.

Below is an example of how to configure usage of a storage class for etcd.

```shell linenums="1"
export CLUSTER_NAME=example
export PULL_SECRET="$HOME/pull-secret"
export MEM="6Gi"
export CPU="2"
export WORKER_COUNT="2"

hypershift create cluster kubevirt \
--name $CLUSTER_NAME \
--node-pool-replicas $WORKER_COUNT \
--pull-secret $PULL_SECRET \
--memory $MEM \
--cores $CPU \
--etcd-storage-class="local"
```





