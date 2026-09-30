# Study: Dual-Cluster Control-Plane Components — CNO as Precedent for Console-Operator

Status: draft / research notes. Not reviewed, not part of the codebase's design docs.

## Purpose

Console-operator today ships as a normal CVO-applied, guest-side-only
component: CVO (control-plane-side) applies its release manifests into the
guest cluster, and both the console-operator pod and its operand (console
Deployment, bridge pods, Routes, etc.) run entirely in the guest cluster.
HyperShift's only involvement is CVO selectively stripping one manifest
(`0000_50_console-operator_01-oauth.yaml`, conditionally, when OAuth is
disabled — `v2/cvo/deployment.go`).

A proposal exists (see untracked `../CONSOLE_CONTROL_PLANE_STUDY.md` /
`../gaps/CONSOLE_CONTROL_PLANE_GAPS.md` in repo root) to port console-operator to run
**control-plane-side** as a CPO v2 `ControlPlaneComponent`, with its operand
split: the bridge Deployment/Service/ConfigMap/Secret/PDB/ServiceAccount would
live in the **management** cluster's HCP namespace, while
Routes/OAuthClient/ConsolePlugin/`config.openshift.io` CRs remain reconciled
in the **guest** cluster.

This is not a new shape in HyperShift. The **Cluster Network Operator (CNO)**
already runs exactly this way today: one operator process, control-plane-side,
holding simultaneous clients to both the guest and management clusters,
placing some operands on management (ovnkube-control-plane,
network-node-identity, multus-admission-controller,
cloud-network-config-controller) and others on guest (ovnkube-node DaemonSet,
multus DaemonSet, CNI config). This study uses CNO as the concrete precedent
to evaluate the console port against — both the "why would you do this" and
the "how would you build it" questions.

Scope is intentionally narrowed to these two components. Other ported
components (ingress-operator, DNS operator, NTO, image-registry-operator,
etc.) use a simpler single-cluster shape — operator runs management-side but
holds only a guest-scoped kubeconfig, no management-side operand — and are out
of scope here since they aren't structurally comparable to what's being
proposed for console.

## Why go dual-cluster: bootstrap-ordering vs isolation

Two distinct motivations can justify pulling an operator control-plane-side
with a management-side operand. They are not mutually exclusive, but the
evidence for each differs in kind.

**Bootstrap-ordering (evidenced elsewhere, not CNO's driver).** Some
operators must act on cloud infrastructure before guest worker nodes exist
(e.g. DNS record creation for ingress, which needs an early cloud-API path
via konnectivity with `ConnectDirectlyToCloudAPIs: true` —
`support/controlplane-component/konnectivity-container.go:46-49`,
hypershift#1601). This is a real, code-evidenced reason to move an operator
control-plane-side, but it does not by itself explain why an operand would
need to *live* in the management namespace — it only explains why the
*operator* needs early access to cloud APIs. It is not CNO's or console's
driver: neither needs to act before guest workers exist.

**Isolation / availability (partially evidenced by CNO, not explicitly
documented anywhere).** No comment or doc in this repo or upstream CNO states
"we placed ovnkube-control-plane on the management cluster for SLO reasons."
But the topology itself is suggestive: OVN-Kubernetes' control-plane pieces
(ovnkube-control-plane, network-node-identity) run in the HCP namespace,
co-located with etcd/KAS, rather than in the guest cluster alongside
ovnkube-node. This means:

- The network control-plane brain keeps running even if the guest cluster's
  nodes are unhealthy, being drained, or the guest API is under load — it
  isn't competing for scheduling/resources with guest workloads.
- A guest-cluster admin with only guest-KAS access cannot delete, scale down,
  or otherwise disrupt ovnkube-control-plane — it sits under
  management-cluster RBAC, out of guest-admin reach.
- Monitoring/alerting/lifecycle for the control-plane piece rides on the
  same management-cluster machinery (CPO rollout, mgmt Prometheus/Thanos)
  used for KAS/etcd/OAuth, rather than depending on the guest cluster's own
  monitoring stack being up.

This is the same argument raised for console: if console availability is
treated as part of a hosted cluster's advertised guarantees, keeping its
serving component (the bridge) on management insulates it from guest-cluster
disruption the same way CNO insulates OVN's control-plane brain. **This
remains an inference from topology, not a documented rationale** — worth
confirming with whoever owns the original OVN-Kubernetes/HyperShift design
(check `openshift/enhancements` for an OVN-on-Hypershift enhancement doc,
not yet located as part of this study) before treating it as settled
precedent.

## CNO's architecture in detail

### What CPO itself creates

`control-plane-operator/controllers/hostedcontrolplane/v2/cno/component.go:45-77`
— the CPO v2 `cno` component creates only the CNO Deployment itself plus its
RBAC in the HCP namespace: `serviceaccount.yaml`, `role.yaml` (adapted),
`rolebinding.yaml`, and (ARO-HCP only) `azure-secretprovider.yaml`. Full asset
dir `v2/assets/cluster-network-operator/` has exactly these 4 files — no
manifest for ovnkube-control-plane, network-node-identity,
multus-admission-controller, or cloud-network-config-controller anywhere in
the hypershift repo.

CPO does not create those other Deployments — it only **polls for them**
after the fact, via `WithCustomOperandsRolloutCheckFunc`
(`component.go:75,103-170`), fetching them from `cpContext.HCP.Namespace` to
confirm CNO rolled them out with the expected image. This is a readiness
gate, not a manifest apply — proof CNO itself, not CPO, is the thing creating
these objects.

### How CNO reaches both clusters

`v2/assets/cluster-network-operator/deployment.yaml:22-27` (args):

```yaml
args:
- start
- --listen=0.0.0.0:9104
- --kubeconfig=/configs/hosted            # primary client = GUEST cluster
- --namespace=openshift-network-operator
- --extra-clusters=management=/configs/management   # secondary client = MGMT cluster / HCP ns
```

Two kubeconfigs are assembled by an init container (`deployment.yaml:145-193`
region, `rewrite-config` style init):

- `/configs/hosted` → guest cluster, via the in-namespace `kube-apiserver`
  service (same in-cluster KAS trick used by KCM/scheduler/ingress-operator,
  since KAS runs in the same HCP namespace).
- `/configs/management` → the pod's own local KAS (management cluster),
  using its own projected SA token — no proxy needed, it's the mgmt cluster
  CNO's own pod is already running in.

Mode/env markers: `HYPERSHIFT=true` (`deployment.yaml:31`) flips CNO into
hosted-control-plane behavior; `HOSTED_CLUSTER_NAMESPACE`
(`deployment.yaml:52-56`, fieldRef `metadata.namespace`) tells CNO which
namespace IS its management-side target (its own HCP namespace).

Image split: `deployment.go:96` `OVN_CONTROL_PLANE_IMAGE` (from CPO's own
control-plane release payload, `ReleaseImageProvider`) vs `deployment.go:110`
`OVN_IMAGE` (from `UserReleaseImageProvider`, the guest/data-plane release) —
CPO hands CNO two separate images for the same OVN-Kubernetes component,
matching the control-plane/guest operand split.

### CNO's dual-cluster client is native, not bolted on

Verified against `openshift/cluster-network-operator` (cloned separately for
this study — **this is an upstream repo, not part of `hypershift`**, so
these paths do not exist in this checkout):

- `cmd/cluster-network-operator/main.go:78-80` — registers `--extra-clusters`
  as a `StringToString` flag (`name=kubeconfigPath`).
- `pkg/client/client.go:98-125` — `NewClient(...)` builds a
  `map[string]*OperatorClusterClient`, one entry per cluster: the in-cluster/
  guest default, plus one entry per `--extra-clusters` pair.
- `pkg/client/client.go:135-144` — `ClientFor(name)` returns the client for a
  given cluster name; `pkg/names/names.go:230-234` defines
  `ManagementClusterName = "management"` / `DefaultClusterName = "default"`.
- **Object-to-cluster routing is data, not code**: every rendered object
  carries an annotation `network.operator.openshift.io/cluster-name`
  (`pkg/names/names.go:103-104`). A single generic apply path
  (`pkg/apply/apply.go:38`) does
  `client.ClientFor(GetClusterName(obj)).…Apply(obj)`. Sending an object to
  management vs guest is an annotation on the rendered manifest, not a
  separate code path per controller.
- Hypershift-mode detection: `pkg/hypershift/hypershift.go:117` reads
  `HYPERSHIFT=true`.

This means CNO's multi-cluster support was a **design decision baked into its
client layer from early on** — every controller renders objects and tags
them with a cluster-name annotation; one generic apply function routes them.
There is no per-controller "if management then X, if guest then Y" branching
to replicate.

### Guest-copy cleanup

CVO's `manifestsToOmit` (`v2/cvo/deployment.go`) excludes only the
**ibm-cloud-managed** variant of CNO's own deployment manifest
(`0000_70_cluster-network-operator_03_deployment-ibm-cloud-managed.yaml`) —
it does not exclude the standard CNO deployment manifest from the guest
release payload for other platforms. This was **not independently confirmed
in this study** whether CNO's guest copy is (a) never applied to the guest
for non-IBM platforms for some other reason, or (b) applied and then
self-removed at runtime by CNO itself. An earlier pass claimed an init
container performing `kubectl delete deployment network-operator` exists in
CNO's own deployment.yaml; this specific line reference was not re-verified
against the current asset file in this pass and should be treated as
**unconfirmed** until checked directly against
`v2/assets/cluster-network-operator/deployment.yaml`.

## Console-operator: what the port would need to replicate

Console-operator's current guest-only architecture is fundamentally
single-cluster: `~14 controllers` (approximate count from
`../CONSOLE_CONTROL_PLANE_STUDY.md` — not independently verified against
upstream `openshift/console-operator` source as part of this study), each
holding a typed client and calling `resourceapply.Apply*` directly against
one apiserver. There is no central apply function and no cluster-routing
layer anywhere in the console-operator codebase (confirmed absent:
`ClientFor`, `ApplyObject`, cluster-name annotation equivalents).

This is the key asymmetry versus CNO:

| | CNO | Console-operator (if ported) |
|---|---|---|
| Multi-cluster support | Native — designed in from the start | Absent — would be retrofitted |
| Apply path | One generic `apply.Apply(obj)`, routed by annotation | ~14 controllers, each calling typed `resourceapply.Apply*` directly |
| Client construction | `NewClient` builds a map of clients from `--extra-clusters` | Would need a second typed client set built and threaded through constructors |
| Object routing | Annotation on rendered object (`cluster-name`) | Would need equivalent per-object placement decision, likely hand-coded per controller since no generic apply exists to hang an annotation off of |
| Effort shape | N/A (already built) | Moderate mechanical refactor: build mgmt client, thread through ~14 controller constructors, sweep operand-namespace constants |

The deployment/topology/framework side of the console port is fully paved —
CPO's `component.NewDeploymentComponent`, `InjectServiceAccountKubeConfig`,
and (per CNO's pattern) a second kubeconfig via whatever mechanism replaces
`--extra-clusters` for a non-CNO binary — none of that needs inventing. The
actual code risk is entirely inside console-operator's own controller code,
which was not built with a routing abstraction the way CNO's was. See
`../CONSOLE_CONTROL_PLANE_STUDY.md` §14 and `../gaps/CONSOLE_CONTROL_PLANE_GAPS.md`
Gap 2 for the detailed controller-by-controller breakdown.

## Open questions

1. Is there a documented rationale (e.g. an `openshift/enhancements` doc for
   OVN-Kubernetes on HyperShift) that states isolation/SLO as the actual
   reason ovnkube-control-plane runs management-side? Not yet located in this
   study — currently an inference from topology only.
2. Is there an actual SLO/SLA commitment around console availability today
   that would justify the same treatment? Needs product/SRE input, not
   answerable from this repo alone.
3. Confirm the CNO guest-copy cleanup mechanism (CVO exclusion for
   ibm-cloud-managed only vs. runtime self-delete for other platforms) —
   directly affects whether an equivalent mechanism is needed for console's
   guest-shipped manifests once ported.
4. What would replace CNO's `--extra-clusters` flag idiom for
   console-operator, which doesn't have that flag today? Likely a second
   `InjectServiceAccountKubeConfig`-style mount plus a manually threaded
   `client.Client` for management, rather than a generic flag (console-operator
   isn't built to parse an equivalent flag).
5. Cost/benefit: console-operator's refactor is "moderate mechanical" per
   this study's read of the gaps doc — is that scoped correctly, or does
   the ~14-controller number and per-controller threading work hide more
   risk than a first pass suggests?

## References

**This repo:**
- `control-plane-operator/controllers/hostedcontrolplane/v2/cno/component.go` — CPO's CNO component: what it creates (Deployment + RBAC only) vs what it polls for (`WithCustomOperandsRolloutCheckFunc`)
- `control-plane-operator/controllers/hostedcontrolplane/v2/assets/cluster-network-operator/deployment.yaml` — dual-kubeconfig args, `HYPERSHIFT`/`HOSTED_CLUSTER_NAMESPACE` env, init containers
- `control-plane-operator/controllers/hostedcontrolplane/v2/cno/deployment.go` — `OVN_CONTROL_PLANE_IMAGE` vs `OVN_IMAGE` split
- `control-plane-operator/controllers/hostedcontrolplane/v2/cvo/deployment.go` — `manifestsToOmit`, console-operator oauth-manifest conditional strip, CNO ibm-cloud-managed exclusion
- `support/controlplane-component/konnectivity-container.go:46-49` — bootstrap-ordering rationale (ingress-operator, for contrast only)
- `docs/content/reference/konnectivity.md:32-37` — diagram showing CNO and ovnkube-control-plane as distinct peer pods in the HCP namespace
- `docs/content/how-to/pki/control-plane-certificates.md:53` — multus/network-node-identity/ovn-metrics certs signed as control-plane components
- Untracked drafts in repo root (not part of codebase): `../CONSOLE_CONTROL_PLANE_STUDY.md`, `../gaps/CONSOLE_CONTROL_PLANE_GAPS.md` — the console port proposal this study evaluates

**Upstream (cloned separately for verification, not present in this repo):**
- `openshift/cluster-network-operator`: `cmd/cluster-network-operator/main.go:78-80`, `pkg/client/client.go:98-144`, `pkg/names/names.go:103-104,230-234`, `pkg/apply/apply.go:38`, `pkg/hypershift/hypershift.go:117`
