---
title: Distribute Hosted Cluster workloads
---

# Distribute Hosted Cluster workloads

HyperShift enables implementing the strategy to colocate and isolate pods for Hosted Clusters.
As a management cluster operator you can leverage the following Node labels and taints: 

`hypershift.openshift.io/control-plane: true`

`hypershift.openshift.io/cluster: ${HostedControlPlane Namespace}`

- Pods for a Hosted Cluster tolerate taints for control-plane and cluster.
- Pods for a Hosted Cluster prefer to be scheduled into the same Node.
- Pods for a Hosted Cluster prefer to be scheduled into control-plane Nodes.
- Pods for a Hosted Cluster prefer to be scheduled into their own cluster Nodes. 

If the `ControllerAvailabilityPolicy` is `HighlyAvailable` Pods for each Deployment within a Hosted Cluster will prefer to be scheduled across different failure domains.
