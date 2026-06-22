---
title: Disconnected Deployments
---

This section of the Hypershift documentation contains pages that explain how disconnected deployments work in the HostedControlPlane area.

## Understanding Disconnected Deployments

HostedControlPlane deployments in disconnected environments function somewhat differently than in a regular OCP.

We have two distinct environments:

- **ControlPlane**: Located in the Management Cluster, where the pods of the HostedControlPlane are running and managed by the ControlPlaneOperator.
- **DataPlane**: Located in the workers of the HostedCluster, where the workloads and some other pods run, all managed by the HostedClusterConfigOperator.

Depending on where the pods are running, they will be affected by the `IDMS`/`ICSP` created in the Management Cluster (Control Plane) or the `ImageContentSources` set in the HostedCluster manifest's spec field. The latter will be translated into an `IDMS` object on the HostedCluster side.

These are the relevant resources for disconnected deployments:

- [Configuring the Registry TLS Certificates](tls-certificates.md)
- [Configuring Disconnected HostedControlPlane Deployments](idms-icsp-for-management-clusters.md)
- [Configuring Disconnected Workloads in the HostedCluster through ImageContentSources](image-content-sources.md)
- [Disconnected Workarounds](disconnected-workarounds.md)
- [Known Issues](known-issues.md)