# Upgrades
HyperShift enables the decoupling of upgrades between the Control Plane and Nodes.

This allows there to be two separate procedures a [Cluster Service Provider](../reference/concepts-and-personas.md#personas) can take, giving them flexibility to manage the different components separately.

Control Plane upgrades are driven by the HostedCluster, while Node upgrades are driven by its respective NodePool. Both the HostedCluster and NodePool expose a `.release` field where the OCP release image can be specified.

For a cluster to keep fully operational during an upgrade process, Control Plane and Nodes upgrades need to be orchestrated while satisfying [Kubernetes version skew policy](https://kubernetes.io/releases/version-skew-policy/) at any time. The supported OCP versions are dictated by the running HyperShift Operator [see here](../reference/versioning-support.md) for more details on versioning.

Control Plane downgrades (moving `.spec.release` to an earlier version) are not supported. Once a HostedCluster has been upgraded, the change cannot be reversed.

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

### HAProxy 3.4 ingress prerequisites with split payloads

For hosted ingress, the guest payload selected by `HostedCluster.spec.release.image`
must provide a nonempty `haproxy-router-haproxy34` image reference, even when a
separate control-plane payload is used. The running ingress operator comes from
the control-plane payload's `cluster-ingress-operator` image and must support
selecting HAProxy 3.4. The guest payload's ingress-operator image supplies the
canary image, not the running operator, in this split-payload configuration.
A HAProxy 3.4 image present only in the control-plane payload is not a fallback.

The running Control Plane Operator (CPO) must also include the conditional HAProxy
3.4 image wiring. Normally, the CPO is selected from the `hypershift` image in
`spec.controlPlaneRelease.image`, or `spec.release.image` when no separate
control-plane release is specified. If the control-plane payload is pinned to a
CPO without this wiring, updating only the guest payload does not enable HAProxy
3.4; select a compatible control-plane payload containing the updated CPO as well.
If a CPO image override is configured, verify that the effective image contains
the wiring rather than relying only on the payload reference.

When the guest image reference is absent or empty, the CPO omits both the optional
environment variable and command mapping. HAProxy 3.2 remains the default; image
availability alone does not opt an IngressController into 3.4. Source support does
not identify the first published payload containing all prerequisites. Verify the
selected payloads and running components before opting in, and validate the
effective version, running router image and Route traffic on a hosted cluster.

## NodePools
`.spec.release` dictates the version of any particular NodePool.

A NodePool will perform a Replace/InPlace rolling upgrade according to `.spec.management.upgradeType`. See [NodePool Rollouts](../reference/nodepool-rollouts.md) for details on what triggers a rollout and how it is executed.
