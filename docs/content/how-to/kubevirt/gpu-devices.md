# GPU Devices

It is possible to attach one or more GPUs to a HCP KubeVirt clusters through
the use of the NodePool api.

## Prerequisites

Before a GPU device can be attached to a HCP KubeVirt NodePool, the GPU must
first be exposed as a resource on the Node that the GPU resides on. For
example this [Nvidia Operator](https://docs.nvidia.com/datacenter/cloud-native/openshift/latest/openshift-virtualization.html) documentation outlines how to expose Nvidia GPUs
as a resource on an OpenShift cluster. Once the GPU is exposed as an
[extended resource](https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/#extended-resources)
on the node, it can then be assigned to a NodePool. 

## Attaching GPU devices to NodePools Using the CLI

Using the `hcp` cli, GPU devices can be attached to NodePools through the
usage of the `--host-device-name` cli argument.


This argument takes the device's resource name as it has been advertised on
the infrastructure node. The argument also takes an optional count that
represents the number of GPU devices to attach to each VM in the NodePool.
By default, the count is `1`.

In the example below, we are attaching 2 GPUs to a NodePool of size 3. This
means each of the 3 VMs in the NodePool will have 2 GPUs each.


```shell linenums="1"
export CLUSTER_NAME=example
export PULL_SECRET="$HOME/pull-secret"
export MEM="16Gi"
export CPU="2"
export WORKER_COUNT="3"

hcp create cluster kubevirt \
--name $CLUSTER_NAME \
--node-pool-replicas $WORKER_COUNT \
--pull-secret $PULL_SECRET \
--memory $MEM \
--cores $CPU \
--host-device-name="nvidia-a100,count:2"
```

Note that the `--host-device-name` argument can be specified multiple times
in order to attach multiple devices of different types.

## Attaching GPU Devices via the NodePool API Directly

The same example provided above with the hcp cli can also be expressed
directly using the NodePool API. Below is an example of what the yaml
would look like to configure a NodePool with 3 replicas with 2 GPU devices
for each replica. Take note of the `nodepool.spec.platform.kubevirt.hostDevices`
section. The `hostDevices` are a list, meaning it is possible to attach
multiple GPU devices of different types as well.

```yaml linenums="1"
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: example
  namespace: clusters
spec:
  arch: amd64
  clusterName: example
  management:
    autoRepair: false
    upgradeType: Replace
  nodeDrainTimeout: 0s
  nodeVolumeDetachTimeout: 0s
  platform:
    kubevirt:
      attachDefaultNetwork: true
      compute:
        cores: 2
        memory: 16Gi
      hostDevices:
      - count: 2
        deviceName: nvidia-a100
      networkInterfaceMultiqueue: Enable
      rootVolume:
        persistent:
          size: 32Gi
        type: Persistent
    type: KubeVirt
  replicas: 3
```

