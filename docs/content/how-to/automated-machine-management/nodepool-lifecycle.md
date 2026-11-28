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

To choose which Nodes are removed when you lower the replica count without scaling to zero, see
[Targeted Machine deletion during NodePool scale-down](targeted-machine-deletion.md).

### Deletion order

!!! note

    The `scaleDown` field is behind the `NodePoolScaleDown` feature gate. It is available only when the HyperShift
    Operator is installed with `--tech-preview-no-upgrade`.

By default, Cluster API deletes the Machines of a scaled-down NodePool in its `Random` order. To delete the oldest or
the newest Machines first, set `spec.scaleDown.deletionOrder`:

```yaml
spec:
  replicas: 3
  management:
    upgradeType: InPlace
  scaleDown:
    deletionOrder: Oldest
```

HyperShift sets this value as the deletion order of the NodePool's MachineSet (`InPlace`) or MachineDeployment
(`Replace`). Changing it does not trigger a rollout. Cluster API applies it as follows:

- Machines that are being deleted, annotated with `cluster.x-k8s.io/delete-machine`, being updated in place by
  Cluster API, or unhealthy are deleted first, in that order, except for the `Oldest` case below. A Machine whose
  `NodeHealthy` condition is `Unknown` counts as healthy. The deletion order only applies to the remaining healthy
  Machines.
- `Newest` and `Oldest` lose precision as Machines age. At 300 days old, Machines created up to about half an hour
  apart can get the same priority, and healthy Machines that are both older than about 375 days always do. Machines
  with the same priority are picked by name. With `Oldest`, a healthy Machine older than about 375 days also gets the
  same priority as an unhealthy Machine, so it can be deleted before the unhealthy one. For long-lived Machines,
  choose the Machines to remove with the `cluster.x-k8s.io/delete-machine` annotation instead. A fix is proposed in
  [kubernetes-sigs/cluster-api#14583](https://github.com/kubernetes-sigs/cluster-api/pull/14583).
- For the `Replace` upgrade type, while the MachineDeployment has more than one MachineSet, Cluster API scales down the
  oldest MachineSet first and applies the deletion order only within it.
- When the NodePool uses `spec.autoScaling`, the cluster autoscaler annotates the Machine that it removes, so the
  deletion order does not decide that removal.

To go back to the default order, remove the whole `scaleDown` field; an empty `scaleDown` is rejected.

While the feature gate is enabled, HyperShift owns the deletion order and clears a value that was set directly on the
MachineSet or MachineDeployment. The `scaleDown` field does not exist until the gate is enabled, so if you set such a
value directly, record it first. After you enable the gate, set it in `spec.scaleDown.deletionOrder` right away, and
do not scale down until `spec.deletion.order` on the MachineSet or MachineDeployment shows it. If the gate is disabled
later, the last value stays on the MachineSet or MachineDeployment.

### Scaling To Zero

Node(s) can become stuck when removing all Nodes from a cluster (scaling NodePool(s) down to zero) because the Node(s) cannot be drained successfully from the cluster.

Several conditions can prevent Node(s) from being drained successfully:

- The hosted cluster contains `PodDisruptionBudgets` that require at least one healthy pod, preventing eviction when there are no other nodes to reschedule onto.
- The hosted cluster contains pods that use `PersistentVolumes` that cannot be detached from the node.

!!! important

    This is expected behavior. When all nodes are removed simultaneously, pods protected by PodDisruptionBudgets cannot be evicted because the PDB constraints cannot be satisfied with no remaining nodes. As a result, the drain operation blocks indefinitely. Configure `nodeDrainTimeout` to ensure nodes are eventually removed after a bounded period.

#### Prevention

To prevent Nodes from becoming stuck when scaling down, set the `.spec.nodeDrainTimeout` and `.spec.nodeVolumeDetachTimeout` in the NodePool CR to a value greater than `0s`.

This forces Nodes to be removed once the timeout specified in the field has been reached, regardless of whether the node can be drained or the volumes can be detached successfully.

```
apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  name: example
  namespace: clusters
spec:
  nodeDrainTimeout: 30m
  nodeVolumeDetachTimeout: 10m
  # ...other fields...
```

!!! note

    See the [HyperShift API reference page](../../reference/api.md) for more details on these fields.

    For an alternative approach that skips draining entirely via machine annotations, see [Scaling down data plane to Zero](scale-to-zero-dataplane.md).
