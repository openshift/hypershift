# Multi-platform support

A platform is an infrastructure environment where different HyperShift components can run enabling them to make a series of assumptions, e.g. AWS, Azure, Kubevirt, Agent, None. 

HyperShift provides semantics and support for platforms at different levels: HostedCluster, NodePools and management cluster.
This document outlines the support matrix that involved these three entities.

## Support level

- A HostedCluster and its NodePools must all have the same platform.
- A single management cluster might have multiple HostedClusters with different platforms each of them.
- Some HostedCluster features might dictate coupling with the management cluster, e.g. an AWS private HostedCluster requires an AWS management cluster.
- For cloud provider platforms e.g. AWS, Azure, etc. HostedClusters are only tested with the same management cluster platform or a provider-agnostic platform e.g. Kubevirt, Agent, None. Mixed cloud providers e.g AWS management cluster and Azure HostedCluster is a best effort support level.
- Non OCP management is a best effort support level. The HyperShift Operator will try to auto-discover the management clusters features it has available.
