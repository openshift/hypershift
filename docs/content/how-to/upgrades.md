# Upgrades
HyperShift enables the decoupling of upgrades between the Control Plane and Nodes.

This allows there to be two separate procedures a [Cluster Service Provider](../reference/concepts-and-personas.md#personas) can take, giving them flexibility to manage the different components separately.

Control Plane upgrades are driven by the HostedCluster, while Node upgrades are driven by its respective NodePool. Both the HostedCluster and NodePool expose a `.release` field where the OCP release image can be specified.

For a cluster to keep fully operational during an upgrade process, Control Plane and Nodes upgrades need to be orchestrated while satisfying [Kubernetes version skew policy](https://kubernetes.io/releases/version-skew-policy/) at any time. The supported OCP versions are dictated by the running HyperShift Operator [see here](../reference/versioning-support.md) for more details on versioning.

## HostedCluster
`.spec.release` dictates the version of the Control Plane.

The HostedCluster propagates the intended `.spec.release` to the `HostedControlPlane.spec.release` and runs the appropriate Control Plane Operator version.

The HostedControlPlane orchestrates the rollout of the new version of the Control Plane components along with any OCP component in the data plane through the new version of the [cluster version operator (CVO)](https://github.com/openshift/cluster-version-operator). This includes resources like:

- the CVO itself
- cluster network operator (CNO)
- cluster ingress operator
- manifests for the kube API-server (KAS), scheduler, and manager
- machine approver
- autoscaler
- infra resources needed to enable ingress for control plane endpoints (KAS, ignition, konnectivity, etc.)

The CVO also applies the payload for the `CLUSTER_PROFILE": "ibm-cloud-managed` into the hosted cluster.

Traditionally, in standalone OCP, the CVO has been the sole source of truth for upgrades. In HyperShift, the responsibility is currently split between CPO and CVO. This enabled the flexibility and speed the HyperShift project needed for the CPO to support the management/guest cluster topology and the multiple customizations needed for manifests that otherwise would have needed to be segregated in the payload.

HyperShift exposes available upgrades in HostedCluster.Status by bubbling up the status of the ClusterVersion resource inside a hosted cluster. This info is purely informational and doesn't determine upgradability, which is dictated by the `.spec.release` input in practice. This does result in the loss of some of the builtin features and guardrails from CVO like recommendations, allowed upgrade paths, risks, etc. However, this information is still available in the HostedCluster.Status field for consumers to read.

## NodePools
`.spec.release` dictates the version of any particular NodePool.

A NodePool will perform a Replace/InPlace rolling upgrade according to `.spec.management.upgradeType`. See [NodePool Upgrades](./automated-machine-management/nodepool-lifecycle.md#upgrades-and-data-propagation) for details.
