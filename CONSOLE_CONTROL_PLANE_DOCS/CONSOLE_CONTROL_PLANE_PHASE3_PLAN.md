# Phase 3 Implementation Plan: Dynamic plugin support — load a ConsolePlugin control-plane-side

**Status: DONE — verified live on the dev-patmarti MC.** Console capability enabled (Ingress
disabled) via a GCP-gated CEL relax; console-operator stripped from the guest payload (GCP-gated);
CMO ships the `monitoring-plugin`; the bridge loads it via a `-plugins` flag; a token-minter sidecar
gives the bridge the guest `console` SA identity (fixing Dashboards RBAC); Observe →
**Alerting / Dashboards / Targets** all render in the browser. **What was actually delivered is
recorded in `CONSOLE_CONTROL_PLANE_PROGRESS.md` (Phase 3 section)** — this document remains the
design/analysis record (Parts A/B/C, the strip, the CEL relax, and the Part C operator analysis).
**Goal:** Prove a **dynamic ConsolePlugin** loads and renders in the control-plane-side
console on a HyperShift/GCP HostedCluster — plugin **assets** fetched from a **guest**
ClusterIP Service through the konnectivity tunnel, verified TLS, no console-operator.
**Test plugin:** `monitoring-plugin` (`github.com/openshift/monitoring-plugin`), which closes
the Observe → **Alerting / Dashboards / Targets** gap left open in Phase 2 (see
`CONSOLE_CONTROL_PLANE_PROGRESS.md`).

**Companions:** `CONSOLE_CONTROL_PLANE_PROGRESS.md` (what Phases 1–2 delivered — core console +
exposure, terminal + monitoring, incl. the konnectivity socks5 sidecar this phase reuses),
`CONSOLE_CONTROL_PLANE_STUDY.md` (§5.1 DNS/resolver,
§9.1 the asset-proxy fix, §14.5 plugin discovery, §13.10 konnectivity modes),
`reference/UPSTREAM_PATCHES.md` (fork/patch tracker). Artifacts live under `console/`.

---

## 1. What Phase 3 actually is (and is not)

Phase 1 proved the core console (browse, OIDC login, CLI-downloads, exposure). Phase 2 proved
the **guest service-network path** (konnectivity socks5 sidecar + `-service-ca-file` trust) with
Thanos/Alertmanager, and rendered Observe → **Metrics** (which is *core* console, not a plugin).

Phase 2 discovered that Observe → **Alerting/Dashboards/Targets** is **not** core console — it is
rendered by the `monitoring-plugin` **dynamic ConsolePlugin**, which is not deployed here because
this HyperShift setup has the guest `Console` capability **disabled** and no CMO-shipped
monitoring-plugin / console-operator (Phase 2, `CONSOLE_CONTROL_PLANE_PROGRESS.md`). So there is currently **no UI surface for
Alerting even though the Alertmanager backend is proven**.

**Phase 3 has three parts:**

- **Part A — load one dynamic plugin** (the runtime path). Thin, because Phase 2 already built the
  hard reusable piece (konnectivity socks5 + service-ca trust). One bridge code fix + config.
- **Part B — enable the `Console` capability guest-side** to get the CRDs / namespaces / RBAC /
  ClusterOperator scaffolding *properly* (instead of hand-applying it under `console/guest/`),
  while keeping the operator/operand **off** the guest. This replaces the Phase-1 hand-rolled guest
  scaffolding and cleans up `console/guest/`.
- **Part C — analyze what the (not-yet-running-anywhere) console-operator must do** to plumb this
  together — secret injection, plugin config generation, status writes, etc. This is an **analysis
  deliverable** in Phase 3 that feeds the operator phase (POC Track B / study §14); we do **not**
  build the operator here.

### 1.1 Plugin deployment ownership model (why the pieces split the way they do)

A dynamic ConsolePlugin has **three independent pieces**, owned by three different parties. The
console-operator **never deploys the plugin workload** — its role is discovery + wiring only.

| Piece | What | Runs where | Normal owner | In our topology |
|---|---|---|---|---|
| **Plugin workload** (Deployment + HTTPS Service w/ service-serving cert) | serves the plugin's JS bundle | **guest worker nodes** (guest pod/service network) | the plugin **provider** — its own operator / Helm chart / OLM; for `monitoring-plugin` it's **CMO** | must run guest-side (Part A §4); CMO may provide it once the capability is on (§10.2) |
| **`ConsolePlugin` CR** (registration: name → `svc.<ns>:<port>`) | declares the backend Service | guest (cluster-scoped) | same provider (CMO for monitoring-plugin) | CRD from Part B; CR from CMO or hand-applied (Part A §5) |
| **Enablement + bridge wiring** (`console.operator/cluster spec.plugins[]` → `console-config.yaml plugins:` → roll bridge) | turns the plugin on and tells the bridge its endpoint | control-plane (the bridge config) | **console-operator** | **replaced by our `-plugins` flag** (Part A §3) — the flag *is* the `console-config plugins:` map, set by hand |

Two consequences that shape Phase 3:
- **Plugins run on guest workers, not control-plane-side.** Only the *bridge* is control-plane-side;
  every plugin backend is a guest ClusterIP Service reached over konnectivity (the whole reason for
  the socks5 sidecar). So Phase 3 needs real worker nodes (Phase 2 already scaled to 4).
- **We must preserve the standard registration process** (`ConsolePlugin` CR + `spec.plugins`
  enablement) so existing plugin providers keep working unchanged and so the design stays coherent
  with other platforms. Our `-plugins` flag is a **spike shortcut** for the enablement/wiring leg
  only — it bypasses `spec.plugins[]` + the operator's config generation, **not** the CR/workload.
  The productization target (Part C §10.2) is the ported operator doing dynamic discovery off the
  guest `ConsolePlugin` CRs, so no provider has to change how it ships a plugin.

### 1.2 Part A work items

| # | Item | New in Phase 3? | Where |
|---|---|---|---|
| 1 | **Bridge code fix**: the plugin **asset** transport ignores `HTTP_PROXY` → can't reach guest services through socks5. Add `Proxy: http.ProxyFromEnvironment`. | **YES** (the one remaining bridge patch) | `openshift/console` `pkg/server/server.go:524` |
| 2 | **Plugin config**: pass one plugin `name→endpoint` to the bridge (`-plugins`). No operator/dynamic discovery. | **YES** (config only) | `console/kustomize/hypershift` |
| 3 | **Plugin backend on the guest**: deploy `monitoring-plugin` (Deployment/Service, guest `openshift-monitoring`). | **YES** (guest-side) | `console/guest/` |
| 4 | **ConsolePlugin CR** on the guest (CRD now comes from Part B). | **YES** (guest-side) | `console/guest/` |
| 5 | **Konnectivity socks5 sidecar** + `HTTP(S)_PROXY`/`NO_PROXY` env | **NO — reused verbatim from Phase 2** | already live in `console/kustomize/hypershift` |
| 6 | **`-service-ca-file` trust** for the asset transport (plugin assets are served with service-serving certs, same signer as Thanos) | **NO — reused from Phase 2** | already live (custom image + flag) |
| 7 | Geneve VPC firewall (cross-node pod networking) | **NO — reused from Phase 2** (GCP-1221) | `console/guest/allow-geneve-firewall.sh` |

**Explicitly OUT of Phase 3** (deferred to the operator/productization phase):
- **Dynamic plugin discovery** — watching guest `ConsolePlugin` CRs, regenerating `console-config`,
  rolling the bridge. That is console-operator logic (study §14.5). Here we wire **one** plugin by
  hand via `-plugins` to prove the runtime path.
- **Building** the console-operator split-cluster refactor + the CPO v2 component — Part C only
  *analyzes* these; the build is the operator phase (POC Track B/C; study §13/§14).
- The CVO placement-flag payload strip of the operator **workload** — Part B keeps the whole
  capability payload (including the operator Deployment) for now; stripping only the workload is the
  operator phase (study §18). See §11.3.

---

# Part A — Load one dynamic plugin (the runtime path)

## 2. The one bridge code change (the crux of Phase 3)

### 2.1 Root cause

The plugin **asset** handler builds a bespoke `http.Client` whose `http.Transport` sets **only**
`TLSClientConfig` and omits `Proxy`. A hand-built `http.Transport` does **not** honor
`HTTP_PROXY`/`HTTPS_PROXY` unless `Proxy` is explicitly set (unlike `http.DefaultTransport`). So
plugin **asset** fetches (and plugin **i18n** fetches, which share the same client) bypass the
konnectivity socks5 proxy and try to dial the guest `*.svc.cluster.local` name directly — which
the control-plane-side pod cannot resolve or route.

`_console-research/pkg/server/server.go:518-528` (verified, branch `off-cluster-ca-file-trust`,
the asset-proxy fix is **NOT yet applied**):

```go
// Plugins
pluginsHandler := plugins.NewPluginsHandler(
	&http.Client{
		Timeout:   120 * time.Second,
		Transport: &http.Transport{TLSClientConfig: s.PluginsProxyTLSConfig},   // <-- no Proxy
	},
	s.EnabledPlugins,
	s.PublicDir,
)
```

Contrast with the plugin **API** proxy (`/api/proxy/`), which already honors proxy env
(`pkg/proxy/proxy.go:59`, `Proxy: http.ProxyFromEnvironment`) and with the off-cluster k8s
resource proxy (`cmd/bridge/main.go`, `UseProxyFromEnvironment: true`). Only the **asset**
transport is the gap.

### 2.2 The fix

`pkg/server/server.go:524`:

```go
Transport: &http.Transport{
	Proxy:           http.ProxyFromEnvironment,
	TLSClientConfig: s.PluginsProxyTLSConfig,
},
```

- Covers **both** plugin assets (`/api/plugins/<name>/…`, `HandlePluginAssets`) and plugin i18n
  (`/locales/resource.json` for `plugin__*` namespaces, `HandleI18nResources`) — they share this
  one `pluginsHandler` client (`server.go:519`, `:530-532`, `:534-539`; `pkg/plugins/handlers.go:167`).
- **TLS is already correct** from Phase 2: `s.PluginsProxyTLSConfig` is set to the off-cluster
  `serviceProxyTLSConfig`, i.e. the `-service-ca-file` trust domain (`cmd/bridge/main.go:596`,
  `offClusterProxyTLSConfigs` `:839-863`). Plugin assets are served with **service-serving certs**
  (service-ca), the same signer as Thanos — so the Phase 2 `-service-ca-file` work already trusts
  them. No new TLS wiring.
- **Scope note (upstream-acceptable):** setting `Proxy: http.ProxyFromEnvironment` is inert in the
  common in-cluster deployment (no proxy env set there) and only activates off-cluster (or in-cluster
  behind a cluster-wide proxy, where it now matches the existing `/api/proxy` behavior). This is the
  **same idiom already used by** the plugin API proxy transport (`pkg/proxy/proxy.go:59`, `NewProxy`)
  and 6 other bridge HTTP clients (oauth2 `auth.go:183`, helm `repos.go:115`, knative `handler.go:253`,
  artifacthub, tekton-results, devconsole webhooks) — so it introduces no new proxy behavior class.
  This is the same one-line change the study called out (§9.1, §14.5).
  See §2.3 for the full three-environment verification (incl. the trailing-dot `NO_PROXY` nuance).

### 2.3 `NO_PROXY` is load-bearing — and inverted between in-cluster and off-cluster

The §2 change is only *safe in-cluster* and *useful off-cluster* because of `NO_PROXY`, and the
two deployments require **opposite** `NO_PROXY` contents for plugin service names. This is subtle
and must not be "normalized" later.

| | In-cluster (standard OCP) | Off-cluster (control-plane-side, this study) |
|---|---|---|
| Who sets proxy env | **console-operator**, from the cluster-wide `Proxy` config object — `deployment.go:502-526` (`setEnvironmentVariables`) copies `proxyConfig.Status.{HTTPProxy,HTTPSProxy,NoProxy}` onto the bridge container, **each only if non-empty** | **we** set it by hand (`hypershift/kustomization.yaml`) |
| `NO_PROXY` value | from `Proxy.Status.NoProxy` — CNO-computed (`.svc`, `.cluster.local`, service/pod CIDRs, …) | deliberately narrow: `kube-apiserver.<hcp-ns>.svc,localhost,127.0.0.1` — **no `.svc`/`.cluster.local` suffix** |
| Should plugin `*.svc.cluster.local.` be dialed direct? | **Yes** — locally routable, no need for a corporate egress proxy | **No** — the name only resolves/routes on the *guest* network; it **must** tunnel through the socks5 sidecar |
| Effect on plugin assets after §2 | **identical to the already-shipped `/api/proxy` plugin transport** (same `ProxyFromEnvironment`, same endpoint) — §2 introduces no new proxy behavior | proxy **used** (name does not match the narrow `NO_PROXY`) → socks5 → guest ✅ |

Verified 2026-09-17 with a standalone `http.ProxyFromEnvironment` harness across all cases.
Consequences:
- **In-cluster, no cluster-wide proxy** (common case): no proxy env ⇒ §2 is a pure no-op (`DIRECT`).
- **In-cluster, with a cluster-wide proxy**: §2 makes the asset transport behave **exactly like the
  already-shipped `/api/proxy` plugin transport** (`pkg/proxy/proxy.go:59`, `NewProxy`), which has
  used `ProxyFromEnvironment` against these same endpoints for years — so it adds no new regression
  class. **Trailing-dot nuance (verified):** the operator emits endpoints with a trailing dot
  (`…svc.cluster.local.`, `configmap.go:250,258`), and Go's `httpproxy` matches `NO_PROXY` on the
  trailing dot exactly — so `…svc.cluster.local.` does **not** match `NO_PROXY=.svc,.cluster.local`
  ⇒ such endpoints route through a corp proxy on **both** the API path (today) and the asset path
  (after §2). Pre-existing API-proxy behavior; §2 doesn't change the risk surface — but the naive
  "`.svc` in `NO_PROXY` always covers it" is not literally true.
- **Off-cluster**: our `NO_PROXY` is intentionally narrow — only the in-namespace guest KAS +
  localhost. It must **never** be broadened to `.svc`/`.cluster.local`, or all guest tunneling
  (plugins **and** the Phase 2 Thanos/Alertmanager path) would silently bypass socks5. Go's matching
  is suffix/domain-based, so `kube-apiserver.<hcp-ns>.svc` doesn't exempt other `*.<ns>.svc` names;
  and the trailing dot on our `-plugins` endpoint guarantees it never matches a dot-less `NO_PROXY`
  entry ⇒ always tunneled.

**Net:** the same one-line change is correct in both topologies, driven entirely by `NO_PROXY` —
inert/direct in-cluster, tunneled off-cluster. Upstream framing: honor proxy env on the plugin
**asset** transport, consistent with the existing `/api/proxy` transport (`proxy.go:59`) and 6 other
bridge HTTP clients; no new proxy behavior class.

### 2.5 Build & ship

Fold into the **existing** custom console image (branch `off-cluster-ca-file-trust`,
`console/build-console.sh` → `quay.io/patmarti/console:*`), which already carries the `-ca-file`
and `-service-ca-file` off-cluster fixes. Commit the asset-proxy fix on the same branch, rebuild,
bump the `images:` digest in `console/kustomize/pat-console/kustomization.yaml`.

**Upstream:** add to the existing PR series (openshift/console#17185 / Jira GCP-1219) or a sibling
PR; update `reference/UPSTREAM_PATCHES.md`. Same "off-cluster bridge fixes" family.

---

## 3. Plugin config (no operator)

Without a console-operator there is no dynamic discovery / `console-config.yaml` generation. Wire
**one** plugin by hand. Two equivalent mechanisms; use the CLI flag to match the existing
flags-only Deployment:

`-plugins=<name>=<https-endpoint>` (repeatable / comma-separated; `MultiKeyValue`,
`cmd/bridge/main.go:154-158`, `pkg/serverconfig/config.go:21-67`). For monitoring-plugin:

```
-plugins=monitoring-plugin=https://monitoring-plugin.openshift-monitoring.svc.cluster.local.:9443
```

Notes:
- Endpoint shape must match what the operator would generate:
  `https://<svc>.<ns>.svc.cluster.local.:<port><basePath>` (study §14.5, `getServiceURL`
  `configmap.go:255-262`). The trailing dot on `.svc.cluster.local.` matches upstream output.
- The socks5 resolver splits `<name>.<namespace>` and does a guest `GET service` →
  `Spec.ClusterIP` (resolver **step 2**, study §5.1); plain ClusterIP Services need **no**
  `--resolve-from-guest-cluster-dns`. `monitoring-plugin` is a plain ClusterIP Service, so step 2
  alone works — matching Phase 2's Thanos path.
- `window.SERVER_FLAGS.consolePlugins` is populated from `EnabledPluginsOrder`
  (`server.go:99`, `:727-734`); with the flag set it becomes `["monitoring-plugin"]`, and the
  frontend fetches `/api/plugins/monitoring-plugin/plugin-manifest.json` → assets, which the bridge
  proxies to the guest Service over socks5 (once §2 lands).
- **Add to the `hypershift/` layer** (generic) as an appended arg — appended, not inserted, because
  the per-cluster overlay patches earlier args by numeric index (see the existing
  `hypershift/kustomization.yaml` arg-index warnings). No per-cluster value needed (the endpoint is
  the same fixed guest service name for every cluster).

---

## 4. Plugin backend on the guest (`monitoring-plugin`)

The plugin's asset server is a **guest** workload (it serves the JS bundle from a guest ClusterIP
Service) in `openshift-monitoring`.

**Expected path (Part B enabled): CMO provides it.** `monitoring-plugin`'s provider is **CMO**
(§1.1 ownership model). In Phase 2 it was absent only because the `Console` capability was disabled
(no pod, `consolePlugins` empty — `CONSOLE_CONTROL_PLANE_PROGRESS.md`). Once **Part B enables the capability** and the
`consoleplugins` CRD exists, CMO is expected to reconcile **both** the monitoring-plugin **workload**
(Deployment + HTTPS Service w/ service-serving cert) **and** its `ConsolePlugin` CR — collapsing
this §4 and §5 into "verify CMO did it." **Verify in the spike** (§9 risk 1 / §10.2): CMO may gate
this on the capability set, the CRD's presence, or a CMO config flag — confirm the pod + CR appear
on their own.

**Fallback (only if CMO does not auto-deploy):** hand-deploy it, as below:

- **Source/image:** `github.com/openshift/monitoring-plugin`. Resolve the image from the release
  payload if present (`oc adm release info --image-for=monitoring-plugin <release>`); if the
  disabled-capability payload omits it, use the upstream published image or a `quay.io/patmarti`
  build. Confirm the exact image ref during the spike.
- **Objects (guest, `openshift-monitoring`):** Deployment + Service. The Service must be **HTTPS**
  with a **service-serving cert** (annotation `service.beta.openshift.io/serving-cert-secret-name`)
  so the asset transport's `-service-ca-file` trust validates it — the guest **does** have the
  service-ca operator (it is a normal OCP data plane), so this works guest-side even though the
  *management* cluster (GKE) does not (that is why the console pod uses a hand-mounted
  `service-serving-ca` ConfigMap — Phase 2).
- **Port:** conventionally `9443` (matches the `monitoring-plugin` ConsolePlugin the operator would
  create). Confirm against the image's served port during the spike; align the `-plugins` endpoint
  port (§3) and the ConsolePlugin CR port (§5).
- **Feature flags:** the CMO monitoring-plugin enables `alerting`, `legacy-dashboards`, `metrics`,
  `targets` (OCP 5.0+ naming). It talks to Thanos/Alertmanager — the **same** guest services
  Phase 2 already reaches. Configure its backend flags to point at the in-guest
  `thanos-querier`/`alertmanager-main` (it runs **in** the guest, so it uses normal in-cluster DNS,
  no konnectivity). Consult the monitoring-plugin README / Helm chart values for exact flag names.

Add under `console/guest/` (a new `monitoring-plugin/` subdir) with an `apply.sh`, mirroring the
existing guest CLI-downloads pattern. Apply with the guest kubeconfig
(`console/hostedcluster/kubeadmin.kubeconfig`).

---

## 5. ConsolePlugin CR on the guest

The frontend loads a plugin because the **bridge** was told about it (`-plugins`, §3). But the
console UI also reconciles/reads `ConsolePlugin` objects. The `console.openshift.io`
`ConsolePlugin` **CRD** now comes from **Part B** (enabling the Console capability → the guest CVO
installs all 8 console CRDs) — so unlike Phase 1's hand-applied CLI-downloads CRD, we no longer
hand-apply the CRD here. Only the **CR** is hand-applied (the console-operator, absent here, would
normally reconcile it).

- **CRD:** provided by Part B (guest CVO, Console capability). Do **not** hand-apply it.
- **CR:** expected to be created by **CMO** (the monitoring-plugin provider) once Part B is enabled
  (§4). Only hand-apply `console/guest/monitoring-consoleplugin.yaml` (`spec.backend.type: Service`,
  `spec.backend.service.{name,namespace,port,basePath}`) as a **fallback** if CMO does not.

**Design caveat / verify during spike:** Confirm whether the bridge strictly *needs* the CR at all
when the plugin is already forced on via `-plugins` (the `-plugins` map is the server-side source of
truth for name→endpoint; the CR may be needed only for operator-driven discovery + certain
frontend-side lookups). Two independent questions to record: (a) does CMO create the CR
automatically? (b) does the CR matter for *loading* given the `-plugins` flag? Both feed Part C
(§10.2).

**Ownership (open, addressed by Part C):** for monitoring-plugin the CR is **CMO's** to own
(coherent with other platforms); the *enablement/wiring* leg (`spec.plugins` → console-config) is
what the ported console-operator owns and what our `-plugins` flag stands in for (§1.1, study §14.5,
§22). Analyzed in §10.

---

## 6. Reused-from-Phase-2 plumbing (no changes)

Already live in `console/kustomize/hypershift/kustomization.yaml`, verified end-to-end in Phase 2 —
Phase 3 depends on but does not modify these:

- **Konnectivity socks5 sidecar** (`konnectivity-proxy-socks5`, CPO image), mounts
  `service-network-admin-kubeconfig` (resolver), `konnectivity-client` cert, `konnectivity-ca-bundle`.
- **Bridge proxy env**: `HTTP_PROXY`/`HTTPS_PROXY=socks5://127.0.0.1:8090`,
  `NO_PROXY=kube-apiserver.<ns>.svc,localhost,127.0.0.1` (keeps KAS + terminal direct).
- **`-service-ca-file=/var/run/service-ca/service-ca.crt`** + the `service-serving-ca` ConfigMap
  mount — this is exactly the trust the plugin asset transport uses once §2 lands.
- **Geneve VPC firewall rule** (`console/guest/allow-geneve-firewall.sh`, GCP-1221) — cross-node
  pod networking, a hard prerequisite for konnectivity reaching guest plugin pods.

The socks5 path already carries Thanos/Alertmanager traffic in Phase 2; plugin asset traffic rides
the identical `HTTP(S)_PROXY` → socks5 → guest-service path. The **only** thing stopping plugin
assets today is the §2 code gap.

---

## 7. Sequencing

**Recommended order: Part B → Part A → Part C** (enable the capability first so the plugin CRD +
scaffolding exist properly, then load the plugin, then write up the gap analysis).

**Part B (capability + cleanup) first:**
1. Enable `Console` in the HC (§9.1) — may require HC recreation (immutability); neutralize the
   guest console-operator (§9.2 option 1: scale 0 / `managementState: Removed`).
2. Verify guest scaffolding (8 CRDs, namespaces, RBAC, `clusteroperator/console`); clean up
   `console/guest/` (§9.3 — delete the hand CRD, update `apply.sh`).

**Part A (load the plugin):**
3. **§2 bridge fix** — build the patched image, bump the overlay digest (one-liner, gates the rest).
4. **Verify CMO reconciled monitoring-plugin** (§4) — expected once Part B is on: the workload
   (Deployment + HTTPS Service) **and** the `ConsolePlugin` CR should appear on their own. Confirm
   it serves the manifest in-guest (`oc exec … curl
   https://monitoring-plugin.openshift-monitoring.svc:9443/plugin-manifest.json`). **Only** if CMO
   did not: hand-deploy the workload (§4 fallback) + CR (§5 fallback).
5. **§3 add `-plugins` flag** to the bridge (hypershift layer) — the enablement/wiring leg CMO does
   **not** do; re-apply the overlay, roll the pods.
6. **Validate** (§8).

**Part C (analysis):**
8. Record findings + finalize the gap list / CPO plumbing design (§10–§12).

Note: if immutability blocks enabling the capability on the live HC, Part A can be spiked first with
the Phase-1 hand-applied CRD as a fallback, then re-based onto Part B on a fresh HC. Prefer Part B
first if recreation is cheap.

---

## 8. Phase 3 validation / acceptance — MET (verified live on dev-patmarti)

- [x] Patched console image runs (Pods Ready), now alongside a token-minter init-sidecar (below).
- [x] `monitoring-plugin` Deployment + Service Running/Ready in the guest `openshift-monitoring`
  (shipped by CMO once the capability was enabled); serves `plugin-manifest.json` in-guest over its
  service-serving cert.
- [x] The bridge enables the plugin: `main.go` logs "Console plugins are enabled: monitoring-plugin"
  (was `[]` in Phase 2).
- [x] Plugin **assets load through the tunnel**: bridge → socks5 → guest
  `monitoring-plugin.openshift-monitoring.svc:9443` returns the manifest (HTTP 200, verified in-pod
  via the socks5 proxy + `-service-ca-file` trust). No proxy/TLS errors on the plugin path.
- [x] Browser: Observe → **Alerting**, **Dashboards**, and **Targets** all render — the exact gap
  Phase 2 left open. Dashboards initially 403'd (`console service account cannot list resource`) until
  the token-minter sidecar gave the bridge the guest `console` SA identity (see PROGRESS Phase 3).
- [x] Regression: Phase 1 (browse, OIDC login, CLI-downloads) + Phase 2 (terminal, Observe →
  Metrics) still work with the plugin present. `NO_PROXY` keeps KAS/terminal direct.
- [x] Fleet-rollout safety: console + plugin config is control-plane-only (bridge flag) / guest-only
  (plugin backend); it feeds no NodePool config hash. The CPO console strip is GCP-gated (does not
  change other platforms' guest payload).

---

# Part B — Enable the `Console` capability guest-side (scaffolding only)

## 9. Why, and exactly what it changes

Today the HostedCluster has `Console` in `spec.capabilities.disabled`
(`console/hostedcluster/hostedcluster.yaml:48-52`). That means the guest CVO installs **none** of
the console-operator's guest scaffolding — so Phase 1 hand-applied fragments of it under
`console/guest/` (the CLI-downloads CRD + CR), and per-user OIDC admin RBAC
(`redhat-domain-admins.yaml`). Part B **enables the capability** so the guest CVO installs the
proper scaffolding (CRDs, namespaces, RBAC, ServiceAccounts, ClusterOperator, config CRs), and we
**stop hand-rolling it**.

**Crucial distinction (verified):** the console-operator payload splits cleanly into
**scaffolding** vs **workload**, and the operand Deployments are **not in the payload at all**:

| Bucket | Objects | In the guest CVO payload? | Phase 3 disposition |
|---|---|---|---|
| **Scaffolding** | 3 Namespaces (`openshift-console`, `-console-operator`, `-console-user-settings`); all 8 `console.openshift.io` CRDs (incl. `consoleplugins`, `consoleclidownloads`); the `operator.openshift.io/v1 Console` CR; ClusterOperator `console` (**but see §9.2.1 follow-up**); all RBAC (ClusterRoles/Roles/Bindings, incl. `system:auth-delegator` for the `console`/`console-operator` SAs); SAs; config/telemetry ConfigMaps; NetworkPolicies; ServiceMonitors | **YES** — every file annotated `capability.openshift.io/name: Console` | **KEEP** (this is the point of Part B) |
| **Operator workload** | `console-operator` Deployment (`0000_50_console-operator_07-operator-ibm-cloud-managed.yaml` — the ibm-cloud-managed variant is the one that ships in HyperShift payloads) | **YES** | **STRIP** via `manifestsToOmit` (§9.2) — chosen faithful mechanism, net-new CPO code |
| **Operand workload** | `console` + `downloads` Deployments/Services/Routes/PDBs | **NO** — created at *runtime by the console-operator* from its bindata | N/A — never in the payload; our kustomize tree creates the operands control-plane-side |

Evidence: console-operator `manifests/` inventory (namespaces `02-namespace.yaml`, SA `06-sa.yaml`,
RBAC `03-rbac-*`/`04-rbac-*`, ClusterOperator `95-clusteroperator.yaml`, operator Deployment
`07-operator.yaml` / `07-operator-ibm-cloud-managed.yaml`), the 8 CRDs under vendored
`openshift/api/console/v1/zz_generated.crd-manifests/`, all gated by
`capability.openshift.io/name: Console`; operands are bindata (`bindata/assets/deployments/
console-deployment.yaml`, `downloads-deployment.yaml`), created by
`pkg/console/subresource/deployment/deployment.go`. HyperShift threads the capability via
`support/capabilities/hosted_control_plane_capabilities.go:CalculateEnabledCapabilities` →
guest ClusterVersion `AdditionalEnabledCapabilities` (CVO bootstrap `v2/cvo/deployment.go:91-94`;
HCCO steady-state `.../resources/resources.go:1674-1690`). HCCO reconciles **no** console-specific
guest resources — it's pure CVO capability payload.

## 9.1 How to enable it

Remove `Console` from `spec.capabilities.disabled` in
`console/hostedcluster/hostedcluster.yaml` (keep `Ingress` disabled — see below). Two caveats from
the API:
- **CEL cross-dependency (`hostedcluster_types.go`): `Ingress` disabled ⇒ `Console` must also be
  disabled.** This HC deliberately keeps **both** `Console` and `Ingress` disabled today; we want to
  enable **Console** while keeping **Ingress disabled** (the console runs control-plane-side; no
  guest ingress/router). That combination is exactly what the rule forbids. **We relax it for the
  GCP platform** (see §9.1.1): the field-level rule was moved to a spec-level rule guarded by
  `self.platform.type == 'GCP'`, so on GCP Console-enabled + Ingress-disabled is now accepted while
  every other platform keeps the original constraint. See the OCPBUGS-58422 watch item (§9.1.2).
- **Immutability** (`hostedcluster_types.go:846`): `spec.capabilities` is immutable via CEL on an
  existing HC. **Confirmed (2026-09-17)** by a server-side dry-run against the live `pat-console` HC:
  `spec.capabilities: Invalid value: Capabilities is immutable`. Enabling Console therefore
  **requires recreating** the HostedCluster — fold it into the Phase 3 bring-up (and land the
  `manifestsToOmit` CPO change first, since the recreated HC must come up with the operator already
  stripped).

## 9.1.1 Relaxing the Ingress↔Console CEL rule for GCP (implemented)

The upstream field-level rule on `Capabilities.Disabled`
(`!self.exists(cap, cap == 'Ingress') || self.exists(cap, cap == 'Console')`) cannot see
`spec.platform.type` (field-level CEL scope is only `self` = the disabled list). So it was:
1. **Removed** from the `Disabled` field.
2. **Re-added at the `HostedClusterSpec` level** — where CEL can reference both `platform.type` and
   `capabilities` — guarded for GCP:
   `self.platform.type == 'GCP' || !has(self.capabilities) || !has(self.capabilities.disabled) || !self.capabilities.disabled.exists(cap, cap == 'Ingress') || self.capabilities.disabled.exists(cap, cap == 'Console')`

Regenerated CRDs via `make api`. Envtest coverage added
(`stable.hostedclusters.capabilities.testsuite.yaml`): the existing AWS "should fail" case still
fails (constraint preserved off-GCP); a new GCP case with Console-enabled + Ingress-disabled passes.
Track in `reference/UPSTREAM_PATCHES.md` (GCP-1219 family).

## 9.1.2 WATCH ITEM — OCPBUGS-58422 (upstream fix in flight)

**Jira:** [OCPBUGS-58422](https://redhat.atlassian.net/browse/OCPBUGS-58422) — "Allow disabling
Ingress without Console in HyperShift." Upstream is fixing this **properly and for all platforms**;
our GCP-only CEL relaxation is an interim measure to unblock the spike. Watch and converge:

- **console-operator [#1182](https://github.com/openshift/console-operator/pull/1182) — MERGED
  (2026-08-24).** Makes the console-operator handle ingress-disabled **gracefully**: at startup it
  detects the external-control-plane + ingress-disabled topology and **skips the route + health-check
  controllers** (no IngressController informer, no `route "console" not found` crash-loop); if
  Ingress is later enabled it **auto-restarts** (poll-and-restart, same pattern as OLM). Cherry-picked
  to **release-4.22 (#1214)** and **release-5.0 (#1215)**. Relevant to us because it confirms an
  ingress-disabled console-operator no longer crash-loops — i.e. even if we *didn't* strip the
  operator (§9.2), a guest operator would now sit quietly rather than degrade. It also touches RBAC
  (`capability.openshift.io/name: Console` → `Console+Ingress` on the ingress-operator-ns role).
- **hypershift [#8933](https://github.com/openshift/hypershift/pull/8933) — OPEN (on hold).** Removes
  the Ingress↔Console CEL rule + the mirror CLI validation **for all platforms** (depends on #1182).
  When this merges upstream, **replace our GCP-only relaxation (§9.1.1) with the upstream removal** to
  avoid drift — our narrower rule becomes redundant. Until then, keep the GCP guard so non-GCP
  platforms retain the original safety.

**Convergence action when #8933 merges:** drop the spec-level GCP-guarded rule entirely (match
upstream), keep the envtest updated, and remove the entry from `reference/UPSTREAM_PATCHES.md`.

## 9.1.3 RESOLVED (2026-09-17) — ran on a dedicated dev MC

The GCP CEL relaxation (§9.1.1) lives in the **HostedCluster CRD**, installed cluster-wide by the
HyperShift Operator. The original test cluster was the **shared integration MC**
(`gcp-hcp-int-mc-us-central1-yjiv`), whose CRDs we do **not** own and must not mutate — so a
`pat-console` HC (Console enabled + Ingress disabled) was rejected there by the stock
`Ingress-disabled ⇒ Console-disabled` rule.

**Resolution:** we stood up a **dedicated dev MC** (`dev-mgt-us-c1-p0917`, dev-patmarti) running our
HyperShift build. To make the operator image *also* carry the operator (not just the CPO override),
`console/Dockerfile.dev.fast` now builds the full binary set, and the MC's ArgoCD/kustomize config
overrides the operator image to our build for the dev-patmarti target only (see the gcp-hcp-infra
`dev-patmarti` branch). With our HO running, the relaxed CRD is installed and the HC is admitted. All
of Part A/B were then verified live — see `CONSOLE_CONTROL_PLANE_PROGRESS.md` (Phase 3).

The HC reuses the previous region's OIDC issuer (shared infraID `patmart-b3bb` / published JWKS) and
customer project (`patmarti-hcp-test`); only the API/OAuth/console endpoints moved to the new
region's HC DNS zone (`us-central1-p0917-1.dev.gcp-hcp.devshift.net`).

## 9.2 Stripping the operator Deployment from the CVO payload (chosen: the faithful mechanism)

Part B enables the capability with `BaselineCapabilitySet: None` semantics, so the guest CVO would
install **everything** Console-gated — **including the `console-operator` Deployment**. We do not
want the operator running in the guest:
- We are **not** yet running a control-plane-side console-operator (that's the operator phase).
- A guest-side console-operator would try to create the `console`/`downloads` **operands in the
  guest** and write guest status — which **conflicts** with our control-plane-side operands.

**Chosen approach: manifest-strip the operator Deployment** via the CVO `preparePayloadScript`
placement path (`v2/cvo/deployment.go` `manifestsToOmit`), mirroring the existing `!oauthEnabled`
`0000_50_console-operator_01-oauth.yaml` strip at `:240-242` and the image-registry operator strip
at `:188-190`. This is net-new CPO code + a rebuilt CPO image, but it is the *real* productization
mechanism and avoids the drift-prone runtime neutralization. (The cheaper spike stand-in —
scale the guest Deployment to 0 / set the `operator.openshift.io/v1 Console` CR
`managementState: Removed` — is **not** what we're doing here; kept only as a fallback note.)

**Verified mechanics (2026-09-17):**
- Guest CVO runs with **`CLUSTER_PROFILE=ibm-cloud-managed`** (confirmed on the live `pat-console`
  guest CVO Deployment env; `v2/assets/cluster-version-operator/deployment.yaml:41-42`). This is
  the OpenShift *hosted/externally-managed* cluster profile — the name is a historical artifact, not
  IBM-specific. Every HyperShift-shipped operator manifest carries **both**
  `include.release.openshift.io/hypershift: "true"` and
  `include.release.openshift.io/ibm-cloud-managed: "true"`.
- Two operator Deployment variants exist in the payload (verified via `oc adm release extract` of
  the exact release digest): `0000_50_console-operator_07-operator.yaml`
  (`self-managed-high-availability` + `single-node-developer` only — **not** in our profile) and
  `0000_50_console-operator_07-operator-ibm-cloud-managed.yaml` (`hypershift` + `ibm-cloud-managed`
  — **the one that ships in our guest**).
- `manifestsToOmit` matches on **exact release-manifests basename** (`rm -f
  /var/payload/release-manifests/<basename>`, `deployment.go:238`). The generic
  `rm -f .../manifests/*_deployment.yaml` at `:214` only touches the *bootstrap* `/manifests` dir and
  would not match this file anyway.

**Exact file to add to `manifestsToOmit`:**
```
0000_50_console-operator_07-operator-ibm-cloud-managed.yaml
```
This strips **only** the operator Deployment. All ~60 other `0000_50_console-operator_*` files (8
CRDs, 3 namespaces, RBAC, SA, config CRs, NetworkPolicies, ClusterOperator, downloads CRs,
quickstarts) remain — verified against the extracted payload; no scaffolding is collaterally
removed.

**Watch item — two `Controller=true` owners (study §17.2):** never let both a guest console-operator
and our tree own the same operand. Stripping the operator Deployment from the payload guarantees no
guest operand is ever created, cleanly avoiding the conflict.

## 9.2.1 FOLLOW-UP (open decision): also strip the console `ClusterOperator`?

**This is an open design question — must be resolved before/at HC recreation. Do not forget.**

The payload also ships `0000_50_console-operator_95-clusteroperator.yaml` — a
`ClusterOperator/console` object with `status.versions[operator]=<release version>` but **no live
conditions**. With the operator Deployment stripped, **nothing reports status on it.**

**Verified risk (2026-09-17, upstream CVO logic):** the guest CVO pre-creates the ClusterOperator
from the payload manifest, then during apply/upgrade runs `checkOperatorHealth`
(`cluster-version-operator/pkg/cvo/internal/operatorstatus.go`). Because the shipped manifest has
non-empty `status.versions`, it passes the "no versions" guard, but the live CO has no
`Available=True` condition ⇒ `available == false` ⇒ it returns `ClusterOperatorNotAvailable` /
`UpdateEffectFail`. **This branch has no mode exception — it blocks in InitializingMode,
UpdatingMode, and ReconcilingMode alike.** Likely effect: the guest ClusterVersion never reaches the
target level (Progressing forever, "Cluster operator console is not available"), stalling initial
rollout **and every future upgrade** at console's runlevel.

**Why image-registry is NOT a counter-precedent:** image-registry keeps its ClusterOperator in the
payload today, but only because in pat-console the **ImageRegistry capability is DISABLED** — so the
CVO never renders the image-registry CO at all (e2e asserts it "not found",
`test/e2e/util/util.go:3897-3899`). That is the safe state "(a) capability disabled → CO not
rendered." Our case is different: Console **enabled** + operator stripped.

**Precedent that DOES apply:** OLM/marketplace runs management-side, so HyperShift strips **both** its
operator Deployment **and** its ClusterOperator (`deployment.go:177`,
`0000_50_operator-marketplace_10_clusteroperator.yaml`). Console-with-stripped-operator is the same
situation.

**Two safe states only:** (a) capability disabled → CO never rendered; (b) capability enabled →
**explicitly omit the CO manifest** from the payload. A present-but-empty CO is **not** safe.

**Recommended resolution (to confirm):** also add
`0000_50_console-operator_95-clusteroperator.yaml` to `manifestsToOmit` (mirroring the marketplace
CO strip). Downside: the guest then has **no** `clusteroperator/console` object at all — some
consumers/tests expect it present. The eventual control-plane-side console-operator (operator phase,
study §14.2/§18.3) is what should publish this CO status; until then, omitting it is the only way to
keep the CVO unblocked. **Decision needed:** confirm we strip the CO now (recommended, unblocks CVO)
vs. keep it and accept the CVO block risk vs. some interim status shim. Track this to the operator
phase where a control-plane operator publishes real CO status.

**Add a `deployment_test.go` case** for whichever files we strip (parallel to the existing oauth
strip test at `deployment_test.go:47-54`).

## 9.3 `console/guest/` cleanup (the concrete deliverable)

Once the capability is enabled, the guest CVO owns what we hand-applied. Reconcile
`console/guest/`:

| File | Today (capability disabled) | After Part B (capability enabled) — DONE |
|---|---|---|
| `consoleclidownloads-crd.yaml` | hand-applied CRD | **DELETED** — CVO installs the console CRDs (incl. `consoleclidownloads` + `consoleplugins`) |
| `oc-cli-downloads.yaml` | hand-applied CR (operator would write it) | **KEPT** (still no operator writing it; the `ConsoleCLIDownload` CRD now comes from CVO). Revisit in Part C |
| monitoring `ConsolePlugin` CR | would be hand-applied | **NOT NEEDED** — CMO ships the `ConsolePlugin` CR once the capability is on (never had to hand-apply it) |
| `redhat-domain-admins.yaml` | hand-applied OIDC-admin RBAC | **KEPT** — unrelated to the Console capability (external-OIDC identity RBAC); still needed |
| `allow-geneve-firewall.sh` | GCP infra workaround | **KEPT** — unrelated (GCP-1221) |
| `apply.sh` | applies CRD then CR | **UPDATED** — dropped the CRD apply; keeps the CR apply |

Net: Part B **removes the hand-applied CRD** and lets everything CRD/namespace/RBAC/ClusterOperator
come from the capability payload. The remaining `console/guest/` files are the things a
**control-plane-side operator would own but doesn't yet** (the CRs) plus genuinely out-of-band items
(OIDC-admin RBAC, geneve). That residue is exactly the input to Part C.

## 9.4 Part B acceptance — MET (verified live)

- [x] HC has `Console` **enabled**; guest CVO installs the console CRDs (8, incl. `consoleplugins` +
  `consoleclidownloads`), namespaces, and RBAC (verified: `oc get crd | grep console.openshift.io`).
- [x] Guest `console-operator` is **stripped from the payload** (not scaled 0 — CPO's GCP-gated
  `manifestsToOmit` removes the operator Deployment + ClusterOperator; see §9.2); **no**
  console-operator in the guest `openshift-console-operator` namespace, and our `console`/`downloads`
  operands stay control-plane-side (verified live).
- [x] `console/guest/consoleclidownloads-crd.yaml` deleted; `apply.sh` updated; CLI-downloads UI
  still works (the `oc-cli-downloads` CR is hand-applied; the CRD now comes from CVO).
- [x] Part A plugin still loads (its CRD now from CVO, `ConsolePlugin` CR shipped by CMO).

---

# Part C — Analysis: what the console-operator must do (seeds Phase 4)

> **This became Phase 4.** Part C is the *analysis* that enumerates everything a control-plane-side
> console-operator would own (the things Parts A/B hand-wire). Turning that into a decision —
> **port the console-operator vs. reimplement (e.g. in CPO)** — and building it is **Phase 4**, a
> separate phase. The inventory below is the Phase 4 backlog.

## 10. Gap inventory — what's missing when nothing runs the operator

Parts A/B leave a set of things the **console-operator normally owns** hand-wired or unwired. This
inventory enumerates them precisely so **Phase 4** (the operator phase — port vs. reimplement) has a
concrete backlog. We did **not** build the operator in Phase 3.

Grouped by what the (ported, control-plane-side) operator would reconcile. Cross-refs to study
§14's split-client work.

### 10.1 Operand generation (today: our kustomize tree)
- **`console` + `downloads` Deployments/Services/PDBs** in the **management** HCP namespace. Today
  hand-authored in `console/kustomize/{origin,hypershift,pat-console}`. Operator equivalent:
  `deployment.go` operand generation with the off-cluster bridge flags injected (study §14.4) and
  the operand namespace parameterized to the HCP namespace (study §14.3). **This is the bulk of the
  operator port.**
- **Bridge CLI flags** we set by hand (off-cluster endpoint, `-ca-file`, `-service-ca-file`, OIDC
  issuer/client-id/secret file, session keys, `-branding=ocp`, `-plugins`, monitoring/Thanos/AM
  URLs, proxy env) — all things the operator would render from `console-config.yaml` + cluster CRs.
- **`POD_NAME` env** (downward API) the operator injects for multi-replica OIDC session cookie
  naming ("console distinguishes cookie sessions by pod names in OIDC envs" — console-operator
  `deployment.go`). Hand-set in the `hypershift` overlay today.
- **`console` SA token / identity** the bridge uses for its own backend calls (Dashboards, plugin
  metrics). Today a token-minter sidecar creates the guest SA + mints the token; the operator would
  own the SA + token lifecycle.

### 10.2 Plugin config (today: Part A `-plugins` flag + hand-applied CR)
- **Dynamic discovery:** operator watches guest `ConsolePlugin` CRs → `getPluginsEndpointMap` →
  `console-config` `plugins:` → **rolls the bridge** (study §14.5,
  `_console-operator-research/.../configmap.go:255-262`). Today Part A hard-codes **one** plugin via
  `-plugins`; adding/removing a plugin means editing kustomize + re-rolling by hand.
- **The `ConsolePlugin` CR itself:** for a CMO-shipped plugin like monitoring-plugin the CR is
  normally created by **CMO** (not console-operator) once the Console capability is on. Verify in
  the spike whether enabling the capability + CMO now creates the `monitoring-plugin` ConsolePlugin
  CR automatically (making Part A §5's hand-applied CR redundant). Record which owner (CMO vs
  hand) applies in this topology.

### 10.3 Secret / config injection (today: hand-applied secrets)
- **OIDC client secret** — Part 1 mounts it from a hand-made secret (`console-oidc-client-secret`,
  from the Google OAuth client JSON). The operator/HCCO productization path is the day-2 "enable
  console with this OIDC client id + secret" flow (study §23, `reference/GOOGLE_OIDC_CLIENT_SETUP.md`). Open
  blocker upstream is Google client provisioning (study D2 / `reference/CONSOLE_AUTH_OPTIONS.md` §7) — **not**
  operator work per se, but the operator is where the wiring lands.
- **Session cookie keys** (32B AES + 64B HMAC) — hand-generated in `apply.sh`; operator normally
  generates/rotates them (`session_secret.go`). (study §23)
- **`oauth-serving-cert` / `default-ingress-cert` sync (resourceSyncer)** — **not needed on the
  OIDC path** (study §14.8); only matters if integrated OAuth returns. Note as deferred.

### 10.4 Guest config CRs + status (today: partially hand-applied / unwritten)
- **`console-public` ConfigMap** (`openshift-config-managed`, holds the console URL) — operator
  writes it; unwritten here. Impact: some consumers read the console URL from it. Verify what breaks.
- **`clusteroperator/console` status** + **`console.config .status.consoleURL`** — with the
  capability enabled (Part B) and no operator writing status, the guest shows an
  enabled-but-unmanaged console CO. Part B §9.2 neutralizes the guest operator; a **control-plane**
  operator must publish this status (study §14.2 guest client, §18.3). Today: unwritten.
- **`ConsoleCLIDownload` CR** (`oc-cli-downloads`) — hand-applied (`console/guest/`); operator's
  `CLIDownloadsSyncController` normally owns it and would drift-correct the downloads host.
- **User-settings namespace + RBAC** — `openshift-console-user-settings` + console-SA Role/Binding.
  **Now provided by Part B** (it's capability scaffolding). Verify user-settings persistence works
  once the capability is on (the Phase 1 user-settings limitation in `CONSOLE_CONTROL_PLANE_PROGRESS.md`
  may close as a Part B side effect — check).
- **Node arch/OS** for the downloads page / Lightspeed gate — operator reads **guest** nodes
  (study §14.9 item 2). Today static. Low impact.

### 10.5 Guest auth identity (today: token-minter / static)
- The bridge authenticates to the guest KAS. Operator productization = token-minter sidecar
  (`KubeAPIServerToken`) for the bridge + `InjectServiceAccountKubeConfig` for the operator
  (study §16), authenticating as the **CVO-created** guest SAs `openshift-console/console` and
  `openshift-console-operator/console-operator` — whose RBAC (incl. `system:auth-delegator`) is now
  installed by Part B's capability payload (study §19.1). This is a clean synergy: **Part B provides
  the guest SA identities + RBAC that the operator phase's token-minter will authenticate as.**

## 11. How the operator plumbs into CPO (design reference for the operator phase)

Verified against existing CPO v2 components. The `consoleoperator` component follows the
**ingress-operator** pattern almost exactly (an OCP operator run control-plane-side that reconciles
the guest), with CNO as the secondary reference for management-side RBAC assets.

### 11.1 Registration + capability gate
- **Register unconditionally** in `hostedcontrolplane_controller.go` `registerComponents`
  (`:242-297`), next to `ingressoperatorv2.NewComponent()` (`:283`) — gating is done **inside** the
  component via `WithPredicate`, not at registration (verified: ingress/registry/nto all do this;
  only management-cluster capabilities gate at registration, e.g. OLM `:294-296`).
- **Add `capabilities.IsConsoleCapabilityEnabled`** to
  `support/capabilities/hosted_control_plane_capabilities.go`, mirroring `IsIngressCapabilityEnabled`
  (`:27-38`) — verified **absent** today; the `ConsoleCapability` API constant already exists
  (`hostedcluster_types.go:491`). Wire it as `WithPredicate(isConsoleCapabilityEnabled)`
  (ingress model `component.go:83-87`).

### 11.2 Builder shape (copy ingress-operator)
- `InjectServiceAccountKubeConfig{Name: "console-operator", Namespace: "openshift-console-operator",
  MountPath: "/etc/kubernetes", ContainerName: ComponentName}` — the operator's guest client, as a
  client-cert kubeconfig for the CVO-created guest SA (ingress model `component.go:68-73`;
  `kubeconfig.go`). **The SA name/namespace are dictated by Part B's payload RBAC, not chosen**
  (study §14.9 item 3, §19.1).
- `InjectKonnectivityContainer{Mode: HTTPS, HTTPSOptions{ConnectDirectlyToCloudAPIs: true},
  KubeconfingVolumeName: "admin-kubeconfig"}` — operator talks only to the guest KAS ⇒ **HTTPS**
  mode (study §13.10; ingress/oapi precedent). (The **bridge**, separately, needs **Socks5** for
  plugin/monitoring guest services — which is exactly the sidecar Phase 2/Part A already run
  hand-rolled.)
- `WithDependencies(oapiv2.ComponentName)`; `InjectAvailabilityProberContainer` waiting on
  `operator.openshift.io/v1 Console` + `console.openshift.io/v1 ConsolePlugin` guest APIs (now
  installed by Part B).
- **Management-side RBAC assets:** unlike ingress-operator (whose operands live guest-side),
  the console operand lives **management-side** in the HCP namespace, so the ported operator needs
  HCP-namespace RBAC to create it → ship `serviceaccount.yaml`/`role.yaml`/`rolebinding.yaml` in
  `v2/assets/console-operator/` (the **CNO** model, study §19.2). The framework auto-applies them to
  the HCP namespace and rewrites the RoleBinding subject namespace
  (`controlplane-component.go:278-311`, esp. `:289-294`).
- **Image dev-overrides** already wired via the HC `image-overrides` annotation (study §13.8) — run
  the patched operator/bridge before upstream merges (study D5).

### 11.3 Relationship to Part B
- Part B **keeps the capability payload** (scaffolding + guest operator). The operator phase adds
  the CVO **placement-flag strip** of just the operator **workload** (`manifestsToOmit`, mirroring
  the oauth strip `v2/cvo/deployment.go:240-242`; study §18), so the *control-plane* operator is the
  sole operator. Part B's "scale guest operator to 0" (§9.2 option 1) is the spike stand-in for that
  strip.
- Closest end-to-end precedent for "capability enabled + operator control-plane-side + relies on
  guest CVO scaffolding": **cluster-image-registry-operator** — gated on
  `IsImageRegistryCapabilityEnabled`, runs control-plane-side, and its deployment even **blocks at
  startup polling the guest API** for the capability's resources
  (`v2/assets/cluster-image-registry-operator/deployment.yaml:39-45`). This is the template for the
  console operator waiting on Part B's guest scaffolding.

## 12. Part C deliverable

A written gap list (§10) + CPO plumbing design (§11) — no code. Output: confirm/adjust the operator
phase backlog in study §14, and record spike
findings (esp. §10.2 CMO-vs-hand ConsolePlugin CR ownership, §10.4 user-settings closing as a Part
B side effect, §5 CR-vs-flag necessity).

---

## 13. Risks / open items

1. **Does CMO auto-deploy monitoring-plugin once Part B is on?** (§4, §10.2) — the expected path is
   CMO reconciling the workload + CR once the capability/CRD exist. If CMO gates on something else
   (e.g. console-operator enablement) it may not, and §4/§5 fall back to hand-deploy. Confirm early;
   it determines how much of Part A is real work vs "verify CMO did it." Also confirm the plugin
   **image** resolves from the payload (`oc adm release info --image-for=monitoring-plugin`).
2. **CR-vs-flag necessity** (§5) — determine whether the `ConsolePlugin` CR is required for
   *loading* when `-plugins` already forces the plugin on server-side. Affects whether the CR (from
   CMO or hand) matters at all for the spike.
3. **monitoring-plugin backend flags** — its Thanos/Alertmanager wiring (in-guest, not
   konnectivity) must match the guest monitoring stack's service names/ports/auth. Consult the
   README/Helm values; this is plugin config, not console/HyperShift work.
4. **Asset performance at scale** — plugin assets stream through konnectivity (study §10 risk 3).
   Validate the manifest + bundle load latency; not a blocker for the spike.
5. **Upstream acceptability of §2** — trivial and mirrors the existing `/api/proxy` transport;
   low risk. Bundle with the existing off-cluster PR family.
6. **Multi-replica sessions** (Phase 1 known limit) — unchanged; plugin loading is per-pod and
   stateless, unaffected.

---

## 14. File:line evidence index (Phase 3-specific)

| Claim | Reference |
|---|---|
| Plugin asset transport omits `Proxy` (the fix site) | `_console-research/pkg/server/server.go:518-528` (fix at `:524`) |
| Asset + i18n share the one pluginsHandler client | `server.go:519,530-532,534-539`; `pkg/plugins/handlers.go:167` |
| Plugin API proxy already honors proxy env | `pkg/proxy/proxy.go:56-79` (`:59`); wired `server.go:555` |
| `-plugins` flag (MultiKeyValue name→endpoint) | `cmd/bridge/main.go:154-158`; `pkg/serverconfig/config.go:21-67` |
| `consolePlugins` served from EnabledPluginsOrder | `pkg/server/server.go:99,727-734` |
| Frontend loads `/api/plugins/<name>/plugin-manifest.json` | `frontend/packages/console-dynamic-plugin-sdk/src/runtime/plugin-init.ts:27-30` |
| Plugin endpoint URL shape (operator-generated) | `_console-operator-research/.../configmap.go:255-262` (study §14.5) |
| Asset transport TLS = `-service-ca-file` trust (Phase 2) | `cmd/bridge/main.go:596,839-863`; `server.go:197,524` |
| socks5 resolver step 2 (Service→ClusterIP, no DNS flag) | study §5.1; `support/konnectivityproxy/resolver.go:216-233` |
| monitoring-plugin = the Observe Alerting/Dashboards/Targets UI | `CONSOLE_CONTROL_PLANE_PROGRESS.md` (Phase 2); monitoring README ("Enabling Monitoring Locally") |
| Guest hand-applied CR precedent (CLI-downloads) | `console/guest/oc-cli-downloads.yaml` (the CRD now comes from CVO once the capability is on) |
| **Part B** — HC has Console disabled today | `console/hostedcluster/hostedcluster.yaml:48-52` |
| Console-gated scaffolding (namespaces/SA/RBAC/CO) | `_console-operator-research/manifests/{02-namespace,06-sa,03-rbac-*,04-rbac-*,95-clusteroperator}.yaml` |
| 8 console CRDs, all Console-gated | `_console-operator-research/vendor/github.com/openshift/api/console/v1/zz_generated.crd-manifests/*.crd.yaml` |
| Operator workload = the one Deployment (ibm-cloud-managed in HyperShift) | `manifests/07-operator-ibm-cloud-managed.yaml` |
| Operands NOT in payload — created by operator at runtime | `bindata/assets/deployments/{console,downloads}-deployment.yaml`; `pkg/console/subresource/deployment/deployment.go` |
| Capability → guest ClusterVersion additionalEnabledCapabilities | `support/capabilities/hosted_control_plane_capabilities.go:CalculateEnabledCapabilities`; `v2/cvo/deployment.go:91-94`; `.../resources/resources.go:1674-1690` |
| CEL: Ingress-disable requires Console-disable; capabilities immutable | `api/hypershift/v1beta1/hostedcluster_types.go:524,846` |
| CVO payload strip precedent (oauth console file) | `v2/cvo/deployment.go:240-242`; `manifestsToOmit` `:151-206,232-239` |
| **Part C** — no `IsConsoleCapabilityEnabled` today; API const exists | `support/capabilities/hosted_control_plane_capabilities.go` (absent); `hostedcluster_types.go:491` |
| ingress-operator component (copy shape) | `v2/ingressoperator/component.go:14-87` |
| CNO mgmt-side RBAC assets pattern (operand mgmt-side) | `v2/assets/cluster-network-operator/{serviceaccount,role,rolebinding}.yaml`; `controlplane-component.go:278-311` |
| registration (unconditional, gate via predicate) | `hostedcontrolplane_controller.go:242-297` (`ingressoperatorv2` `:283`) |
| image-registry-operator: capability-gated + waits on guest CVO scaffolding | `v2/registryoperator/component.go:37,60-64`; `assets/cluster-image-registry-operator/deployment.yaml:39-45` |
