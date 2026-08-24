# Control Plane Operator (CPO)

CPO manages the lifecycle of individual hosted control plane components (KAS, etcd, OAuth, routers, cloud controllers, etc.). It runs as a **per-HCP Deployment** in the HCP namespace on the management cluster — one CPO instance per hosted cluster. It currently manages ~40 control plane components.

## Delivery Lifecycle

CPO is **part of the OCP release payload** (`io.openshift.release.operator=true`). Its release lifecycle is different from the hypershift-operator:

- **hypershift-operator**: Released independently with HyperShift, deployed once on the management cluster
- **CPO**: Released with OCP, pulled from the control plane release image (`spec.controlPlaneReleaseImage`, falls back to `spec.releaseImage`). A different HCP version means a different CPO version.

The hypershift-operator creates the CPO Deployment when a HostedControlPlane is provisioned. CPO then watches the HCP CR in its namespace and reconciles all control plane components.

## Component Framework (v2)

All control plane component reconciliation **must** use the `support/controlplane-component` framework. Components live in `controllers/hostedcontrolplane/v2/{component-name}/` with manifests in `v2/assets/{component-name}/`.

Code that reconciles control plane resources outside this framework is **legacy** and should be migrated. The top-level `HostedControlPlaneReconciler` still handles infrastructure, PKI, and orchestration, but component-level work belongs in v2.

See `support/controlplane-component/AGENTS.md` and `support/controlplane-component/README.md` for the framework.

## HCCO (Hosted Cluster Config Operator)

HCCO is a **separate binary in the same image**, invoked as `control-plane-operator hosted-cluster-config-operator`. It runs as a Deployment in the control plane namespace, mainly reconciling guest-side resources: `openshift-config/pull-secret`, node configuration, in-place upgrades, global pull secret, draining, etc.

HCCO controllers are in `hostedclusterconfigoperator/controllers/`. They do **not** use the v2 component framework today — this is a migration opportunity.

## HostedControlPlane Status Patching

CPO, HCCO, and the hypershift-operator (karpenter) all write to the same
`HostedControlPlane.Status` concurrently. Never patch HCP status with a raw
`Status().Update()` or an unguarded `client.MergeFrom()` — both can silently
overwrite a concurrent writer's changes (`Update()` replaces the whole status
subresource; `MergeFrom()` without an optimistic lock lets a stale
`resourceVersion` succeed silently instead of conflicting).

Use `support/statuspatching` instead:

- `statuspatching.PatchStatus(ctx, c, obj, mutate)` — general case. Re-fetches
  the object, applies `mutate`, patches with `MergeFromWithOptimisticLock`, and
  retries automatically on conflict.
- `statuspatching.PatchStatusCondition(ctx, c, obj, conditions, condition)` —
  single-condition updates. Uses `meta.SetStatusCondition`'s own change
  detection to skip no-ops without a false positive from `LastTransitionTime`.

**The mutate callback must recompute from the object it's given, not replay a
value captured earlier.** `PatchStatus`/`PatchStatusCondition` re-fetch the
object before calling `mutate`, so if `mutate` just re-applies a value computed
before the call, a conflict-retry blindly overwrites whatever concurrent write
the re-fetch picked up — defeating the point of the optimistic lock. If the
desired value depends on `Spec` and needs an expensive computation done once
outside the callback (e.g. an AWS API call), guard against the spec changing
mid-flight by comparing a captured `Generation` inside the callback instead of
patching unconditionally. A value is only safe to blindly replay on retry if
it's derived from a live check with no other writer (e.g. a fresh guest-cluster
probe done right before patching), not from an earlier `Status` snapshot.

The `hcpstatuspatch` static analyzer (`hack/tools/hypershiftlinter`) flags
direct `Status().Update()`/unguarded `MergeFrom()` on `HostedControlPlane` —
see [CNTRLPLANE-3532](https://redhat.atlassian.net/browse/CNTRLPLANE-3532) for
migration status.

## Key Directories

| Directory | Purpose |
|-----------|---------|
| `controllers/hostedcontrolplane/` | Main HCP reconciler (legacy orchestration + v2 component dispatch) |
| `controllers/hostedcontrolplane/v2/` | v2 component implementations (~40 components) |
| `controllers/hostedcontrolplane/v2/assets/` | Embedded YAML manifests per component |
| `controllers/hostedcontrolplane/pki/` | Certificate and CA generation |
| `controllers/hostedcontrolplane/cloud/` | Cloud provider-specific reconciliation (AWS, Azure, OpenStack) |
| `hostedclusterconfigoperator/` | HCCO binary and its controllers (runs in guest cluster) |
| `controllers/awsprivatelink/` | AWS PrivateLink endpoint management |
| `controllers/azureprivatelinkservice/` | Azure Private Link Service management |
| `controllers/gcpprivateserviceconnect/` | GCP Private Service Connect management |
| `featuregates/` | Feature gate configuration |

## Configuration

CPO receives configuration through:

- **HostedControlPlane CR**: The primary input — spec fields drive all component reconciliation
- **Environment variables**: Set by hypershift-operator when creating the CPO Deployment (`MY_NAMESPACE`, `CONTROL_PLANE_OPERATOR_IMAGE`, `HOSTED_CLUSTER_CONFIG_OPERATOR_IMAGE`, `HYPERSHIFT_FEATURESET`)
- **Release image**: CPO extracts component images from the control plane release image

## Multi-Binary Image

The CPO image contains multiple binaries selected via argv[0] symlink naming:

- `control-plane-operator` — CPO itself
- `control-plane-pki-operator` — PKI/certificate operator
- `hosted-cluster-config-operator` — HCCO (also available as a subcommand)
