# Upgrades

HyperShift enables the decoupling of upgrades between the Control Plane and Nodes.

These are two separate procedures that a [Cluster Service Provider](../reference/concepts-and-personas.md#personas) has the flexibility to manage as they better see fit.

Control Plane upgrades are driven by the HostedCluster.

Node upgrades are driven by NodePools.

For a cluster to keep fully operational during and upgrade process, Control Plane and Nodes upgrades need to be orchestrated while satisfying [Kubernetes version skew policy](https://kubernetes.io/releases/version-skew-policy/) at any time.

## HostedCluster

`.spec.release` dictates the version of the Control Plane.

The HostedCluster propagates the intended `.spec.release` to the `HostedControlPlane.spec.release` and runs the appropriate Control Plane Operator version.

The HostedControlPlane orchestrates the rollout of the new version of the Control Plane components along with any OCP component through the new version of the [CVO](https://github.com/openshift/cluster-version-operator).

## NodePools

`.spec.release` dictates the version of any particular NodePool.

A NodePool will perform a Replace/InPlace rolling upgrade according to `.spec.management.upgradeType`.
