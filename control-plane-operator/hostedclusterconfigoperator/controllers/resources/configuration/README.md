# Hosted cluster configuration

The configuration owner reconciles foundation and global configuration policy.
Shared manifest constructors and globalconfig helper APIs remain in their original packages.

Foundation `Reconcile` retains its four stages and narrow `ReconcileParams`.
Global objects and image policy use `ReconcileGlobal` and `ReconcileImagePolicy`.
`GlobalConfigParams` contains only the facts consumed by shared policy helpers;
its private HCP compatibility snapshot is copied and never acts as a controller context.
Proxy and observed configuration have independent inputs. Install metadata accepts
a lazy component-version accessor so existing installed versions do not require a lookup.
Callers of `ReconcileInstallMetadata` must provide a callable `componentVersions`
accessor whenever initial version population can occur. The lookup is deliberately
lazy: an existing `version` key avoids calling the accessor. A nil accessor is not
valid when that key is absent; no eager lookup or alternative nil/error policy is provided.

`HostedCluster` and `ControlPlane` are distinct adapters over existing clients and upsert.
They share only transport mechanics: resource selection and mutation stay in this package.
Observed Build/Project flow from the hosted cluster to owned control-plane ConfigMaps;
proxy CA data flows in the opposite direction, preserving existing cleanup semantics.

The root retains shared HCP, pull-secret and release loading, logging, error aggregation,
HCP reconciliation status, and these nonadjacent phase positions:

- Foundation CRDs, endpoints, admission policies, install metadata, alerts.
- Foundation ClusterVersion and ClusterOperators, global configuration, foundation namespaces, RBAC.
- Proxy user CA after pull-secret propagation and before OAuth CA in networking/secrets.
- Observed configuration after kubelet, storage, CSI and recycler resources in storage/misc.

Global ordering is Infrastructure, DNS, Image, Ingress, Network, TrustedCA, Proxy,
image policy, install-config YAML, CloudCredential, Authentication and APIServer.
Failures preserve existing wrappers and continuation; observed errors remain individual
entries in the root aggregate. Infrastructure status belongs to object policy, while
HCP status and watch registration stay in the root. No policy is added for unrelated domains.

Direct policy tests live here; root phase/error/status regression tests remain in resources.
Run the configuration package and the complete resources subtree with race detection.
