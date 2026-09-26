# Console control-plane-side — progress log (what we did)

Running log of the console-on-the-control-plane study for HyperShift/GCP HCP: the big steps we
followed, the changes actually made, and the PR/Jira refs. Active/ongoing work and future needs live
in `CONSOLE_CONTROL_PLANE_PHASE3_PLAN.md`; the design and file:line references live in
`CONSOLE_CONTROL_PLANE_STUDY.md`; upstream patch/PR/Jira tracking lives in `reference/UPSTREAM_PATCHES.md`.

Target cluster for all live validation: HostedCluster **`pat-console`** on GCP HCP. Phases 1–2 were
validated on the shared integration MC `gcp-hcp-int-mc-us-central1-yjiv`; Phase 3 on a dedicated dev
MC `dev-mgt-us-c1-p0917` (dev-patmarti), reusing the same customer project (`patmarti-hcp-test`) and
infraID (`patmart-b3bb`).

## Big-step arc

1. **Study** — feasibility of running the console bridge control-plane-side (in the HCP namespace on
   the management cluster) instead of in the guest. Verdict: viable; core console needs no code
   change, plugins/monitoring need a guest-network tunnel. → `CONSOLE_CONTROL_PLANE_STUDY.md`.
2. **Phase 1 — core console (DONE).** Bridge deployed control-plane-side, exposed Public + Private,
   per-user Google OIDC login, guest resource browsing, CLI-downloads.
3. **Phase 2 — day-0 UX (DONE).** Pod terminal + monitoring (Observe → Metrics) via a konnectivity
   socks5 tunnel to guest Thanos/Alertmanager.
4. **Phase 3 — dynamic plugins + Console capability guest-side (CLOSED — verified live).** Console
   capability enabled (Ingress disabled) via a GCP-gated CEL relax; console-operator stripped from
   the guest payload (GCP-gated); CMO ships the `monitoring-plugin`; the bridge loads it via a
   `-plugins` flag; Observe → Alerting/Dashboards/Targets render in the browser; multi-replica HA
   proven (OIDC refresh-token recovery). See the Phase 3 section below and
   `CONSOLE_CONTROL_PLANE_PHASE3_PLAN.md` for the design/analysis.
5. **Phase 4 — the console-operator's role (NEXT / active).** Define what a control-plane-side
   console-operator must own (all the injections/lifecycle we currently hand-roll) and decide **port
   vs. reimplement (e.g. in CPO)**, then build it. Seeded by the Phase 3 "Part C" analysis.

---

## Phase 1 — core console control-plane-side (DONE, verified live)

**What it proves:** the core OpenShift console runs in the HCP namespace on the management cluster,
reachable from a browser for both PublicAndPrivate and Private GCP clusters, browsing guest
resources through the in-namespace guest KAS (no konnectivity), on a zero-node guest and later on 4
workers.

**Data path:** browser → (public LB `:443` / Private PSC endpoint) → HCP HAProxy router (SNI
passthrough) → console Service (ClusterIP `:8443`) → console bridge pod (terminates TLS) →
off-cluster k8s proxy → guest KAS `kube-apiserver.<hcp-ns>.svc:6443`.

**Changes made:**
- **CPO router — generic labeled-Route backend.** The HCP HAProxy router only built backends for
  hardcoded route names; a hand-applied `console` Route was skipped. Added console/downloads router
  backend cases. This grew into CPO **owning** the console/downloads exposure Routes (+ `-private`
  variants + PSC ExternalName services for external-dns) — **Jira GCP-1202**, PR
  openshift/hypershift#9622 (draft/RFC). Detail: `reference/PRIVATE_ENDPOINT_ACCESS.md`, `reference/UPSTREAM_PATCHES.md`.
- **Console bridge `-ca-file` off-cluster TLS trust (patched image).** The off-cluster bridge
  couldn't verify the guest KAS private `root-ca` (only skip-verify worked). Fixed in three places
  (KAS resource proxy, anonymous transport, and — Phase 2 — `-service-ca-file` for service proxies).
  **Jira GCP-1219**, PR openshift/console#17185. Custom image `quay.io/patmarti/console:*` built by
  `console/build-console.sh` (branch `off-cluster-ca-file-trust`). Detail: `reference/UPSTREAM_PATCHES.md`.
- **Live manifests:** `console/kustomize/` (origin → hypershift → pat-console layers; see
  `console/kustomize/README.md` for the stock→ours delta table).
- **Per-user Google OIDC login** (upgraded from the initial `-user-auth=disabled` static token):
  bridge OIDC flags + the console client ID added to the guest KAS OIDC `audiences` +
  `email,profile` scopes + a session-key Secret — all hand-replicated (no console-operator). Runbook:
  `reference/GOOGLE_OIDC_CLIENT_SETUP.md`.

**Key findings (carried forward):**
- Guest KAS is a plain in-namespace ClusterIP — core console needs **no konnectivity**.
- An `oidcProviders[].oidcClients[]` entry is **not admissible** here (its admission needs
  `status.oidcClients`, which only a running guest console-operator writes) → we use audience +
  bridge flags only. A control-plane-side `status.oidcClients` owner is a future need.
- HCP namespaces enforce **restricted PSA**; the upstream console asset is already compliant.

**Known limitations carried out of Phase 1 (status updated in Phase 3):**
- Router config not hot-reloaded — manual router restart after applying a Route. *(Still open.)*
- **Multi-replica sessions** — **RESOLVED in Phase 3** (`replicas: 2`). For OIDC the bridge recovers
  a cross-pod session only from a refresh-token cookie; the Phase 3 bridge change
  (`access_type=offline` + `prompt=consent`, PR #17185) makes the provider issue one, so a request on
  any replica rebuilds the session via a silent refresh — no sticky routing needed (our
  SNI-passthrough router can't do affinity anyway). Proven live. Details in the Phase 3 section.
- ~~User-settings persistence~~ — **RESOLVED in Phase 3.** Enabling the Console capability installs the
  `openshift-console-user-settings` namespace + `console-user-settings-admin` RBAC, and the
  token-minter gives the bridge the `console` SA identity to use it (verified: per-user settings
  Roles/RoleBindings are created live).
- Everything hand-applied (no operator, no lifecycle) — the ownership of these injections is the
  Phase 4 (operator) question.

---

## Phase 2 — pod terminal + monitoring (DONE, verified live)

**Part A — pod terminal (DONE).** Works with **zero** new plumbing: the Pods-page terminal opens a
WebSocket to the k8s resource proxy (`/api/kubernetes/.../pods/<pod>/exec`) → guest KAS → kubelet,
riding the Phase 1 path. Verified: interactive shell in a guest pod via the UI, governed by the
logged-in OIDC user's guest RBAC.

**Part B — monitoring (DONE).** Observe → **Metrics** renders live (real graphs; PromQL `up` returns
data). Thanos/Alertmanager are guest ClusterIP services unreachable from the control-plane pod, so:

**Changes made:**
- **Konnectivity socks5 sidecar** on the console pod (tunnels the bridge's `HTTP(S)_PROXY` into the
  guest network, resolving guest Service → ClusterIP), plus bridge `-k8s-mode-off-cluster-thanos` /
  `-alertmanager` flags and `HTTP(S)_PROXY`/`NO_PROXY` env. Reused verbatim in Phase 3.
- **`-service-ca-file` off-cluster trust** (folded into the same GCP-1219 console PR): service
  proxies (Thanos/Alertmanager/terminal/plugins) present service-ca-signed certs, a different signer
  than the KAS CA. Detail: `reference/UPSTREAM_PATCHES.md`.
- **Guest VPC geneve firewall fix.** First live test got `504` because the guest VPC dropped OVN-K
  geneve (UDP 6081) between nodes, breaking all cross-node pod networking (and thus konnectivity to
  guest pods). Fixed with one INGRESS allow rule (`console/guest/allow-geneve-firewall.sh`).
  Productization = CPO owning the rule — **Jira GCP-1221** (later cherry-picked into this branch as
  the CPO firewall reconciler, commit `feat(gcp): GCP-1221 | manage worker firewall rule in CPO`).

**Key finding (shaped Phase 3):** Observe → **Alerting/Dashboards/Targets** is **not** core console —
it's the `monitoring-plugin` dynamic ConsolePlugin (shipped by CMO / the Console capability, neither
present here; `SERVER_FLAGS.consolePlugins` is `[]` live). The Alertmanager *backend* path is proven
(real alerts firing, seen via `oc exec`). Loading the plugin moved to Phase 3.

---

## Phase 3 — dynamic plugins + Console capability guest-side (CLOSED, verified live)

**What it proves:** with the guest **`Console` capability enabled** (and Ingress kept disabled) but
the **console-operator running nowhere**, a **dynamic ConsolePlugin** (`monitoring-plugin`) loads and
renders in the control-plane-side console — Observe → **Alerting / Dashboards / Targets** all render
in the browser. Validated live on the dev-patmarti MC.

**Changes made:**
- **GCP-gated CEL relax (API).** Upstream CEL forbids disabling Ingress unless Console is also
  disabled. Relaxed it to a spec-level rule guarded `self.platform.type == 'GCP'`, so GCP HCs may run
  **Console enabled + Ingress disabled** (console runs control-plane-side). `hostedcluster_types.go`
  + regenerated CRDs + an envtest case (AWS rejects, GCP accepts). Converges on upstream OCPBUGS-58422
  (console-operator #1182 merged; hypershift #8933 removes the rule for all platforms — replace the
  GCP-only relax when it merges). Tracker: `reference/UPSTREAM_PATCHES.md`.
- **GCP-gated console-operator strip (CPO).** Enabling the Console capability installs the whole
  Console payload incl. the operator Deployment + ClusterOperator, which we don't want running on the
  guest. CPO's CVO `preparePayloadScript` now strips
  `0000_50_console-operator_07-operator-ibm-cloud-managed.yaml` +
  `0000_50_console-operator_95-clusteroperator.yaml` (BOTH — the CO strip is required or guest CVO
  blocks on `ClusterOperatorNotAvailable`), gated to GCP. `v2/cvo/deployment.go` + test. Result:
  guest has the 8 Console CRDs + namespaces + RBAC, but **no console-operator** (verified live).
- **All-in-one dev image.** `console/Dockerfile.dev.fast` now builds the full binary set so ONE image
  serves both the **HyperShift operator** (deployed on the MC) and the **CPO override** on the HC.
  `console/build.sh` pushes `quay.io/patmarti/hypershift-console-control-plane:*`.
- **Plugin enablement via bridge flag.** The console-operator normally writes the enabled-plugin set
  into `console-config`; with no operator, the bridge gets
  `-plugins=monitoring-plugin=https://monitoring-plugin.openshift-monitoring.svc.cluster.local.:9443`
  directly. The plugin backend is a guest ClusterIP Service, so it reuses the Phase 2 konnectivity
  socks5 tunnel + `-service-ca-file` trust unchanged.
- **Bridge service-account identity (Dashboards RBAC fix).** With `-user-auth=oidc`, USER requests
  proxy with the logged-in user's token, but the bridge's OWN backend calls (Dashboards ConfigMaps in
  `openshift-config-managed`, plugin metrics) need a service account — otherwise `system:anonymous` →
  403 ("console service account cannot list resource"). Added a **token-minter native init-sidecar**
  (CPO `token-minter` subcommand) that creates the guest `console` SA (normally the operator's job),
  mints a KAS-audience token (`--token-audience=<HCP IssuerURL>`) to a shared in-memory file, and
  auto-refreshes it; the bridge reads it via
  `-k8s-mode-off-cluster-service-account-bearer-token-file`. The Console capability's own
  `ClusterRoleBinding/console` + `RoleBinding/console-configmap-reader` (shipped by CVO) already bind
  that SA, so Dashboards then returns 200. Shape mirrors the framework's
  `InjectTokenMinterContainer(KubeAPIServerToken)`; the konnectivity socks5 sidecar stays a regular
  container, matching `InjectKonnectivityContainer` (which never uses native sidecars).
- **Multi-replica sessions (`replicas: 2`) via OIDC refresh-token recovery.** The bridge names its
  OIDC session cookie per-pod (`SessionCookieName()` = `<cookie>-$POD_NAME`; the console-operator
  injects `POD_NAME` via the downward API — our overlay omitted it, now added). But POD_NAME alone
  caused a re-auth loop across replicas, because the OIDC path rebuilds a cross-pod session **only**
  from a refresh-token cookie and the bridge never requested one. Fix: the bridge now requests offline
  access for OIDC (`access_type=offline` + `prompt=consent`, openshift/console **PR #17185**; Google
  rejects the `offline_access` *scope*, so request params are the mechanism), so a request on any
  replica rebuilds the session via a silent back-channel refresh. No sticky routing needed — which
  matters because our SNI-passthrough router can't do cookie affinity and Service
  `sessionAffinity: ClientIP` only ever sees the router pod IP. Proven live (see the finding below).

**Key findings (carried forward):**
- **Capability enablement replaces the Phase-1 hand-rolled guest scaffolding.** CVO installs all 8
  Console CRDs (incl. `consoleplugins`, `consoleclidownloads`), namespaces, and RBAC — so
  `console/guest/consoleclidownloads-crd.yaml` was deleted. The `oc-cli-downloads` **CR** is still
  hand-applied (`console/guest/oc-cli-downloads.yaml`): the operator that normally creates it is
  stripped, and other CLI-download CRs (helm, netobserv) come from their own operators/CVO.
- **Plugin ownership holds:** CMO (the provider) ships the plugin **workload + `ConsolePlugin` CR**
  once the capability is on; only enablement/wiring (the operator's job) is replaced by our flag.
- **Cross-region OIDC works:** the HC keeps its issuerURL on the *previous* region's OIDC bucket
  (shared infraID / published JWKS); `serviceAccountSigningKey` makes the new MC's operator skip
  re-upload.
- **Multi-replica works (`replicas: 2`) — the investigation, in full.** Under `-user-auth=oidc` the
  bridge (`pkg/auth/oauth2/auth_oidc.go`) keeps login state **per-pod in memory** and recovers a
  session on another pod **only** from a **refresh-token cookie** (code comment: *"requires smart
  routing when running multiple backend instances"*; the OIDC path has no access-token
  recovery-cookie fallback — that exists only for the openshift-oauth path). Two problems compounded:
  (1) the bridge never requested a refresh token, and (2) `POD_NAME` (correctly) makes each pod expire
  the others' session cookie, so every cross-pod hop forced a re-login → **endless re-auth loop**.
  This is worse in our topology because normal clusters get **cookie session affinity** from the
  edge/reencrypt Route's HAProxy, whereas our HCP router is **SNI-passthrough** (can't), and Service
  `sessionAffinity: ClientIP` is useless (the router dials the Service fresh, so it only ever sees the
  **router pod IP**, public or private). **Fix:** make the bridge request offline access for OIDC
  (`access_type=offline` + `prompt=consent`, PR #17185) so the provider returns a refresh token
  (Google rejects the `offline_access` *scope* — `invalid_scope`, confirmed live — hence request
  params). Now a request on any replica rebuilds the session via a **silent back-channel refresh**;
  `POD_NAME` keeps per-pod cookies clean. **Proven live:** with 2 replicas, deleting the
  session-holding pod (and even a freshly-created replacement pod) kept the user logged in with **zero
  `session was not found`** — before the change the same setup looped continuously.

**Remaining (open, benign / deferred to Phase 4):**
- **Login-role metric unavailable under OIDC:** `auth.metrics isKubeAdmin` queries the OpenShift User
  API (`users.user.openshift.io`), which doesn't exist under pure Google OIDC → a harmless
  login-metrics-only error (upstream-acknowledged `// FIXME`). No functional impact.
- **Router config not hot-reloaded** (Phase 1) — manual router restart after applying a Route.
- **Phase 4 — the console-operator's role.** Everything the operator normally injects/owns we
  currently hand-roll (plugin enablement, `POD_NAME`, the console SA + token, `status.oidcClients`,
  user-settings RBAC, secret injection). Phase 4 defines that role and decides **port the
  console-operator vs. reimplement (e.g. in CPO)** — see `CONSOLE_CONTROL_PLANE_PHASE3_PLAN.md`
  "Part C" (the analysis that seeds Phase 4).

---

## Cross-cutting infra gap (open) — CNO restricted PSA

Independent of console: cluster-network-operator's self-managed network operands set only pod-level
security fields and fail restricted-PSA admission on SCC-less (GKE) management clusters, blocking
node bring-up. Worked around live with `pod-security-admission-label-override: baseline` on the HC.
Needs an upstream CNO fix. Full analysis: `gaps/CNO_RESTRICTED_PSA_GAP.md`; tracker: `reference/UPSTREAM_PATCHES.md`.
