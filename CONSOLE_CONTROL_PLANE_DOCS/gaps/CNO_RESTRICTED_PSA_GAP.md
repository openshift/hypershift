# Upstream gap — CNO-managed network operands violate restricted PSA on SCC-less management clusters

**Status: worked around live (stopgap patch, will be reverted by CNO); needs an
upstream fix in `openshift/cluster-network-operator`.**

Discovered while scaling `pat-console`'s NodePool from 0 to 4 nodes to observe
real workloads: **no guest node ever became Ready.** The root cause is a
restricted-PodSecurity-Admission (PSA) violation in four CNO-managed Deployments
in the HCP namespace — the same class of problem ../CONSOLE_CONTROL_PLANE_STUDY.md §20 describes for the
console workloads, but hitting a different owner (CNO, not CPO).

This is *not* console-specific. It blocks **any** zero-SCC (e.g. GKE) HyperShift
guest from bringing up nodes, independent of the console spike. It is recorded
here because the spike surfaced it.

## Symptom chain

1. The four CNO Deployments (below) sit at `0/N` replicas —
   `FailedCreate` / `ReplicaFailure`, `ReplicaSet ... has timed out progressing`.
   Their pods are rejected at admission because the HCP namespace enforces
   `pod-security.kubernetes.io/enforce: restricted` (GKE has no
   SecurityContextConstraints, so HyperShift falls back to PSA — see ../CONSOLE_CONTROL_PLANE_STUDY.md
   §20).
2. `network-node-identity`'s **approver** container therefore never runs, so node
   client CSRs (`kubernetes.io/kube-apiserver-client`, `system:ovn-node:*`) stay
   **Pending** forever.
3. `ovnkube-controller` on every guest node crash-loops on
   `failed to start the node certificate manager: certificate was not signed`.
4. CNI never comes up → nodes stuck **NotReady**.

## Root cause

These four Deployments are **not** managed by CPO's `controlplane-component`
framework. They are rendered and reconciled **directly by
cluster-network-operator (CNO)** from CNO's own upstream Go templates/bindata, in
a child-object reconciliation loop separate from CPO.

CPO's GCP `EnforceRestrictedSecurityContextToContainers` fix (GCP-205, commit
`58b7b2f63c`) — which sets restricted-PSA-compliant container security contexts —
only applies to Deployments/StatefulSets built via that framework. It has no way
to touch objects CNO creates on its own.

CNO's upstream templates *partially* handle SCC-less management clusters: when CPO
sets `SetDefaultSecurityContext=true` (true here, because GKE has no SCC
capability), CPO passes `RUN_AS_USER=1001` to CNO via env var, and CNO's bindata
uses it to set **pod-level** `runAsUser` / `runAsNonRoot` /
`seccompProfile: RuntimeDefault` (upstream CNO PRs
[#2757](https://github.com/openshift/cluster-network-operator/pull/2757),
[#2780](https://github.com/openshift/cluster-network-operator/pull/2780)).

But CNO's templates never set the **container-level**
`allowPrivilegeEscalation: false` and `capabilities.drop: [ALL]` that restricted
PSA also requires — they leave `capabilities.drop: [NET_RAW]` only (a
baseline-level hardening leftover, not restricted). So the pods fail restricted
admission despite the pod-level fields.

## Workaround (preferred) — relax the HCP namespace to `baseline` PSA

Set on the HostedCluster:

```yaml
metadata:
  annotations:
    hypershift.openshift.io/pod-security-admission-label-override: baseline
```

The HyperShift operator applies this to the HCP namespace's
`pod-security.kubernetes.io/{enforce,audit,warn}` labels
(`hostedcluster_controller.go:1977-1981`,
`PodSecurityAdmissionLabelOverrideAnnotation` in
`api/hypershift/v1beta1/hostedcluster_types.go`). `baseline` admits the CNO
operands as-is — they already carry `capabilities.drop: [NET_RAW]`, which is
baseline-compliant — so no per-Deployment patching is needed and it **survives
reconciles** (the operator owns the namespace labels).

This is the workaround carried in `console/hostedcluster/hostedcluster.yaml`. It
is still a stopgap: it relaxes PSA for the *entire* HCP namespace (not just the
CNO operands), so the real per-container fix belongs upstream (below).

## Alternative stopgap — patch the Deployments directly (does not persist)

Strategic-merge patch by container name on each Deployment, in namespace
`clusters-pat-console-pat-console` on the management cluster
`gcp-hcp-int-mc-us-central1-yjiv`:

| Deployment | Containers patched | Change |
|------------|--------------------|--------|
| `network-node-identity` | init `hosted-cluster-kubecfg-setup`; main `webhook`, `approver`, `token-minter` | `securityContext: {allowPrivilegeEscalation: false, capabilities.drop: [ALL]}` (replacing `capabilities.drop: [NET_RAW]`) |
| `ovnkube-control-plane` | init `init-ip`; main `token-minter`, `ovnkube-control-plane`, `socks-proxy` | same |
| `multus-admission-controller` | init `hosted-cluster-kubecfg-setup`; main `hosted-cluster-token`, `multus-admission-controller` | same, **plus** pod-level `securityContext.runAsNonRoot: true` (this pod spec had only `runAsUser`/`seccompProfile`, missing the explicit `runAsNonRoot` PSA checks for) |
| `cloud-network-config-controller` | init `hosted-cluster-kubecfg-setup`; main `hosted-cluster-token`, `cloud-token` | same (the `controller` container was already compliant — CNO had set it correctly there, inconsistent with its siblings) |

**Result:** all four Deployments schedule; `network-node-identity`'s approver
runs; node CSRs are approved; nodes come Ready.

**Why it's only a stopgap:** CNO owns these Deployments and will overwrite the
patch on its next reconcile (unlike CPO-managed objects, they are not
label/annotation-guarded against external edits). This only unstuck the cluster.

## The real fix (upstream `openshift/cluster-network-operator`)

In the bindata templates for these four workloads — roughly
`bindata/network/node-identity/**/node-identity.yaml`,
`bindata/network/ovn-kubernetes/*ovnkube-control-plane*.yaml`,
`bindata/network/multus-admission-controller/admission-controller.yaml`, and the
cloud-network-config-controller manifest — when the `RunAsUser` /
HyperShift-managed-service code path is active, also set container-level
`allowPrivilegeEscalation: false` and `capabilities: {drop: [ALL]}` (not just the
pod-level `runAsUser`/`runAsNonRoot`/`seccompProfile` that #2757/#2780 added), and
add the missing pod-level `runAsNonRoot: true` on multus-admission-controller.

Gate it the same way #2757/#2780 gate the existing fields: only when running on a
management cluster without SCC / enforcing restricted PSA. This is the CNO
equivalent of the restricted-PSS work CPO already did for its own workloads
(GCP-205, `58b7b2f63c`) — CNO needs it for its self-managed operands.

## Tracking

Add a row to `../reference/UPSTREAM_PATCHES.md` and file a Jira + upstream CNO PR when this
moves past the stopgap. Cross-ref: ../CONSOLE_CONTROL_PLANE_STUDY.md §20 (restricted PSA in HCP
namespaces), GCP-205 (CPO's equivalent fix).
