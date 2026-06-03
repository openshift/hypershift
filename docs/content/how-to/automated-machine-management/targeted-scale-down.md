---
title: Targeted node scale-down for NodePools
---

# Targeted node scale-down for NodePools

By default, when a NodePool is scaled down, CAPI selects machines for deletion based on creation timestamp (newest first). This page explains how to target specific nodes for removal, giving you control over which machines are deleted during a scale-down operation.

## How it works

You annotate one or more Nodes in the hosted cluster with `hypershift.openshift.io/scale-down=true`. The HCCO Node Controller detects this annotation and sets `cluster.x-k8s.io/delete-machine=yes` on the corresponding Machine in the management cluster. When a scale-down occurs (either by reducing `spec.replicas` on the NodePool or via the cluster autoscaler), CAPI prioritizes machines with the `delete-machine` annotation for deletion.

!!! important

    This annotation is a **deletion-priority hint**, not a trigger. Annotating a Node does **not** cause it to be deleted immediately. The machine is only removed when a scale-down actually occurs.

## Usage

### Marking a node for preferential deletion

```bash
oc --kubeconfig <hosted-cluster-kubeconfig> annotate node <node-name> hypershift.openshift.io/scale-down=true
```

Then scale down the NodePool:

```bash
oc -n <nodepool-namespace> scale nodepool <nodepool-name> --replicas=<current - 1>
```

The annotated node will be selected for deletion before any non-annotated nodes.

### Changing your mind

If you annotated a node but decide you no longer want it removed, simply remove the annotation **before** the scale-down completes:

```bash
oc --kubeconfig <hosted-cluster-kubeconfig> annotate node <node-name> hypershift.openshift.io/scale-down-
```

The controller will remove the `delete-machine` annotation from the corresponding Machine, returning it to normal deletion priority.

### Marking multiple nodes

You can annotate multiple nodes simultaneously. When a scale-down occurs, CAPI will prioritize all annotated machines for deletion. If the scale-down removes fewer machines than are annotated, CAPI selects among the annotated machines using its default ordering (newest first).

```bash
oc --kubeconfig <hosted-cluster-kubeconfig> annotate node node-1 node-2 hypershift.openshift.io/scale-down=true
```

## Important details

- **Value must be exactly `true`**: The annotation value is case-sensitive. Values like `"True"`, `"yes"`, `"1"`, or `"false"` will **not** activate the feature.
- **Works with all platforms**: This feature operates at the CAPI layer and works with AWS, Azure, KubeVirt, Agent, OpenStack, and IBM Cloud.
- **Works with autoscaling**: When the cluster autoscaler decides to shrink the pool, annotated machines are prioritized for deletion.
- **Node draining is respected**: The standard CAPI drain process (including `NodeDrainTimeout`) applies to annotated machines just like any other.
- **Unidirectional sync**: The controller syncs from Node to Machine only. If `delete-machine` is set directly on a Machine without the corresponding Node annotation, the controller will remove it on the next reconciliation cycle.
