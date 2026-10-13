---
title: NodePool lifecycle
---

# NodePool lifecycle

NodePools represent homogeneous groups of Nodes with a common lifecycle management and upgrade cadence.

## Upgrades and data propagation

NodePools support two types of rolling upgrades: **Replace** and **InPlace**, specified via [UpgradeType](../../reference/api.md#hypershift.openshift.io/v1beta1.UpgradeType).

!!! important

    You cannot switch the UpgradeType once the NodePool is created. You must specify UpgradeType during NodePool
    creation. Modifying the field after the fact may cause nodes to become unmanaged.

For a comprehensive reference on what triggers a rollout, upgrade strategies, rollout lifecycle, and monitoring, see [NodePool Rollouts](../../reference/nodepool-rollouts.md).


## Triggering Upgrades examples

### Upgrading to a new OCP version

These upgrades can be triggered via changing the `spec.release.image` of the NodePool. Note that you should only upgrade NodePools to the current version of the Hosted Control Plane.

### Adding a new MachineConfig

You can create a MachineConfig inside a ConfigMap in the management cluster as follows:

```
apiVersion: v1
kind: ConfigMap
metadata:
  name: ${CONFIGMAP_NAME}
  namespace: clusters
data:
  config: |
    apiVersion: machineconfiguration.openshift.io/v1
    kind: MachineConfig
    metadata:
      labels:
        machineconfiguration.openshift.io/role: worker
      name: ${MACHINECONFIG_NAME}
    spec:
      config:
        ignition:
          version: 3.2.0
        storage:
          files:
          - contents:
              source: data:...
            mode: 420
            overwrite: true
            path: ${PATH}
```

Once that is applied to the cluster, you can specify that to the NodePool via:

```
spec:
  config:
    - name: ${CONFIGMAP_NAME}
```

## Scale Down

Scaling a NodePool down will remove Nodes from the hosted cluster.

### Scaling To Zero

Node(s) can become stuck when removing all Nodes from a cluster (scaling NodePool(s) down to zero) because the Node(s) cannot be drained successfully from the cluster.

Several conditions can prevent Node(s) from being drained successfully:

- The hosted cluster contains `PodDisruptionBudgets` that require at least 
- The hosted cluster contains pods that use `PersistentVolumes``

#### Prevention

To prevent Nodes from becoming stuck when scaling down, set the `.spec.nodeDrainTimeout` and `.spec.nodeVolumeDetachTimeout` in the NodePool CR to a value greater than `0s`. 

This forces Nodes to be removed once the timeout specified in the field has been reached, regardless of whether the node can be drained or the volumes can be detached successfully.

!!! note
    See the [Hypershift API reference page](../../reference/api.md) for more details.
