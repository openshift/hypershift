---
title: Targeted Machine deletion during NodePool scale-down
---

# Targeted Machine deletion during NodePool scale-down

When you lower `spec.replicas` on a NodePool, Cluster API (CAPI) selects which Machines to delete. HyperShift does not
set a deletion order on the MachineDeployment or MachineSet that backs a NodePool, so CAPI uses its default `Random`
order. To choose the Machines that are removed, annotate them with `cluster.x-k8s.io/delete-machine` on the management
cluster before you lower `spec.replicas`.

This page covers removing specific Nodes while the NodePool keeps running at a lower replica count. It is not the
[Scaling down data plane to Zero](scale-to-zero-dataplane.md) procedure, which annotates every Machine with
`machine.cluster.x-k8s.io/exclude-node-draining=` and scales all NodePools to zero. The two annotations have different
effects and are not interchangeable:

| Annotation | Effect |
|------------|--------|
| `cluster.x-k8s.io/delete-machine` | Gives the Machine priority for deletion when its MachineSet scales down. It does not delete the Machine on its own and does not change how the Node is drained. |
| `machine.cluster.x-k8s.io/exclude-node-draining` | Skips draining the Node when the Machine is deleted. It does not affect which Machine is selected for deletion. |

## How the annotation works

- The CAPI MachineSet controller only checks whether the `cluster.x-k8s.io/delete-machine` key is present in the
  Machine's annotations. The value is ignored. This page uses `yes`, the value shown in the Cluster API
  `MachineSetDeletionOrder` API documentation.
- Set it as an annotation on the Machine object. The controller does not read labels, so a label with the same key has
  no effect.
- In Cluster API v1.9.0 and later, when the MachineSet has more Machines than its desired replicas, annotated Machines
  are deleted first under every MachineSet deletion order (`Random`, `Newest`, and `Oldest`). Only Machines that are
  already being deleted rank higher. Earlier versions behave differently. See [Limitations](#limitations).
- Deleting a selected Machine follows the normal Machine deletion process. The Node is drained and the NodePool's
  `nodeDrainTimeout` and `nodeVolumeDetachTimeout` apply. See [NodePool lifecycle](nodepool-lifecycle.md#scale-down).

For the upstream description, see the `cluster.x-k8s.io/delete-machine` entry in the Cluster API book's
[Labels and Annotations](https://cluster-api.sigs.k8s.io/reference/api/labels-and-annotations.html) reference.

## Prerequisites

- Access to the management cluster (where the HyperShift Operator runs), with permission to read and annotate
  `machines.cluster.x-k8s.io` and read `machinesets.cluster.x-k8s.io` in the hosted control plane namespace, and to
  read and scale the NodePool in the HostedCluster namespace. Machine objects are in the hosted control plane namespace
  on the management cluster, not in the hosted cluster. If you do not have management cluster access, this procedure is
  not available to you.
- A kubeconfig for the hosted cluster with permission to list Nodes. The Node checks in this procedure run against the
  hosted cluster.
- The NodePool sets `spec.replicas`. NodePools that set `spec.autoScaling` are out of scope because their replica count
  is managed by the cluster autoscaler, and the NodePool API does not allow both fields to be set.
- The NodePool's Nodes are backed by CAPI Machines. NodePools on the `None` platform, and IBM Cloud NodePools that use
  the `UPI` provider type, have no Machines, so this procedure does not apply to them.
- The NodePool is not paused with `spec.pausedUntil`. While the NodePool is paused, the NodePool controller ignores
  changes to it, including a new replica count.

Run all commands on this page against the management cluster unless a step says otherwise.

Set the following variables. The hosted control plane namespace is `<hosted_cluster_namespace>-<hosted_cluster_name>`,
with any `.` in the HostedCluster name replaced by `-`.

```bash
export HC_NAMESPACE=<hosted_cluster_namespace>
export HC_NAME=<hosted_cluster_name>
export NODEPOOL_NAME=<nodepool_name>
export HCP_NAMESPACE="${HC_NAMESPACE}-${HC_NAME//./-}"
```

## Procedure

### 1. Confirm that the NodePool is stable

Check the NodePool's replica count and confirm that `spec.autoScaling` is not set. The output must show a replica
count and an empty `autoScaling=` value:

```bash
oc get nodepool -n "${HC_NAMESPACE}" "${NODEPOOL_NAME}" \
  -o jsonpath='replicas={.spec.replicas} autoScaling={.spec.autoScaling}{"\n"}'
```

Check that no rollout is in progress. The `UpdatingVersion`, `UpdatingConfig`, and `UpdatingPlatformMachineTemplate`
conditions must not be `True`:

```bash
oc get nodepool -n "${HC_NAMESPACE}" "${NODEPOOL_NAME}" \
  -o jsonpath='{range .status.conditions[*]}{.type}={.status}{"\n"}{end}' | grep '^Updating'
```

List the MachineSets in the hosted control plane namespace. Exactly one MachineSet for your NodePool should have a
non-zero `REPLICAS` value. The `NODEPOOL` column shows `<hosted_cluster_namespace>/<nodepool_name>`.

```bash
oc get machinesets.cluster.x-k8s.io -n "${HCP_NAMESPACE}" \
  -o custom-columns='NAME:.metadata.name,NODEPOOL:.spec.template.metadata.annotations.hypershift\.openshift\.io/nodePool,REPLICAS:.spec.replicas'
```

Wait until any rollout has completed before you continue. While a NodePool with the `Replace` upgrade type is rolling
out, its MachineDeployment has more than one MachineSet with Machines, and a scale-down is not guaranteed to reduce the
MachineSet that contains the annotated Machine. With the `OnDelete` strategy, the rollout only completes as the old
Machines are deleted, so this procedure is not available until all old Machines are gone. See
[NodePool Rollouts](../../reference/nodepool-rollouts.md) for how to monitor a rollout.

### 2. Identify the Machine to delete

List the Machines with their NodePool, Node name, phase, and deletion timestamp:

```bash
oc get machines.cluster.x-k8s.io -n "${HCP_NAMESPACE}" \
  -o custom-columns='NAME:.metadata.name,NODEPOOL:.metadata.annotations.hypershift\.openshift\.io/nodePool,NODE:.status.nodeRef.name,PHASE:.status.phase,DELETING:.metadata.deletionTimestamp'
```

Find the row whose `NODE` value is the hosted cluster Node that you want to remove, and whose `NODEPOOL` value is
`<hosted_cluster_namespace>/<nodepool_name>`. Note the Machine name.

Check that no Machine of the same NodePool has a value in the `DELETING` column. A Machine that is already being deleted
still counts toward the MachineSet's replicas and is always selected first. A scale-down could therefore be satisfied by
that Machine instead of the one you annotate. Wait for any such Machine to be removed before you continue.

Also check that the other Machines of the NodePool are healthy. Unhealthy Machines are also preferred for deletion, and
in Cluster API versions earlier than v1.9.0 they have the same priority as annotated Machines under the `Random` order.
Cluster API treats a Machine as unhealthy if it has no Node, if its Node's `Ready` condition is `False`, or if the Node
reports memory, disk, or PID pressure. Cluster API v1.7.4 and later also treat a Machine as unhealthy if a
MachineHealthCheck has set the Machine's `HealthCheckSucceeded` condition to `False`.

In the Machine list above, check that each Machine of the NodePool that you will keep has a value in the `NODE` column.
Then run the following command against the hosted cluster. For each of those Nodes, `READY` must be `True` and the three
pressure columns must be `False`:

```bash
oc --kubeconfig <hosted_cluster_kubeconfig> get nodes \
  -o custom-columns='NAME:.metadata.name,READY:.status.conditions[?(@.type=="Ready")].status,MEMORY_PRESSURE:.status.conditions[?(@.type=="MemoryPressure")].status,DISK_PRESSURE:.status.conditions[?(@.type=="DiskPressure")].status,PID_PRESSURE:.status.conditions[?(@.type=="PIDPressure")].status'
```

If the Cluster API version that manages your hosted cluster might be earlier than v1.9.0 (see
[Limitations](#limitations)), or you are not sure, also check the Machines that you will keep on the management cluster.
In those versions, Cluster API also treats a Machine as unhealthy if its `failureReason` or `failureMessage` is set, and
versions earlier than v1.7.4 also treat a `NodeHealthy` condition with the status `Unknown` as unhealthy. The following
command reads the `v1beta1` API version, which reports these fields. It can print a warning such as
`cluster.x-k8s.io/v1beta1 Machine is deprecated; use cluster.x-k8s.io/v1beta2 Machine`, which you can ignore:

```bash
oc get machines.v1beta1.cluster.x-k8s.io -n "${HCP_NAMESPACE}" \
  -o custom-columns='NAME:.metadata.name,NODEPOOL:.metadata.annotations.hypershift\.openshift\.io/nodePool,NODE:.status.nodeRef.name,FAILURE_REASON:.status.failureReason,FAILURE_MESSAGE:.status.failureMessage,NODE_HEALTHY:.status.conditions[?(@.type=="NodeHealthy")].status,HEALTH_CHECK:.status.conditions[?(@.type=="HealthCheckSucceeded")].status,DELETING:.metadata.deletionTimestamp'
```

For each Machine of the NodePool that you will keep: `NODE` must have a value; `FAILURE_REASON`, `FAILURE_MESSAGE`, and
`DELETING` must be `<none>`; `NODE_HEALTHY` must be `True`; and `HEALTH_CHECK` must not be `False`.

If any Machine that you will keep fails these checks, do not continue. If it is being deleted, wait until it is gone.
If it is unhealthy, wait until it recovers or, if `spec.management.autoRepair` is enabled on the NodePool, until it is
replaced. If it does not recover, fix that Machine or its Node first. Then repeat the checks in this step.

### 3. Annotate the Machines

Before you annotate, list the Machines that already have the `cluster.x-k8s.io/delete-machine` annotation, for example
from an earlier operation:

```bash
oc get machines.cluster.x-k8s.io -n "${HCP_NAMESPACE}" \
  -o custom-columns='NAME:.metadata.name,NODEPOOL:.metadata.annotations.hypershift\.openshift\.io/nodePool,DELETE-MACHINE:.metadata.annotations.cluster\.x-k8s\.io/delete-machine'
```

For the Machines of your NodePool, `<none>` in the `DELETE-MACHINE` column means that the annotation is absent. Any
other value, including an empty cell or `no`, means that the annotation is present and the Machine is prioritized for
deletion. If a Machine that you will keep has the annotation, find out why before you continue. Remove the annotation,
as shown in [Cleanup](#cleanup), only if it is left over from an operation that is no longer in progress. If you cannot
determine why it was set, stop: removing it could interfere with another operation, and keeping it could cause that
Machine to be deleted instead of a Machine that you want to remove.

Then annotate each Machine that you want to remove and that does not already have the annotation:

```bash
oc annotate machines.cluster.x-k8s.io -n "${HCP_NAMESPACE}" <machine_name> \
  cluster.x-k8s.io/delete-machine=yes
```

Before you scale down, run the listing command again. For the Machines of your NodePool, the `DELETE-MACHINE` column
must show a value other than `<none>` for exactly the Machines that you will remove. Also repeat the Machine and Node
health checks in [step 2](#2-identify-the-machine-to-delete).

!!! important

    Annotate the Machines before you lower the replica count. As soon as the MachineSet has more Machines than its
    desired replicas, the MachineSet controller selects Machines and starts deleting them. Annotating a Machine after
    that does not cancel deletions that have already started.

### 4. Scale down the NodePool

Lower `spec.replicas` on the NodePool by the number of Machines of your NodePool that have the annotation:

```bash
oc scale nodepool/"${NODEPOOL_NAME}" -n "${HC_NAMESPACE}" --replicas=<new_replica_count>
```

Scale the NodePool, not the MachineDeployment or MachineSet. The NodePool controller sets the replica count of the
MachineDeployment or MachineSet from `NodePool.spec.replicas` and overwrites changes made to those objects directly.

## Verification

Check the Machines in the hosted control plane namespace. The annotated Machine gets a deletion timestamp, its Node is
drained, and the Machine is then removed:

```bash
oc get machines.cluster.x-k8s.io -n "${HCP_NAMESPACE}" \
  -o custom-columns='NAME:.metadata.name,NODEPOOL:.metadata.annotations.hypershift\.openshift\.io/nodePool,NODE:.status.nodeRef.name,PHASE:.status.phase,DELETING:.metadata.deletionTimestamp'
```

Confirm that the Machine with a value in the `DELETING` column is the one that you annotated. If a different Machine of
the NodePool is being deleted instead, for example because automatic repair started deleting an unhealthy Machine, that
Machine satisfied the scale-down. The annotated Machine keeps its annotation. See [Cleanup](#cleanup).

After the Machine is removed, its Node is normally no longer listed in the hosted cluster:

```bash
oc --kubeconfig <hosted_cluster_kubeconfig> get nodes
```

The NodePool's `Desired Nodes` and `Current Nodes` columns show the new replica count:

```bash
oc get nodepool -n "${HC_NAMESPACE}" "${NODEPOOL_NAME}"
```

If the Machine stays in the `Deleting` phase, the Node drain or volume detachment might be blocked, for example by a
PodDisruptionBudget. See [NodePool lifecycle](nodepool-lifecycle.md#scale-down) for the `nodeDrainTimeout` and
`nodeVolumeDetachTimeout` fields.

## Cleanup

An annotated Machine that is not deleted keeps its annotation and is preferred for deletion the next time its MachineSet
scales down. This happens if you annotated more Machines than you removed, or if you annotated a Machine and did not
scale down.

List the Machines that still have the annotation. Machines without it show `<none>`. An empty cell or any other value
means that the annotation is present:

```bash
oc get machines.cluster.x-k8s.io -n "${HCP_NAMESPACE}" \
  -o custom-columns='NAME:.metadata.name,DELETE-MACHINE:.metadata.annotations.cluster\.x-k8s\.io/delete-machine'
```

Remove the annotation from any Machine that you want to keep, if you set the annotation yourself or have confirmed that
it is left over from an operation that is no longer in progress:

```bash
oc annotate machines.cluster.x-k8s.io -n "${HCP_NAMESPACE}" <machine_name> \
  cluster.x-k8s.io/delete-machine-
```

## Limitations

- The annotation only influences which Machines are deleted when a MachineSet scales down. It does not create a
  scale-down. If you delete a Machine directly without lowering `spec.replicas`, the MachineSet creates a replacement
  Machine.
- If you lower `spec.replicas` by more than the number of annotated Machines, the remaining Machines are selected by
  the default `Random` order, which prefers unhealthy Machines first. If you annotate more Machines than you remove,
  the controller decides which of the annotated Machines are deleted.
- The annotation does not skip draining. Use the NodePool's `nodeDrainTimeout` and `nodeVolumeDetachTimeout` fields to
  bound how long a deletion can wait.
- The deletion priority is implemented by the Cluster API MachineSet controller that HyperShift runs for each hosted
  cluster, not by a HyperShift API. The priority described on this page matches Cluster API v1.9.0 and later. In the
  [openshift/cluster-api](https://github.com/openshift/cluster-api) repository, it is present in the `release-4.19` and
  later branches. In earlier versions, the default `Random` order gave annotated Machines the same priority as unhealthy
  Machines, so an unhealthy Machine in the same MachineSet could be deleted instead of an annotated one. If you are not
  sure which version manages your hosted cluster, make sure that the other Machines of the NodePool are healthy before
  you scale down, as described in [step 2](#2-identify-the-machine-to-delete).
