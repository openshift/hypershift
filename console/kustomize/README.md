# Console kustomize layers

Three layers, each building on the last:

| Layer | What it is | Builds standalone? |
|---|---|---|
| `origin/` | Verbatim upstream `openshift/console-operator` static assets for the `console` **and** `downloads` operands (deployment/service/route/pdb/serviceaccount each). Cited, unmodified, for diffing. | No — has operator-only placeholders (`${IMAGE}`, no volumes, no `spec.host`). |
| `hypershift/` | Generic, cluster-agnostic patch of `origin` for running the core console control-plane-side with **no console-operator** (Phase 1). Every patch is commented with *why* (what the operator would otherwise inject). | No — still has `REPLACE_*` placeholders. |
| `pat-console/` | Per-HostedCluster overlay: real namespace, hostnames, image digest, and guest token for the live `pat-console` cluster. | Yes — `./apply.sh` deploys it. |

Add a new per-cluster overlay by copying `pat-console/` and swapping its values; `hypershift/` should rarely need to change.

## Route ownership

CPO creates the console/downloads exposure Routes: per `endpointAccess` it
reconciles a public route, or a `-private` route (labeled
`route-visibility=private`) plus a matching ExternalName service for external-dns
under Private, deriving the host from the APIServer host (`api.<domain>` →
`console.<domain>` / `downloads.<domain>`). This tree manages the
Deployments/Services/PDBs/Secret; the console Deployment's `-base-address` (set
in the per-cluster overlay) must match CPO's derived host.

## Downloads operand (CLI download server)

The `downloads` operand serves the `oc`/CLI download page. Two Phase 1 notes:

- **TLS sidecar:** the HCP router is SNI passthrough only and can't do the
  upstream downloads Route's `edge` TLS termination, so the pod runs an
  `oauth-proxy` sidecar (auth bypassed via `-skip-auth-regex=^/`) that
  terminates TLS on 8443 and forwards to the verbatim upstream
  download-server on `127.0.0.1:8080`.
- **UI "Command Line Tools" link:** the page lists `ConsoleCLIDownloads` CRs
  read (browser-side) from the guest. The guest has the `Console` capability
  disabled, so the CRD isn't installed and nothing generates the CR. We supply
  both by hand in `../guest/` (the CRD + an `oc-cli-downloads` CR pointing at
  the downloads host). Open question — who installs the CRD and owns the CR
  long-term (normally the Console capability + console-operator): see
  `CONSOLE_CONTROL_PLANE_DOCS/CONSOLE_CONTROL_PLANE_STUDY.md` §22.

## `origin/` → `hypershift/` deltas (what we changed vs stock, and why)

Diffing against `origin/` makes every departure from upstream an explicit, reviewable patch.
The real deltas:

| Field | Upstream (`origin/`) | `hypershift/` | Why |
|---|---|---|---|
| `priorityClassName` | `system-cluster-critical` | `hypershift-control-plane` | Guest-cluster default; the pod now runs on the management cluster (matches every other HCP-ns operator Deployment). |
| `nodeSelector`/`tolerations` (`node-role.kubernetes.io/master`) | present | removed | Guest-master concept; meaningless on the management cluster. |
| `serviceAccountName`/`serviceAccount` | `console` | removed (`automountServiceAccountToken: false`) | Bridge only uses off-cluster mode (static guest token); no mgmt-side SA identity needed. |
| Service `spec.ports[0].port` | `443` (→ `targetPort: 8443`) | `8443` | **Functional.** CPO router's console case hardcodes `DestinationPort: 8443` and dials `ClusterIP:8443` directly (`v2/router/config.go:136-138`); upstream `443` leaves nothing on 8443. |
| Service annotation `service.beta.openshift.io/serving-cert-secret-name` | present | removed | No service-ca operator on GKE. |
| Route `spec.host` | unset (guest admission fills default) | explicit | No route-admission for a hand-applied mgmt-cluster Route. |
| Route `spec.tls.termination` | `reencrypt`+`Redirect` | `passthrough`/`None` | Router is SNI-passthrough only; bridge terminates its own TLS. |
| `spec.replicas` | unset (operator computes) | `2` | No operator to compute it. |
| Container `command`/`args`, `env`, `volumeMounts`, `volumes` | operator-generated (`--config=console-config.yaml` + injected volumes) | static off-cluster CLI flags + `serving-cert`/`guest-ca` volumes | No console-operator to generate `console-config.yaml`/inject auth volumes. |

**Deliberately unchanged from `origin/`** (upstream choices that already fit): the restricted-PSA
`securityContext` (pod `runAsNonRoot`/`seccompProfile`, container `allowPrivilegeEscalation: false`/
`capabilities.drop: [ALL]`/`readOnlyRootFilesystem: true`), the `target.workload.openshift.io/management`
annotation, probes, ports, resources, and the `{app: console, component: ui}` selector.

Two gotchas hit during the restructure:
- **Deployment `spec.selector` is immutable** — adopting upstream's real `{app: console, component: ui}`
  selector required delete+recreate of the live Deployment (Service/PDB selector changes apply in place).
- **kustomize `images:` can't match `${IMAGE}`** — upstream's `image: ${IMAGE}` is the operator's own
  Go placeholder; kustomize parses `images:` targets as docker refs and silently no-ops on `${IMAGE}`.
  Fix: JSON6902-`replace` the field to a valid-looking placeholder (`REPLACE_CONSOLE_IMAGE_REGISTRY`)
  in `hypershift/`, then let the per-cluster `images:` transform target that.

See `CONSOLE_CONTROL_PLANE_DOCS/CONSOLE_CONTROL_PLANE_PROGRESS.md` (what was deployed) and `CONSOLE_CONTROL_PLANE_STUDY.md` (design) for what this implements. Upstream patch tracking: `CONSOLE_CONTROL_PLANE_DOCS/reference/UPSTREAM_PATCHES.md`.
