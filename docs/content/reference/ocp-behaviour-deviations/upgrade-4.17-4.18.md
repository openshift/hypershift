# Upgrade Hypershift NodePool from 4.17 to 4.18

This section outlines the behavior and key differences between a Hosted Control Plane and a standalone OCP environment during an upgrade from version 4.17 to 4.18.

## Runtime Migration from `runc` to `crun`

In OpenShift 4.18, the MachineConfig Operator sets `crun` as the default runtime for worker nodes. This change also affects Hosted Control Planes, requiring us to assess and determine the best course of action, considering that OpenShift has some key differences in behavior.

### Expected Behavior for OCP Standalone Upgrade to 4.18

The OpenShift team has established rules for this runtime migration, affecting both control planes and worker nodes:

- A new OCP cluster deployed with the 4.18 release will use `crun` as the default runtime.
- New nodes created in 4.18 will use `crun` by default.
- Existing nodes from 4.17 **will not automatically migrate** from `runc` to `crun` in 4.18 unless additional actions are taken by the customer.

### Expected Behavior for Hosted Control Planes Upgrade to 4.18

The Hypershift team has chosen a different approach from standalone OCP due to certain design decisions:

- A new Hosted Cluster from the 4.18 release will use `crun` as the default runtime.
- New NodePools in 4.18 will also use `crun` as the default runtime.
- Existing NodePools from 4.17 **will migrate** from `runc` to `crun` once they are upgraded to 4.18.

### Detailed Overview

#### Why

##### Context

In Hosted Control Planes, there are two different upgrade strategies: **Replace** and **InPlace**. The documentation can be found [here](../../how-to/automated-machine-management/nodepool-lifecycle.md), and these strategies operate differently at the code level.

The **Replace** strategy creates new instances with the new version while removing old nodes in a rolling fashion. Internally, the NodePool controller will receive the new payload to roll out new nodes, which will eventually replace the current nodes once they join the NodePool. These new nodes will use `crun` as the default runtime, as the payload comes directly from the new version.

The **InPlace** strategy performs updates to the operating system of existing instances. Internally, the Hosted Cluster Config Operator (HCCO) will run a pod to generate new ignition payloads, which will then be applied to reignite the current nodes. The worker nodes (usually one at a time, depending on the `maxUnavailable` and `maxSurge` settings) will be cordoned, drained, and rebooted. After reboot, these nodes will apply the new payload, load the new MCS templates, and use `crun` as the default container runtime.

##### Reasons

The rationale behind this design is as follows:

- Supporting retention of the `runc` runtime would introduce significant engineering complexity at an abstraction layer that would need to be maintained indefinitely.
- This would make it difficult to reproduce scenarios or bugs, as the state of long-running clusters would diverge significantly from newly created clusters, which presents a product management challenge.
- Extensive testing upstream and downstream within CRI-O and OpenShift has not indicated any significant risk associated with this change.


### Safest way to upgrade your HostedCluster

HyperShift allows customers to just leave their NodePools on 4.17, while creating a new NodePool on 4.18 to validate the workloads perform correctly with `crun` runtime. For that, the steps are clear:

- Upgrade your HostedCluster Control Plane to 4.18.Z. NodePools will still run 4.17.Z and runc. Workloads are not affected.
- Bring up a new small nodepool in 4.18, which will run `crun` as default runtime. Scale up the workload to spread the pods to the new nodepool.
- In the best case, all is fine and the nodepool with version 4.17.Z could be upgraded.
- In the worst case, you can report the issue as a bug to the team for the further investigation.
