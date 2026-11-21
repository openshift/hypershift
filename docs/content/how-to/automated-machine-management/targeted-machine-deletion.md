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
- The `jq` command-line JSON processor, which the annotation and Machine count checks on this page use.

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

Note the `replicas` value. It is the NodePool's desired replica count, and it must not change until you scale down in
[step 4](#4-scale-down-the-nodepool). The checks in step 3 and step 4 compare the NodePool's `spec.replicas` with this
value. If another actor, such as another user or an automation tool, changes it during the procedure, stop and start
again from step 1.

Check that no rollout is in progress or has failed and that the NodePool conditions are current. The output must show
the NodePool `generation` and the `UpdatingVersion`, `UpdatingConfig`, and `UpdatingPlatformMachineTemplate`
conditions. None of these conditions can be `True`, and each condition's `observedGeneration` must equal `generation`.
A condition that is missing, or whose `observedGeneration` differs from `generation`, has not been updated for the
current NodePool spec, so do not continue until it is. If `UpdatingVersion` or `UpdatingConfig` has the reason
`InplaceUpgradeFailed`, an `InPlace` rollout failed on a Node and has not completed, even though the condition is
`False` and current. Its `message` names the Node. Do not continue until the failed rollout is resolved:

```bash
oc get nodepool -n "${HC_NAMESPACE}" "${NODEPOOL_NAME}" \
  -o jsonpath='generation={.metadata.generation}{"\n"}{range .status.conditions[*]}{.type}={.status} reason={.reason} observedGeneration={.observedGeneration} message={.message}{"\n"}{end}' \
  | grep -E '^(generation|Updating)'
```

List the MachineSets in the hosted control plane namespace. Exactly one MachineSet for your NodePool should have a
non-zero `REPLICAS` value. The `NODEPOOL` column shows `<hosted_cluster_namespace>/<nodepool_name>`. The `ACTUAL`,
`GENERATION`, and `OBSERVED` columns are used by the Machine count checks in [step 3](#3-annotate-the-machines) and
[step 4](#4-scale-down-the-nodepool).

```bash
oc get machinesets.cluster.x-k8s.io -n "${HCP_NAMESPACE}" \
  -o custom-columns='NAME:.metadata.name,NODEPOOL:.spec.template.metadata.annotations.hypershift\.openshift\.io/nodePool,REPLICAS:.spec.replicas,ACTUAL:.status.replicas,GENERATION:.metadata.generation,OBSERVED:.status.observedGeneration'
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
In those versions, Cluster API also treats a Machine as unhealthy if its `failureReason` or `failureMessage` is set
(the check remains through v1.10 and is removed in v1.11.0; from v1.9.0, annotated Machines outrank unhealthy ones), and
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

Before you annotate, check which Machines of the NodePool already have the `cluster.x-k8s.io/delete-machine`
annotation, for example from an earlier operation. The following inventory command prints each Machine of the NodePool
and whether the annotation key is `present` or `absent`. The output must list every Machine of the NodePool. If it
prints nothing, check the `HC_NAMESPACE`, `NODEPOOL_NAME`, and `HCP_NAMESPACE` variables:

```bash
oc get machines.cluster.x-k8s.io -n "${HCP_NAMESPACE}" -o json \
  | jq -r --arg nodepool "${HC_NAMESPACE}/${NODEPOOL_NAME}" '
      .items[]
      | (.metadata.annotations // {}) as $annotations
      | select($annotations["hypershift.openshift.io/nodePool"] == $nodepool)
      | [.metadata.name,
         (if ($annotations | has("cluster.x-k8s.io/delete-machine")) then "present" else "absent" end)]
      | @tsv'
```

The command checks whether the key exists instead of reading its value, because Cluster API prioritizes a Machine
whenever the key is present, whatever its value, including an empty value or `no`. Do not use a `custom-columns`
listing for this check: it prints `<none>` both when the annotation is absent and when its value is the literal string
`<none>`.

If a Machine that you will keep shows `present`, find out why before you continue. Remove the annotation,
as shown in [Cleanup](#cleanup), only if it is left over from an operation that is no longer in progress. If you cannot
determine why it was set, stop: removing it could interfere with another operation, and keeping it could cause that
Machine to be deleted instead of a Machine that you want to remove. After you remove a leftover annotation from a
Machine that you will keep, run the inventory command again, and use its new output, not the earlier one, for the
checks below that must pass before you annotate any Machine.

Immediately before you annotate any Machine, confirm that no scale-down is pending and that every Machine is accounted
for. The MachineSet controller reconciles a MachineSet when one of its Machines changes, including when you add an
annotation. Each time, it lists the Machines that match its selector, and if there are more of them than its desired
replicas, it selects as many Machines as exceed the desired replicas and deletes them. Annotated Machines are selected
first. If a scale-down is already pending when you annotate a Machine, for example because another actor lowered the
replica count and the controller has not yet selected a Machine, the controller can select the Machine that you
annotated for that scale-down. The checks in step 4 run after you annotate and cannot undo that selection.

Run the NodePool and MachineSet commands from [step 1](#1-confirm-that-the-nodepool-is-stable) again, and count the
Machines of the NodePool. Replace `<active_machineset_name>` with the name of the MachineSet for your NodePool that has
a non-zero `REPLICAS` value:

```bash
oc get machinesets.cluster.x-k8s.io,machines.cluster.x-k8s.io -n "${HCP_NAMESPACE}" -o json \
  | jq -r --arg nodepool "${HC_NAMESPACE}/${NODEPOOL_NAME}" --arg machineset "<active_machineset_name>" '
      def controlled_by($uid): any(.metadata.ownerReferences[]?; .controller == true and .kind == "MachineSet" and .uid == $uid);
      def selected($labels): (.metadata.labels // {}) as $l | all($labels | to_entries[]; $l[.key] == .value);
      ([.items[] | select(.kind == "MachineSet" and .metadata.name == $machineset)]
        | if length == 1 then .[0] else error("MachineSet \($machineset) not found") end) as $active
      | ($active
        | reduce (.spec.selector.matchExpressions // [])[] as $e
            ((.spec.selector.matchLabels // {}) + {"cluster.x-k8s.io/cluster-name": .spec.clusterName};
             if $e.operator == "In" and ($e.values | length) == 1 then . + {($e.key): $e.values[0]}
             else error("unsupported MachineSet selector expression for \($e.key)") end)) as $selector
      | .items |= map(select(.kind == "Machine"))
      | [.items[] | select(any(.metadata.ownerReferences[]?; .controller == true and .kind == "MachineSet" and .name == $machineset and .uid != $active.metadata.uid)) | .metadata.name] as $stale
      | if $stale != [] then error("stale owner reference to MachineSet \($machineset) on Machines: \($stale | join(", "))") else . end
      | (.items | map(select((.metadata.annotations // {}) | has("hypershift.openshift.io/nodePool") | not)) | length) as $unannotated
      | (.items | map(select(controlled_by($active.metadata.uid))) | length) as $owned
      | (.items | map(select(controlled_by($active.metadata.uid) and (selected($selector) | not))) | length) as $unselected
      | (.items | map(select(any(.metadata.ownerReferences[]?; .controller == true) | not)) | length) as $ownerless
      | [.items[] | select((.metadata.annotations // {})["hypershift.openshift.io/nodePool"] == $nodepool)]
      | (map(select(.metadata.deletionTimestamp != null)) | length) as $deleting
      | (map(select(controlled_by($active.metadata.uid) | not)) | length) as $notowned
      | "machines=\(length) deleting=\($deleting) not_owned=\($notowned) owned=\($owned) unselected=\($unselected) ownerless=\($ownerless) unannotated=\($unannotated)"'
```

In the MachineSet listing, `ACTUAL` (`status.replicas`) is the number of Machines that the MachineSet controller counted
in the most recent reconciliation that updated the MachineSet status, including Machines that are being deleted. It is
the same set of Machines that the controller uses to decide how many Machines to delete. `OBSERVED`
(`status.observedGeneration`) equal to `GENERATION` shows that the controller has processed the current MachineSet spec.
The `machines` count comes from the `hypershift.openshift.io/nodePool` annotation, so if `machines` differs from
`ACTUAL`, either the MachineSet status is not up to date yet, or the MachineSet counts a Machine that does not have your
NodePool's annotation value or does not count a Machine that has it. HyperShift creates the MachineSets in the hosted
control plane namespace from NodePools, directly for the `InPlace` upgrade type or through a MachineDeployment for the
`Replace` upgrade type, and their Machine templates carry this annotation, so every Machine in that namespace should
have it. `unannotated` counts the Machines in the namespace that do not, which the other counts cannot attribute to a
NodePool. The MachineSet controller sets itself as the controlling owner of each Machine that it creates or adopts, and
`not_owned` counts the Machines of your NodePool whose controlling owner is not the active MachineSet. When `not_owned`
is `0` and `machines` equals `ACTUAL`, the Machines of your NodePool are exactly the Machines that the MachineSet
counted in its most recent status update.

Because `ACTUAL` comes from the last status update, the command also checks the current Machines directly. `owned`
counts the Machines in the namespace whose controlling owner is the active MachineSet, whatever their annotations, and
`ownerless` counts the Machines that have no controlling owner. The MachineSet controller lists Machines by its label
selector, ignores Machines that another owner controls, and adopts matching Machines that have no controlling owner,
so a Machine that `ownerless` counts could be added to the MachineSet the next time that the controller reconciles it,
for example after you annotate a Machine or scale down.

The MachineSet controller identifies the Machines that it controls by the UID in their controlling owner reference, not
by the MachineSet name, so `owned` and `not_owned` compare that UID with the active MachineSet's `metadata.uid`. A
controlling owner reference that has the active MachineSet's name and a different UID refers to a MachineSet that no
longer exists, such as one that was deleted and recreated with the same name. The controller does not count, delete, or
adopt a Machine with such a reference, and the command stops with an error that names each one.

The MachineSet controller also does not count or delete a Machine that it controls but that does not match its
selector. `unselected` counts the Machines that `owned` counts and whose labels do not match the active MachineSet's
selector. The controller lists Machines by the labels in the MachineSet's `spec.selector.matchLabels`, the
`cluster.x-k8s.io/cluster-name` label with the MachineSet's `spec.clusterName` value, and each
`spec.selector.matchExpressions` entry that uses the `In` operator with one value. The controller cannot list Machines
for a selector with any other `matchExpressions` entry, so the command stops with an error for one, as it does when it
cannot find the active MachineSet. When `unselected` and `ownerless` are `0`, the Machines that `owned` counts are
exactly the Machines that the MachineSet controller counts and selects from when it reconciles the MachineSet, including
after you annotate a Machine or scale down.

None of these counts depend on the `cluster.x-k8s.io/delete-machine` annotation. `unannotated` counts the Machines
that do not have the `hypershift.openshift.io/nodePool` annotation. The command prints the same counts before and after
you annotate, and only the inventory command checks the `cluster.x-k8s.io/delete-machine` annotation.

Annotate a Machine only if all of the following are true:

- The NodePool's `spec.replicas` is still the value that you noted in step 1.
- The step 1 checks still pass: `autoScaling` is empty, all three `Updating` conditions are present and not `True`,
  each condition's `observedGeneration` equals the NodePool `generation`, and neither `UpdatingVersion` nor
  `UpdatingConfig` has the reason `InplaceUpgradeFailed`.
- Exactly one MachineSet for your NodePool has a non-zero `REPLICAS` value, and every other MachineSet for your
  NodePool shows `0` in both `REPLICAS` and `ACTUAL`.
- For that MachineSet, `OBSERVED` equals `GENERATION`.
- The NodePool's `spec.replicas`, that MachineSet's `REPLICAS` and `ACTUAL` values, `machines`, and `owned` are all
  equal.
- `deleting` is `0`.
- `not_owned` is `0`.
- `unselected` is `0`.
- `ownerless` is `0`.
- `unannotated` is `0`.
- The inventory command shows `absent` for every Machine of the NodePool that you will keep.

You have not annotated any Machine yet, so do not expect `present` for the Machines that you will remove. One of them
shows `present` only if the annotation is left over from an earlier operation, as described above, or you set it in an
earlier attempt at this procedure.

If any of these checks fails, or the count command prints an error instead of the counts, do not annotate any Machine.
If the NodePool's `spec.replicas` is no longer the value that you noted in step 1, another actor changed the desired
replica count. Find out why, and start again from step 1 after that change is complete. If another check fails, wait
until the NodePool and its MachineSet finish reconciling, and then start again from step 1. If `unselected`,
`ownerless`, or `unannotated` stays above `0`, or the command keeps reporting a stale owner reference, find out why
before you continue.

If all of these checks pass, annotate each Machine that you want to remove and that does not already have the
annotation:

```bash
oc annotate machines.cluster.x-k8s.io -n "${HCP_NAMESPACE}" <machine_name> \
  cluster.x-k8s.io/delete-machine=yes
```

Run the inventory command again. It must show `present` for exactly the Machines that you will remove and `absent` for
every other Machine of the NodePool. Step 4 repeats this check, the Machine count checks, and the Machine and Node
health checks in [step 2](#2-identify-the-machine-to-delete) immediately before you scale down.

!!! important

    Annotate the Machines before you lower the replica count. As soon as the MachineSet has more Machines than its
    desired replicas, the MachineSet controller selects Machines and starts deleting them. Annotating a Machine after
    that does not cancel deletions that have already started.

### 4. Scale down the NodePool

Immediately before you scale down, confirm that the NodePool is not already adding or removing Machines, that every
Machine is accounted for, and that exactly the Machines that you will remove have the annotation. When its desired
replicas drop, the MachineSet controller selects for deletion as many Machines as exceed the desired replicas, and
Machines that are already being deleted count toward that number. If the actual Machines do not match the desired
replicas, the scale-down might not remove exactly the Machines that you annotated.

Run the NodePool and MachineSet commands from [step 1](#1-confirm-that-the-nodepool-is-stable), and the count command
and the inventory command from [step 3](#3-annotate-the-machines), again. Replace `<active_machineset_name>` in the
count command as in step 3. Also repeat the Machine and Node health checks in
[step 2](#2-identify-the-machine-to-delete).

Continue only if all of the following are true:

- The NodePool's `spec.replicas` is still the value that you noted in step 1.
- The step 1 checks still pass: `autoScaling` is empty, all three `Updating` conditions are present and not `True`,
  each condition's `observedGeneration` equals the NodePool `generation`, and neither `UpdatingVersion` nor
  `UpdatingConfig` has the reason `InplaceUpgradeFailed`.
- Exactly one MachineSet for your NodePool has a non-zero `REPLICAS` value, and every other MachineSet for your
  NodePool shows `0` in both `REPLICAS` and `ACTUAL`.
- For that MachineSet, `OBSERVED` equals `GENERATION`.
- The NodePool's `spec.replicas`, that MachineSet's `REPLICAS` and `ACTUAL` values, `machines`, and `owned` are all
  equal.
- `deleting` is `0`.
- `not_owned` is `0`.
- `unselected` is `0`.
- `ownerless` is `0`.
- `unannotated` is `0`.
- The inventory command shows `present` for exactly the Machines that you will remove and `absent` for every other
  Machine of the NodePool.
- Every Machine of the NodePool that you will keep still passes the Machine and Node health checks in step 2.

If any of these checks fails, or the count command prints an error instead of the counts, stop. Do not scale down while
a count differs, a Machine is being deleted, or you cannot tell which MachineSet a Machine belongs to. Annotations that
you already set stay in place. If you do not continue the procedure, remove them as shown in [Cleanup](#cleanup).

- If the NodePool's `spec.replicas` is no longer the value that you noted in step 1, another actor changed the desired
  replica count while your annotations were in place. If the count was lowered, the MachineSet controller might
  already have selected an annotated Machine for deletion. Check the `DELETING` column as shown in
  [Verification](#verification). Find out why the count changed, and start again from step 1 after that change is
  complete.
- If another check fails, wait until the NodePool and its MachineSet finish reconciling, and then start again from
  step 1. If `unselected`, `ownerless`, or `unannotated` stays above `0`, or the command keeps reporting a stale owner
  reference, find out why before you continue.

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

To find the Machines of the NodePool that still have the annotation, run the inventory command from
[step 3](#3-annotate-the-machines). Machines that show `present` still have it.

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
