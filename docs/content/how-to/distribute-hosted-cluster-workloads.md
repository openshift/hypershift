---
title: Distribute Hosted Cluster workloads
---

# Distribute Hosted Cluster workloads

## Topology

HyperShift enables implementing the strategy to colocate and isolate pods for Hosted Clusters.
As a management cluster operator you can leverage the following Node labels and taints:

`hypershift.openshift.io/control-plane: true`

`hypershift.openshift.io/cluster: ${HostedControlPlane Namespace}`

- Pods for a Hosted Cluster tolerate taints for "control-plane" and "cluster".
- Pods for a Hosted Cluster prefer to be scheduled into "control-plane" Nodes.
- Pods for a Hosted Cluster prefer to be scheduled into their own "cluster" Nodes.

In addition:

- Pods for a Hosted Cluster prefer to be scheduled into the same Node.
- If the `ControllerAvailabilityPolicy` is `HighlyAvailable` Pods for each Deployment within a Hosted Cluster will require to be scheduled across different failure domains by setting `topology.kubernetes.io/zone` as the topology key.
- A HostedCluster can require their Pods to be scheduled into particular Nodes by setting `HostedCluster.spec.nodeSelector`. E.g
```yaml
  spec:
    nodeSelector:
      role.kubernetes.io/infra: "" 
```

## Scheduling Topology Options 

Cluster Service Providers may choose how hosted control planes are isolated or co-located. The three different options are:

 - Shared Everything
 - Shared Nothing 
 - Dedicated Request Serving

These options can be seen as a spectrum of isolation. Shared Everything is the least isolated, Dedicated Request Serving (Shared Some) and then Shared Nothing being the most isolated option.

**NOTE**: Each hosted control plane can run Single Replica or Highly Available. If Highly Available, the control plane will be spread across failure domains via `topology.kubernetes.io/zone` as the topology key.

### Shared Everything

- All hosted control plane pods are scheduled to any node that can run hosted control plane workloads.
- Nodes can be allocated specifically for control plane workloads by tainting and labeling them with `hypershift.openshift.io/control-plane: true`.

![shared_everything_topology.png](..%2Fimages%2Fshared_everything_topology.png)

### Shared Nothing

- To confine nodes to a specific hosted cluster taint and label them with `hypershift.openshift.io/cluster` value.
- No other control plane pods will land on those nodes.

![shared_nothing_topology.png](..%2Fimages%2Fshared_nothing_topology.png)

### Dedicated Request Serving

- Two nodes in different zones are dedicated to a specific hosted cluster’s front end serving components.
- The rest of the hosted cluster’s control plane pods can co-exist with other clusters’ control plane pods running on shared nodes.
- When running a Highly Available control plane, there will only be 2 replicas of request serving workloads instead of 3.

**NOTE**: A HostedCluster must have:

- `hypershift.openshift.io/topology: dedicated-request-serving-components` annotation to honor dedicated serving content workloads affinity opinions.
-  nodeSelector set as `hypershift.openshift.io/control-plane: true` for it to be a hard requirement for workloads to be scheduled. Without it that label is a soft requirement meaning workloads will try to find any suitable node if there’s none with this label.

![dedicated_request_serving_topology.png](..%2Fimages%2Fdedicated_request_serving_topology.png)

## Priority

HyperShift leverages PriorityClasses for driving Priority and Preemption of their managed Pods.
It will install four priority classes in a management cluster with the following order of priority from highest to lowest:

- `hypershift-operator`: Hypershift operator pods
- `hypershift-etcd`: Pods for etcd.
- `hypershift-api-critical`: Pods that are required for API calls and resource admission to succeed. This includes pods like kube-apiserver, aggregated API servers, and webhooks.
- `hypershift-control-plane`: pods in the HyperShift Control Plane that are not API critical but still need elevated priority. E.g Cluster Version Operator.
