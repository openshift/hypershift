---
title: Distribute Hosted Cluster workloads
---

# Distribute Hosted Cluster workloads

## Topology

HyperShift enables implementing the strategy to colocate and isolate pods for Hosted Clusters.
As a management cluster operator you can leverage the following Node labels and taints: 

`hypershift.openshift.io/control-plane: true`

`hypershift.openshift.io/cluster: ${HostedControlPlane Namespace}`

- Pods for a Hosted Cluster tolerate taints for control-plane and cluster.
- Pods for a Hosted Cluster prefer to be scheduled into the same Node.
- Pods for a Hosted Cluster prefer to be scheduled into control-plane Nodes.
- Pods for a Hosted Cluster prefer to be scheduled into their own cluster Nodes. 

If the `ControllerAvailabilityPolicy` is `HighlyAvailable` Pods for each Deployment within a Hosted Cluster will require to be scheduled across different failure domains by setting `topology.kubernetes.io/zone` as the topology key.

## Priority

HyperShift leverages PriorityClasses for driving Priority and Preemption of their managed Pods.
It will install three high priority classes in a management cluster with the following order of priority from highest to lowest:

- `hypershift-etcd`: Pods for etcd.
- `hypershift-api-critical`: Pods that are required for API calls and resource admission to succeed. This includes pods like kube-apiserver, aggregated API servers, and webhooks.
- `hypershift-control-plane`: pods in the HyperShift Control Plane that are not API critical but still need elevated priority. E.g Cluster Version Operator.