---
title: Hosted Clusters on OpenStack
---

This section of the Hypershift documentation contains pages that explain how Hosted Clusters
and Nodepools can be deployed on the [OpenStack](https://www.openstack.org) platform.

!!! warning
    OpenStack support within Hypershift is currently "dev-preview" and therefore is not
    yet intended for production use. However, it is possible to create and manage
    Hosted Clusters for development and testing purposes. It is expected to work
    as described in this documentation.

!!! note
    When you create a HostedCluster with the OpenStack platform, HyperShift will install the [CAPI Provider for OpenStack (CAPO)](https://github.com/kubernetes-sigs/cluster-api-provider-openstack).
    in the Hosted Control Plane (HCP) namespace.
    Upon scaling up a NodePool, a Machine will be created, and the CAPI provider will create the necessary resources in OpenStack.
    CAPO created OpenStack resources by leveraging [Gophercloud](https://github.com/gophercloud/gophercloud), the OpenStack SDK for Go.

Table of content:

- [Prerequisites](prerequisites.md)
- [Create a Hosted Cluster and a default Nodepool](hostedcluster.md)
- [Destroy a Hosted Cluster](destroy.md)

Advanced scenarios: additional Nodepools can be created with specific configurations:

- [Nodepools distributed over Nova availability zones](az.md)
- [Additional ports for Nodepools](additional-ports.md)
- [Node tuning, SR-IOV Network Operator](performance-tuning.md)
