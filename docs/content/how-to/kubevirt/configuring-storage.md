# Storage Overview

Configuring storage for KubeVirt Hosted Control Planes falls within three
categories, each of which have differing requirements.

**ETCD Storage**

ETCD requires usage of high performance persistent storage on the management
cluster hosting the ETCD pods. Due to the performance requirements, usage of a
local storage csi driver such as [LVM Storage](https://docs.openshift.com/container-platform/latest/storage/persistent_storage/persistent_storage_local/persistent-storage-using-lvms.html) is prefered.
When a guest cluster is created in HighAvailability mode, ETCD is replicated
in pods across three separate management cluster nodes. This replication
ensures data resiliency even when a local storage csi driver is in use.

More information about ETCD storage configuration can be found in the
[ETCD Storage Configuration](#etcd storage configuration) section.

**Node Root Volume Storage**

In a KubeVirt Hosted Control Plane, the worker nodes are hosted in KubeVirt VMs.
It is recommended to use a csi driver capable of providing ReadWriteMany
access mode and Block volume mode for the VM root volume storage. This allows
the KubeVirt VMs to live migrate and remain available even when the underlying
infra cluster nodes are disrupted. Ceph is an example of a csi driver that
would meet these requirements.

More information about configuring root volume storage can be found in the
[KubeVirt VM Root Volume Configuration](#kubevirt vm root volume configuration) section.

**KubeVirt CSI Storage**

The KubeVirt CSI driver allows storage classes present on the infra cluster (the cluster the KubeVirt VMs run
on)
to be mapped directly to storage classes within the KubeVirt guest cluster.
This lets the guest cluster utilize the same storage that is available on the
infra cluster.

It is recommended to use an underlying infra storage class capable of
ReadWriteMany access mode and Block volume mode for KubeVirt CSI. This allows
KubeVirt CSI to pass storage to the VMs in a way that still allows for the VMs
to live migrate and be portable across infra nodes.

Below is a chart that outlines the current features of KubeVirt CSI as they map
to the infra cluster's storage class.

| Infra CSI Capability  | Guest CSI Capability               | VM Live Migration Support | Notes                                           | 
|-----------------------|------------------------------------|-----------------------------------------------------------------------------|
| RWX Block             | RWO (Block/Filesystem) RWX (Block) | Supported                 |                                                 |
| RWO Block             | RWO (Block/Filesystem)             | Not Supported             |                                                 |
| RWO Filesystem        | RWO (Block/Filesystem)             | Not Supported             | suboptimal guest block volume mode performance. |

More information about configuring KubeVirt CSI can be found in the
[KubeVirt CSI StorageClass Mapping](#kubevirt csi storageclass mapping) section.

# Configuring Storage

By default, if no advanced configuration is provided, the default storageclass
is used for the the KubeVirt VM images, the kubevirt csi mapping, and the
etcd volumes.

## KubeVirt CSI StorageClass Mapping

KubeVirt CSI permits any infra storage class with the `ReadWriteMany` access
mode to be exposed to the guest cluster. This mapping of infra cluster storage
class to guest cluster storage class can be configured during cluster creation
using the `hcp` cli tool and the `--infra-storage-class-mapping` cli
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

hcp create cluster kubevirt \
--name $CLUSTER_NAME \
--node-pool-replicas $WORKER_COUNT \
--pull-secret $PULL_SECRET \
--memory $MEM \
--cores $CPU \
--infra-storage-class-mapping=infra-sc1/guest-sc1 \
--infra-storage-class-mapping=infra-sc2/guest-sc2
```

Once the guest cluster is created, the `guest-sc1` and `guest-sc2` storage
classes will be visible within the guest cluster. When users create a PVC
within the guest cluster that utilizes one of these storage classes, KubeVirt
CSI will facilitate provisioning that volume using the infra storage class
mapping that was configured during cluster creation.

!!! note

    KubeVirt CSI only supports mapping an infrastructure storage class that is
    capable of `ReadWriteMany` (RWX) access.

## KubeVirt VM Root Volume Configuration

The storage class used to host the KubeVirt Virtual Machine root volumes can be
configured at cluster creation time using the `hcp` cli tool and the
`--root-volume-storage-class` argument. Likewise, the size of the volume can be
configured using the `--root-volume-size` cli argument.

Below is an example of setting a custom storage class and volume size for the
KubeVirt VMs. The result will be a guest cluster with VMs hosted on 64Gi PVCs
hosted by the `ocs-storagecluster-ceph-rdb` storage class

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
--root-volume-storage-class ocs-storagecluster-ceph-rbd \
--root-volume-size 64
```

## KubeVirt VM Image Caching

KubeVirt VM image caching is an advanced feature that can be used to optimize
both cluster startup time and storage utilization. This feature requires the
usage of a storage class capable of [smart cloning](https://docs.openshift.com/container-platform/4.13/virt/virtual_machines/virtual_disks/virt-cloning-a-datavolume-using-smart-cloning.html) and the `ReadWriteMany`
access mode.

Image caching works by importing the VM image once to a single PVC associated
with the HostedCluster, and then making a unique clone of that PVC for every
KubeVirt VM added as a worker node to the cluster. This reduces VM startup time
by only requiring a single image import, and can further reduce overall cluster
storage usage when the storage class supports copy on write cloning.

Image caching can be enabled during cluster creation using the `hcp` cli
tool with the `--root-volume-cache-strategy=PVC` argument. Below is an example.


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
--root-volume-cache-strategy=PVC
```

## ETCD Storage Configuration

The storage class used to host the etcd data can be customized at cluster
creation time using the `hcp` cli and the `--etcd-storage-class` cli
argument. When no `--etcd-storage-class` argument is provided, the default
storage class will be used.

Below is an example of how to configure usage of a storage class for etcd.

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
--etcd-storage-class="local"
```
