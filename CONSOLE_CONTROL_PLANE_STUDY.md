# Feasibility Study: Running the Core OpenShift Console Control-Plane-Side in GCP HCP

**Status:** Draft / analysis — local, not committed
**Scope:** Core web console + console-operator relocated to the management cluster (control-plane side), replacing the data-plane console. GCP HCP specific.
**Companion file:** `console-control-plane-manifests.example.yaml` (illustrative Kubernetes manifests).

---

## 1. Motivation

- The console is effectively a control-plane component. Running it control-plane-side prevents customers from breaking it via data-plane changes.
- Enable console access on **zero-node** clusters (no workers required to reach/use the core console).
- GCP HCP specific implementation. Auth via external OIDC (GCP identities) initially; oauth may return as a requirement, so the design stays auth-agnostic.

---

## 2. Verdict

**Feasible.** The core console is a stateless Go reverse-proxy needing only the guest kube-apiserver + an auth issuer, both already reachable from the HCP namespace. Exposure reuses the existing shared HAProxy router + public LB / GCP Private Service Connect rails already used by api-server and oauth. Zero-node works.

Engineering cost splits across three areas:
- **HyperShift:** new CPO v2 component + exposure wiring (bulk of the work; all standard patterns).
- **console (bridge):** effectively config-only; one optional 1-line proxy fix if plugins/monitoring are proxied through konnectivity.
- **console-operator:** a real dual-client / configurable-namespace refactor to support split targets (operand on the management cluster, config CRs on the guest). This is the primary **upstream** engineering cost.

**For implementers:** the actionable, verified references are §13 (HyperShift CPO v2
component — copy-usable code + exact file:line), §14 (upstream console-operator
split-cluster refactor — client-per-resource table + exact change sites), §9 (console
bridge changes), and §15 (recommended sequencing). The companion manifest file shows the
resulting objects.

---

## 3. What the core console requires (console repo analysis)

The bridge boots and serves the core UI (login + browse k8s/OCP resources) with only:

| Requirement | Notes | Source |
|---|---|---|
| `-k8s-mode` (in/off-cluster) | off-cluster mode, pointed at guest KAS | `cmd/bridge/main.go:611,420` |
| `-listen` + TLS files | serves HTTPS | `main.go:743,748` |
| `-base-address` | public console URL | `main.go:202` |
| `-public-dir` static assets | shipped in image | `main.go:803` |
| `-user-auth` (oidc/oauth/disabled) | OIDC for GCP | `main.go:386` |
| k8s CA + bearer token | proxy to guest apiserver | `main.go:47,422,436` |

**All of the following are OPTIONAL and gated** — the bridge serves the core UI without them:

| Subsystem | Gate | Source |
|---|---|---|
| Dynamic plugins | `-plugins` empty tolerated | `main.go:519-528` |
| Thanos/Prometheus | `prometheusProxyEnabled()` | `server.go:235,375` |
| Alertmanager | `alertManagerProxyEnabled()` | `server.go:238,433` |
| GitOps / Catalogd | gated / 404 | `server.go:242,854` |
| service-ca | `if fServiceCAFile != ""` (NOT needed for core k8s browsing) | `main.go:449` |

Monitoring is truly optional: the Overview page does not hard-fail; monitoring URLs are injected into the frontend `ServerFlags` only when proxies are enabled (`server.go:774-782`), otherwise empty → graceful degradation.

---

## 4. Zero-node verdict: core console works

- Bridge is a Go HTTP server; runs anywhere the apiserver is reachable, no worker scheduling.
- k8s proxy target is `kubernetes.default.svc` apiserver only — never kubelet/nodes (`main.go:420`, `proxy/proxy.go:68`).
- Login (OIDC/oauth + TokenReview) hits the issuer + apiserver only (`auth/tokenreviewer.go:28-50`).
- Health-check list is empty — never probes nodes (`server.go:350-352`).
- Features that need workers (pod exec/terminal, logs, node dashboards, monitoring) are non-core and out of scope; the frontend degrades gracefully (empty lists), no crash.

---

## 5. Cross-plane runtime data paths

console-operator reconciling the guest API from the control plane is the **standard HCP pattern** (CNO, ingress-operator, DNS operator all reconcile the guest API from the control plane via a guest kubeconfig). Not a blocker.

Runtime data paths are solved by the existing konnectivity socks5/dual proxy sidecar (`support/controlplane-component/konnectivity-container.go`; precedent: oauth-openshift, oauth-apiserver, olm, catalog):

| Data path | Mechanism |
|---|---|
| Guest kube-apiserver | in-namespace `kube-apiserver.<hcp-ns>.svc:6443` |
| OIDC issuer (GCP) | direct (external) or via proxy if a guest-network path is needed |
| Plugin assets (data-plane plugins) | bridge `HTTP(S)_PROXY` → konnectivity socks5 → guest `svc.cluster.local`; custom resolver (see §5.1) |
| Monitoring (optional/future) | same konnectivity path |

### 5.1 How DNS resolution works when reaching data-plane components

This is the subtle part. A control-plane pod cannot resolve guest names like
`thanos-querier.openshift-monitoring.svc` or a plugin's `<svc>.<ns>.svc` with normal
DNS — those names only exist in the **guest** cluster's DNS, and their ClusterIPs are
only routable on the **guest** pod network. The konnectivity socks5 proxy sidecar
solves both (name → IP, and IP → reachable) with a **custom resolver**, not the OS
resolver (`support/konnectivityproxy/resolver.go`, `proxyResolver.Resolve` :110).

The bridge is configured with `HTTP(S)_PROXY=socks5://127.0.0.1:8090`, so every
upstream request (except `NO_PROXY` entries) is handed to the sidecar, which resolves
the hostname via a **4-step fallback chain**:

1. **Cloud-API / disabled bypass** (`resolver.go:112`). If the name is a cloud-provider
   API endpoint (GCP/AWS) or the resolver is disabled, use the default system resolver
   and do **not** tunnel. Keeps cloud API traffic on the management network.

2. **Guest Service lookup via the guest kube-apiserver — primary path**
   (`ResolveK8sService`, `resolver.go:216`). For `thanos-querier.openshift-monitoring.svc`
   the resolver splits the name into `Name=thanos-querier`, `Namespace=openshift-monitoring`
   (`:217-224`), does a Kubernetes `GET service` against the **guest API** (`:227`), and
   returns `service.Spec.ClusterIP` (`:233`). **No DNS server is involved** — it reads the
   Service object's ClusterIP directly. This is the common case for both plugin backends
   and the monitoring stack (they are ordinary ClusterIP Services). It requires the sidecar
   to have a **guest-cluster client** (the same guest kubeconfig the bridge uses).

3. **Guest CoreDNS over the tunnel** (`guestClusterResolver.resolve`, `resolver.go:59`).
   Only if step 2 fails **and** `--resolve-from-guest-cluster-dns` is enabled
   (`:119`). The resolver reads the guest `dns-default` Service in `openshift-dns` to get
   the CoreDNS ClusterIP (`:40-44`), then builds a `net.Resolver` whose `Dial` goes
   **through the konnectivity tunnel** to `<coreDNS-ClusterIP>:53` over TCP (`:49-53`) and
   issues a real DNS query answered by the guest cluster's CoreDNS (`:64`). The DNS packet
   path is: bridge → socks5 sidecar → konnectivity-server → reverse tunnel →
   konnectivity-agent (data plane) → guest CoreDNS. This covers names that are not plain
   Services (headless/SRV/custom records). Includes health/debounce logic
   (`konnectivityHealth`, `:132-142`) and optional fallback to management-cluster
   resolution when konnectivity is down (`:134,149`).

4. **Default Go resolver** (`resolver.go:196`) — last resort / management-cluster
   resolution.

**Dialing after resolution:** once a guest ClusterIP is obtained, the socks5 dialer opens
the TCP connection **through konnectivity** (server → reverse agent tunnel → into the guest
pod network), so the otherwise-unroutable guest ClusterIP becomes reachable from the
management side.

**Implications for this design:**
- The console bridge sidecar must be given a **guest kubeconfig** so step 2 (Service →
  ClusterIP) works — reuse the framework-generated per-SA `service-account-kubeconfig` (see
  §16), the same guest credential mechanism the operator uses.
- For plain plugin/monitoring Services, **step 2 alone is sufficient**; no DNS server call
  is needed.
- Enable `--resolve-from-guest-cluster-dns` on the sidecar only if non-Service names must
  resolve (headless services, custom DNS records).
- Names sent by the bridge must match the `<name>.<namespace>` split pattern; standard
  `svc.cluster.local` service names do.
- `NO_PROXY` must include `kube-apiserver.<hcp-ns>.svc` (the guest KAS is reached directly
  in-namespace, not through the tunnel) plus `localhost`/`127.0.0.1`.

---

## 6. Exposure architecture — reuse the shared router rails

The per-HCP HAProxy router pod (`app: private-router`, `v2/router/deployment.yaml`) already fronts KAS + oauth + ignition + konnectivity in **one pod**, `mode tcp` SNI passthrough (`router_config.template:6,28,34`). Backends are built dynamically from any Route labeled `netutil.HCPRouteLabel` (`v2/router/config.go:109-136`).

- **Public clusters:** a single `RouterPublicService` cloud LB fronts the router by SNI (`infra.go:481-491`). A new SNI host rides the existing LB.
- **Private GCP clusters:** the router internal LB is exposed via **Private Service Connect** (`GCPPrivateServiceConnect` CRD; HO service-attachment controller; CPO PSC-endpoint + Cloud DNS controller under `gcpprivateserviceconnect/`). PSC fronts the router as a whole by SNI. A new host rides the existing service attachment.
- **DNS:** external-dns auto-registers the Route `spec.host` in-zone (`support/netutil/route.go:124-125`), like `api-<name>.<domain>` / `oauth-<name>.<domain>`.
- **Certs:** the router holds no cert (passthrough); the console terminates its own TLS at the
  pod. Because the client is a **web browser**, the served leaf must be **publicly trusted** — an
  internal CA-signed leaf (the oauth/`pki` pattern) would trigger a browser TLS warning. **The
  console reuses the api-server certificate mechanism** (D3): a cert-manager-issued
  **wildcard** `*.<domain>` cert, provided as a Secret next to the HostedCluster and referenced
  the same way as `spec.configuration.apiServer.servingCerts.namedCertificates`. That wildcard
  already covers `console.<domain>` (same base domain), so **no new cert resource is needed** —
  the console mounts the existing wildcard secret. When no such cert is configured, fall back to
  a self-signed default (dev only; browser warns), exactly like api-server's default. GKE has no
  service-ca operator, so the OpenShift service-serving-cert mechanism is **not** available and
  is not used.

**Consequence:** the core console gets its own control-plane hostname on the shared rails; the traffic path is `user → public LB / PSC → HCP router → console pod`, entirely control-plane-side. This removes the earlier "guest ingress route → control-plane pod" loose end and gives a clean zero-node story with **no guest ingress dependency**.

---

## 7. Decisions

**D1 — Operator strategy: Option A (full console-operator port), contributed upstream.**
Port console-operator as a CPO v2 component reconciling the guest API. Retains route/oauth-client/plugin-discovery/session-rotation logic; eases future plugin/monitoring parity. The dual-client refactor (see §9) is to be **contributed upstream** to `openshift/console-operator`, with the ability to **override images** for local dev (custom operator/bridge images).

*Alternative considered and rejected: direct-CPO ("start simple").* A HyperShift-only path
where a CPO component owns the console operand directly (no operator port) was evaluated (see
§17). It is technically viable and ~80% object-reusable, but delivering a **full-featured**
console — specifically **dynamic plugin discovery** (watch guest ConsolePlugin CRs → regenerate
`console-config` → roll the bridge) plus custom logo, upgrade banners, and guest status writes —
would require re-implementing the console-operator's core logic inside CPO and hand-maintaining
`console-config` generation against upstream forever. That is code duplication that does not
evolve with future OCP releases. **Because full features and long-term maintainability are
requirements, Option A is chosen.** The investigation still de-risks Option A (see §17.2):
the `console-config` schema is stable and leniently parsed, and the `oidcClients` guest status
write is not mandatory for OIDC login.

**D2 — Auth: pluggable, OIDC-first.**
External OIDC (GCP) is the initial mode. Because oauth may return, the component is built auth-agnostic (support `-user-auth=oidc` and `-user-auth=openshift`); the oauth-client wiring path is retained, not deleted. Exposure is auth-agnostic — no rework if oauth returns.

**The OIDC base works on live GCP HCP** (HCCO writes the guest `Authentication` CR from the HC spec;
KAS structured-auth already live), and HCCO already **delivers** any `oidcClients[]` entry + secret
to the guest. **But there is one open BLOCKER:** the console bridge is a confidential web app that
needs its **own Google OAuth Web client**, and Google web-client creation is **not automatable** +
**forbids wildcard redirects** — so provisioning a console client per hosted cluster without a manual
Google step is the gating problem. It is **topology-independent** (hits data-plane console too, not
just this port). The realistic solutions are a **state-based redirect broker** or an **alternate IdP
with dynamic registration**; secret isolation is available via the day-2 `hosted-cluster-sourced`
pattern ([OCPSTRAT-2173](https://redhat.atlassian.net/browse/OCPSTRAT-2173)).

**This is the single biggest open risk in the study. Full analysis — options, security, day-2,
broker design, private-cluster handling — lives in `CONSOLE_AUTH_OPTIONS.md` §7** (tracked as the
open Gap 4).

**D3 — Hostname: `console.<domain>`, same pattern as `api.<domain>` / `oauth.<domain>`.**
Rides shared router SNI + public LB / GCP PSC + external-dns. Sets KAS `ConsolePublicURL` for the
`oc-oidc` login command. This replaces the classic `console-openshift-console.apps.<basedomain>`
guest-ingress hostname. **No per-cluster OIDC redirect-URI registration** in the GCP model: Google
is the issuer with a pre-existing shared OAuth client (`CONSOLE_AUTH_OPTIONS.md` §7), so there is no
`https://console.<domain>/auth/callback` whitelisting step the console port provisions
(earlier drafts wrongly implied one).

**TLS — reuse the api-server cert mechanism (decision (a)).** The console serves its own cert at
the pod (router is passthrough). Use the **same mechanism api-server uses on GCP HCP**: a
cert-manager-issued **wildcard** cert `*.<domain>` supplied as a Secret alongside the
HostedCluster and referenced like `spec.configuration.apiServer.servingCerts.namedCertificates`
(`servingCertificate.name: <secret>`). Since `console.<domain>` shares the base domain, the
existing api wildcard **already covers it** — the console component simply reuses that secret
when present; otherwise it serves a self-signed default (dev only). No `pki/console.go`
service-ca leaf.

*Possible evolution (decision (b)):* add a dedicated console serving-cert config field (a console
equivalent of `namedCertificates`) so an operator can point the console at a distinct
secret/leaf independent of the api wildcard. Not needed initially; (a) exploits the wildcard with
minimal API surface.

**D4 — Coexistence: full replacement, gated by capability + placement flag (see §18, Gap 7).**
Two orthogonal switches: (1) the **`Console` capability gate is kept unchanged** — disabled means
no console anywhere; (2) a **new placement flag** (keyed on GCP platform today, could become a
custom flag) decides where console runs when the capability is enabled. Placement **off**
(default) = today's path (guest CVO deploys console-operator + operand in the guest), zero change
for existing consumers. Placement **on** = console-operator + operand run **control-plane-side**;
the guest CVO console manifests are stripped from the payload (the conditional
`preparePayloadScript` mechanism, `cvo/deployment.go:208-275`), applied **only when the flag is
on** — not unconditionally. Because the capability stays enabled, the control-plane operator must
still publish guest `clusteroperator/console` + `console.config .status.consoleURL`. Plugins/day-2
addons keep running data-plane; the control-plane console-operator reconciles guest ConsolePlugin
CRs and the bridge proxies plugin assets into the guest via konnectivity.

**D5 — Image overrides for local dev.**
The CPO component must allow overriding the console bridge and console-operator images (env/override map) so developers can run patched images before upstream changes merge.

---

## 8. Work breakdown (HyperShift side)

**A. Port console-operator into CPO v2 component**
- `control-plane-operator/controllers/hostedcontrolplane/v2/consoleoperator/`.
- Vendor/adapt `github.com/openshift/console-operator`; reconcile against the guest kubeconfig (cnov2/ingressoperatorv2 pattern).
- Register in `hostedcontrolplane_controller.go` `registerComponents`, gated by `ConsoleCapability`.
- Bridge Deployment runs in the HCP namespace.
- Support image overrides (D5).

**B. Bridge config (off-cluster + auth-agnostic)**
- `apiServerURL` → guest KAS in-namespace Service; mount guest SA token + kube CA.
- `-user-auth=oidc` (GCP issuer, CA) with the oauth path retained.
- `-base-address` → `https://console.<domain>` (D3).

**C. Konnectivity proxy sidecar on the bridge**
- Inject a socks5/dual sidecar; set `HTTP(S)_PROXY` for the OIDC issuer (if needed) + plugin-asset proxying into the guest.

**D. Exposure (shared-router rails; oauth path as template)**

| Piece | File / function |
|---|---|
| ServiceType const `Console` + CRD enum | `api/hypershift/v1beta1/hostedcluster_types.go:1075-1095`, marker :1016 |
| Publishing strategy handling + validation | like `oauth/service.go:49-81`; `hostedcluster_controller.go:4399` |
| Service + Route reconcile, `HCPRouteLabel`, TLS passthrough | mirror `reconcileOAuthServerService` `infra/infra.go:338-403`; optional named router backend `v2/router/config.go:126-131` |
| `InfraStatus.ConsoleHost` populate | `infra/infra.go:137` pattern |
| Cert: reuse api-server wildcard named-cert (D3 (a)); self-signed default | mount the cert-manager `*.<domain>` secret referenced like `apiServer.servingCerts.namedCertificates`. **Not** `pki/console.go`/service-ca. |
| CLI hostname + private ExternalName svc | `cmd/cluster/gcp/create.go:318,322`; `psc_endpoint_controller.go:458` |

Constraint: console TLS terminates at the console pod; it must serve the SNI hostname matching its Route host.

**E. Plugin reconciliation across planes**
- console-operator (control-plane) watches guest ConsolePlugin CRs → injects plugin `name→svc.cluster.local` endpoints into `console-config.yaml`.
- Bridge reaches those guest Services via konnectivity socks5 (generic, no per-plugin work).

**F. Remove data-plane console + console-operator — conditional on the placement flag (§18, Gap 7)**
- Strip the guest console-operator payload manifests via the CVO `preparePayloadScript` mechanism
  (`cvo/deployment.go:208-275`) — the same pattern as the `!oauthEnabled` console-manifest removal
  (`:240-242`) and per-platform pruning. Applied **only when the placement flag is on**, not
  unconditionally.
- **Keep the `Console` capability gate unchanged** (disabled = no console anywhere; enabled +
  placement-off = today's guest path).
- For clusters flipping placement off→on, actively delete the already-applied guest
  console-operator/operand via the `resourcesToRemove`/`0000_01_cleanup.yaml` path (`:243-273`).
- **Still publish guest status** from the control-plane operator (`clusteroperator/console`,
  `console.config .status.consoleURL`) since the capability remains enabled.
- Confirm exact release-payload filenames (dev-repo names are re-prefixed as
  `0000_50_console-operator_*` in the payload). **Note:** the blanket `rm *_deployment.yaml`/
  `*_servicemonitor.yaml` (`:214-215`) operates on the `$payload/manifests/` copy (`:213`), NOT
  `release-manifests/` where the console-operator payload lives; `manifestsToOmit` (`:238`) and
  the oauth strip (`:240-242`) target `release-manifests/`. `manifestsToOmit` currently lists NO
  console files, so today the ONLY console removal is `0000_50_console-operator_01-oauth.yaml`
  when oauth is off. The placement-on strip must therefore add console workload files to a
  release-manifests removal path (extend `manifestsToOmit` conditionally, or a placement-gated
  `rm` mirroring the oauth line) — the blanket `:214` strip does not cover them.

**G. Config feedback**
- Update KAS `ConsolePublicURL` (`kas/params.go:77`, `kas/config.go:151`) to `https://console.<domain>`.
- OIDC console client: provision an entry (`openshift-console`/`console`) in the HC auth spec when
  Console is enabled; HCCO copies the secret to the guest (`resources.go:1566+`). The client model
  itself is the open blocker — see `CONSOLE_AUTH_OPTIONS.md` §7.

**H. Tests**
- Unit: component config gen, sidecar injection, plugin endpoint injection, OIDC config, Service/Route + label, cert SANs.
- E2E (GCP): OIDC login + browse; **zero-node** scenario; data-plane plugin loads via konnectivity; public LB + private PSC exposure paths.
- Fleet-rollout check: console config is control-plane-only; verify it does not feed any NodePool config hash (`hashStruct`/`configHash`) per repo rule.

---

## 9. Upstream code changes (console / console-operator)

This work is **not** all in HyperShift. Two upstream repos are affected.

### 9.1 Bridge (`openshift/console`) — near-zero

- **Off-cluster mode is first-class and production-usable** (`cmd/bridge/main.go:514-612`). Points at an arbitrary guest KAS via `-k8s-mode-off-cluster-endpoint` (`main.go:515`), mounts an SA token via `-k8s-mode-off-cluster-service-account-bearer-token-file` (`main.go:533-535`), trusts the guest CA via `-ca-file` (`main.go:125,727`). No in-cluster assumption blocks off-cluster use (`kubernetes.default.svc` + in-cluster SA paths are only in the in-cluster branch, `main.go:420-440`). The core off-cluster flags carry no dev-only warning. The **bearer token file** this flag wants is produced by the standard HyperShift token-minter sidecar — no new provisioning path (see §16).
- **OIDC is production and needs no oauth-server** (`authoptions.go:72`, validated `:199-203`; discovery `auth.go:242-257`). The openshift oauth-server path is a separate branch never hit in oidc mode.
- **One optional code change**, needed **only** if plugin assets/i18n are served through the konnectivity socks5 proxy: the plugin asset transport is hand-built with no proxy (`server.go:520-525`) and ignores `HTTP_PROXY`. Fix: add `Proxy: http.ProxyFromEnvironment` at `server.go:524`. The plugin *API proxy* (`/api/proxy`) already honors proxy env (`server.go:555`, `proxy.go:59`). Core console (no plugins) needs **zero** bridge changes.
- **Optional:** set `UseProxyFromEnvironment: true` on off-cluster thanos/alertmanager configs (`main.go:558-594`) for websocket monitoring streams.

### 9.2 console-operator (`openshift/console-operator`) — the real work (split-client refactor)

Today the operator is single-clientset: it creates the operand Deployment AND all config CRs (operator config, Route, OAuthClient, `config.openshift.io` reads, `openshift-config-managed` configmaps) against **one** cluster (`starter.go:94-129`). Our topology needs the operand Deployment on the **management** cluster but the config CRs on the **guest**. That split is unsupported.

Already available (no change):
- `--kubeconfig` via library-go controllercmd (`pkg/cmd/operator/cmd.go:19-25`) — can point at a remote cluster.
- OIDC handled: OAuthClient creation is skipped when `authentication/cluster` type != OAuth (`oauthclients.go:125-132`); bridge config emits `authType=oidc` (`config_builder.go:202-243`).
- No hardcoded in-cluster host / SA path / `kubernetes.default.svc` in operator code.

Must change (file:line):
1. **Dual clients** — split `starter.go:94-129` into management (operand: Deployment/Service/SA/PDB) vs guest (config CRs, Route, OAuthClient, `config.openshift.io`, config-managed configmaps). Thread both through `NewConsoleOperator` (`starter.go:260-295`) and controller constructors.
2. **Operand namespace configurable** — Deployment currently targets const `api.TargetNamespace="openshift-console"` (`api.go:41`; sync `sync_v400.go:377-383`; informer `starter.go:133-137`). Must inject the per-HostedCluster control-plane namespace.
3. **Off-cluster bridge wiring in the generated Deployment** — generation (`deployment.go:395-419`, base `console-deployment.yaml:59-63`) passes no `-k8s-mode`/`-user-auth` and relies on the implicit in-cluster SA. Must emit `-k8s-mode=off-cluster -k8s-mode-off-cluster-endpoint=<guestKAS>` + mount a guest SA token/CA (or a guest kubeconfig).
4. **Namespace consts → params** — `api.go:25,30,41,57` consts are consumed pervasively; the largest mechanical change.

**Upstream vs override:** the split-client refactor is intended to land **upstream** in `openshift/console-operator`. Until it merges, HyperShift must allow **image overrides** (D5) so a patched operator/bridge can be run in dev.

---

## 10. Risks / open items

1. **console-operator split-client refactor** — the primary engineering cost; must be designed upstream-acceptable (generic "manage console on a remote cluster" feature, not HyperShift-specific hacks).
2. **Hostname migration (D3)** — console URL changes from `console-openshift-console.apps.<basedomain>` to `console.<domain>`; affects `ConsolePublicURL` and bookmarks. Confirm acceptable to consumers.
3. **Plugin/monitoring reachability at scale** — konnectivity proxy path works generically; validate performance for plugin asset serving; the `server.go:524` bridge fix is required for asset proxying.
4. **oauth return (D2)** — keep auth pluggable; if oauth returns, re-enable the `console` OAuthClient CR + redirect wiring; no exposure rework.
5. **TLS at pod** — the router is passthrough and presents no cert; the console must serve a
   **publicly-trusted** cert whose SAN covers the console Route host (browser client). Resolved
   by reusing the api-server cert-manager wildcard `*.<domain>` secret (D3 (a)); self-signed
   default is dev-only and will trigger browser warnings.
6. **Image-override maintenance (D5)** — a temporary dev affordance; ensure it does not become the production default before upstream merges.

---

## 11. Reference: oauth exposure path (template, end-to-end)

1. ServiceType `OAuthServer` `hostedcluster_types.go:1082`
2. Strategy read `netutil.ServicePublishingStrategyByTypeForHCP` `infra.go:339`; valid-strategy `oauth/service.go:49-81`
3. Deployment (v2) `v2/oauth/component.go:38-84`
4. Service + Routes `infra.go:338-403`, `oauth/route.go:12-33`
5. Hostname `netutil.ReconcileExternalRoute` `support/netutil/route.go:102-127`
6. Cert `pki/oauth.go:12-21`, caller ~`:1651` — NOTE: oauth uses an internal-CA leaf (its
   clients are `oc`/in-cluster, not a browser). **Console does NOT follow this**; it reuses the
   api-server publicly-trusted wildcard cert instead (see D3 (a)), because its client is a
   browser.
7. Router SNI backends `v2/router/config.go:126-131`
8. Feedback: KAS issuer `v2/kas/oauth.go:46-52`; HCP status `hostedcontrolplane_controller.go:803`

---

## 12. Cleanup note

Shallow clones left at `_console-research/`, `_console-operator-research/`, and
`_cluster-network-operator-research/` (openshift/cluster-network-operator, the CNO dual-cluster
precedent) — none gitignored. Remove before any commit:

```
rm -rf _console-research _console-operator-research _cluster-network-operator-research
```

---

## 13. Implementation reference — HyperShift CPO v2 `consoleoperator` component

Verified against the codebase. Template components: `ingressoperator` (operator that
reconciles the guest cluster via HTTPS konnectivity + per-SA kubeconfig) and `cno`.
Release payload confirmed to contain both `console` and `console-operator` image tags,
so `GetImage(...)` works out of the box.

### 13.1 Files to create

```
control-plane-operator/controllers/hostedcontrolplane/v2/consoleoperator/
    component.go        # ComponentName, options struct, NewComponent(), predicate
    deployment.go       # adaptDeployment (image env, args, volumes)
control-plane-operator/controllers/hostedcontrolplane/v2/assets/console-operator/
    deployment.yaml     # operand-operator workload; dir name MUST equal ComponentName
                        # set serviceAccountName: console-operator
    serviceaccount.yaml # mgmt SA the operator pod runs as (framework auto-applies)
    role.yaml           # HCP-namespace CRUD on operand objects — clone CNO's role.yaml (§19)
    rolebinding.yaml    # binds role.yaml -> serviceaccount.yaml (subject ns rewritten by framework)
    <other>.yaml        # optional extra manifests (auto-discovered)
```

The `serviceaccount.yaml`/`role.yaml`/`rolebinding.yaml` trio is the **management-side** RBAC that
lets the operator manage its operand in the HCP namespace (see §19.2). Guest-side RBAC is NOT
shipped here — it comes from the upstream console payload via CVO (§19.1).

Plus edits to:
```
support/capabilities/hosted_control_plane_capabilities.go   # add IsConsoleCapabilityEnabled
control-plane-operator/controllers/hostedcontrolplane/hostedcontrolplane_controller.go  # register
```

### 13.2 Asset embedding (no per-component embed directive)

A single global embed picks up every `v2/assets/*/*.yaml` (`v2/assets/assets.go:20-21`
`//go:embed */*.yaml`). The framework loads `<ComponentName>/deployment.yaml`
automatically (`assets.go:30-38,76-88`). **The asset directory name must exactly equal
`ComponentName`** — so `ComponentName = "console-operator"` ⇄ dir `assets/console-operator/`.
Extra manifests in the dir are auto-reconciled (`ForEachManifest`, `assets.go:143-157`);
to mutate one, register `WithManifestAdapter("<file>.yaml", component.WithAdaptFunction(...))`.

### 13.3 `component.go` — copy this shape (from `ingressoperator/component.go:15-91`)

```go
const (
	ComponentName = "console-operator"
	// Guest SA the operator authenticates as. VERIFY against payload manifests —
	// upstream console-operator SA is "console-operator" in ns "openshift-console-operator"
	// (console-operator manifests/06-sa.yaml, 07-operator.yaml:43).
	serviceAccountName      = "console-operator"
	serviceAccountNamespace = "openshift-console-operator"
)

var _ component.ComponentOptions = &consoleOperator{}
type consoleOperator struct{}

func (c *consoleOperator) IsRequestServing() bool        { return false }
func (c *consoleOperator) MultiZoneSpread() bool         { return false }
func (c *consoleOperator) NeedsManagementKASAccess() bool { return false } // reconciles GUEST only

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &consoleOperator{}).
		WithAdaptFunction(adaptDeployment).
		WithPredicate(isConsoleCapabilityEnabled).
		WithDependencies(oapiv2.ComponentName).
		InjectKonnectivityContainer(component.KonnectivityContainerOptions{
			Mode: component.HTTPS, // operator reaches only the guest KAS -> HTTPS. See §13.10.
			HTTPSOptions: component.HTTPSOptions{
				ConnectDirectlyToCloudAPIs: ptr.To(true),
			},
			KubeconfingVolumeName: "admin-kubeconfig", // note the upstream misspelling of the field
		}).
		InjectAvailabilityProberContainer(podspec.AvailabilityProberOpts{
			KubeconfigVolumeName: component.ServiceAccountKubeconfigVolumeName,
			RequiredAPIs: []schema.GroupVersionKind{
				// Gate on a console-relevant guest API before starting.
				{Group: "operator.openshift.io", Version: "v1", Kind: "Console"},
				{Group: "console.openshift.io", Version: "v1", Kind: "ConsolePlugin"},
			},
		}).
		InjectServiceAccountKubeConfig(component.ServiceAccountKubeConfigOpts{
			Name:          serviceAccountName,
			Namespace:     serviceAccountNamespace,
			MountPath:     "/etc/kubernetes",
			ContainerName: ComponentName,
		}).
		Build()
}

func isConsoleCapabilityEnabled(cpContext component.WorkloadContext) (bool, error) {
	return capabilities.IsConsoleCapabilityEnabled(cpContext.HCP.Spec.Capabilities), nil
}
```

`InjectServiceAccountKubeConfig` generates secret
`console-operator-service-account-kubeconfig` (`support/controlplane-component/kubeconfig.go:39-41`)
holding a kubeconfig that authenticates as the named GUEST ServiceAccount, mounted at
`/etc/kubernetes/kubeconfig` into the operator container. The operator reads
`KUBECONFIG=/etc/kubernetes/kubeconfig`. This is the console-operator's guest client.

Konnectivity sidecar (`support/controlplane-component/konnectivity-container.go`): image is
the CPO image (`GetImage(podspec.CPOImageName)`), container name `konnectivity-proxy-<mode>`,
serves proxy on `:8090`, mounts konnectivity client cert (secret `KonnectivityClientSecret`)
+ CA (configmap `KonnectivityCAConfigMap`) automatically. Set `HTTP(S)_PROXY=http://127.0.0.1:8090`
(HTTPS mode) or `socks5://127.0.0.1:8090` (Socks5) in the operator/bridge container env.

### 13.4 `deployment.yaml` essentials (mirror `assets/ingress-operator/deployment.yaml`)

- Main container image key: bare `image: console-operator` (framework resolves it), OR set
  via adapt env (§13.5). Namespace/labels per the HCP-namespace convention (the framework
  injects the namespace).
- `env: - name: KUBECONFIG` `value: /etc/kubernetes/kubeconfig`.
- Proxy env for reaching guest services/routes through konnectivity:
  `HTTP_PROXY`/`HTTPS_PROXY: http://127.0.0.1:8090`, `NO_PROXY` incl. management-local names.
- `volumes:` add `admin-kubeconfig` (secret `service-network-admin-kubeconfig`) referenced by
  the konnectivity sidecar's `KubeconfingVolumeName`.
- Command: `console operator --kubeconfig=/etc/kubernetes/kubeconfig` PLUS the new upstream
  split-cluster flags (see §14): `--operand-namespace`, `--guest-apiserver-endpoint`,
  `--console-image` (dev override).

### 13.5 `deployment.go` adapt (mirror `ingressoperator/deployment.go:13-58`)

```go
func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		podspec.UpsertEnvVar(c, corev1.EnvVar{Name: "RELEASE_VERSION", Value: cpContext.UserReleaseImageProvider.Version()})
		// operand (bridge) image runs in the HCP namespace on MGMT -> control-plane provider
		podspec.UpsertEnvVar(c, corev1.EnvVar{Name: "CONSOLE_IMAGE", Value: cpContext.ReleaseImageProvider.GetImage("console")})
		// operator's own image is set by the framework from ReleaseImageProvider via the YAML image key
	})
	return nil
}
```

Image-provider rule: use `cpContext.ReleaseImageProvider` for anything running in the HCP
namespace on the management cluster (operator + bridge + sidecars); use
`cpContext.UserReleaseImageProvider` only for images that run on guest nodes. Both honor dev
overrides automatically.

### 13.6 Capability gate (`support/capabilities/hosted_control_plane_capabilities.go`)

Add, mirroring `IsIngressCapabilityEnabled` (`:27-38`):

```go
func IsConsoleCapabilityEnabled(capabilities *hyperv1.Capabilities) bool {
	if capabilities == nil {
		return true
	}
	for _, disabledCap := range capabilities.Disabled {
		if disabledCap == hyperv1.ConsoleCapability { // api/.../hostedcluster_types.go:491
			return false
		}
	}
	return true
}
```

### 13.7 Registration (`hostedcontrolplane_controller.go`)

- Add import alias:
  `consoleoperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/consoleoperator"`
- Add one line to the second `append` block in `registerComponents` (`:242-297`), near the
  other operator components (`ingressoperatorv2.NewComponent()` is at `:283`):
  `consoleoperatorv2.NewComponent(),`

### 13.8 Image dev-overrides (D5) — already wired

No component code needed. HostedCluster annotation
`hypershift.openshift.io/image-overrides` (`api/.../hostedcluster_types.go:231-234`) flows to
`imageprovider.NewWithRegistryOverrides` (`imageprovider/imageprovider.go:69-79`), which
rewrites image refs by registry prefix. As long as images resolve through
`*ReleaseImageProvider.GetImage`, overrides apply. To point `console`/`console-operator` at
patched images, set the override map accordingly.

### 13.9 Builder API cheat-sheet (`support/controlplane-component/builder.go`)

`NewDeploymentComponent` :16, `WithAdaptFunction` :69, `WithPredicate` :74,
`WithManifestAdapter` :79, `WithDependencies` :92, `InjectKonnectivityContainer` :97,
`InjectAvailabilityProberContainer` :102, `InjectTokenMinterContainer` :109,
`InjectServiceAccountKubeConfig` :116, `Build` :143. `KonnectivityContainerOptions`,
`HTTPSOptions`, `Socks5Options` (incl. `ResolveFromGuestClusterDNS`, `DisableResolver`) live
in `konnectivity-container.go:20-82`.

### 13.10 Konnectivity mode per component — RESOLVED

Two modes exist (`konnectivity-container.go:20-28`): **HTTPS** (`konnectivity-https-proxy`) is an
HTTP CONNECT proxy that resolves-then-dials a **known endpoint** over the tunnel (the guest KAS);
**Socks5** (`konnectivity-socks5-proxy`) is a generic SOCKS5 server for **arbitrary** guest
ClusterIP services (`<svc>.<ns>.svc.cluster.local`). **Dual** injects both.

| Component | Mode | Reason | Precedent |
|---|---|---|---|
| **console-operator** | **HTTPS** | Talks only to the guest KAS to reconcile CRs (ConsolePlugin, Route, config.openshift.io, operator.openshift.io/Console). "Known endpoint over the tunnel." | `oapi` (`v2/oapi/component.go:52-54`), `ingressoperator` (`v2/ingressoperator/component.go:55-61`) |
| **console-bridge** | **Socks5** (Dual only if it also needs the resolve-before-dial KAS path in-pod) | Reaches **arbitrary** guest ClusterIP services (plugin backends, monitoring) — the SOCKS5 server's purpose. The bridge hits the guest KAS directly in-namespace (`kube-apiserver.<hcp-ns>.svc:6443`, in `NO_PROXY`), so pure Socks5 covers the plugin/monitoring path. | `olm/*` socks5; oauth **Dual** (`v2/oauth/component.go:71-81`) |

**`--resolve-from-guest-cluster-dns` — NOT needed** in the common case:
- Operator (HTTPS): not caller-settable; the HTTPS binary hard-codes it internally
  (`konnectivity-https-proxy/cmd.go:48`). No action.
- Bridge (Socks5): plugin/monitoring endpoints are **plain ClusterIP Services**, resolved by
  resolver **step 2** (`ResolveK8sService` → Service→ClusterIP via the guest API,
  `resolver.go:216-233`), which is independent of the flag. Set it **only** if the bridge must
  resolve **non-Service** names (headless/SRV/custom coreDNS records). Corroborated by `cno`
  reaching the guest network with `DisableResolver=true` (`v2/cno/component.go:58-64`).

**Correction:** the companion manifest showed the **operator** sidecar as socks5 with
`--resolve-from-guest-cluster-dns=true` — that is wrong (operator = HTTPS). The bridge sidecar
being socks5 is correct, but drop `--resolve-from-guest-cluster-dns` unless non-Service names are
required.

---

## 14. Implementation reference — upstream console-operator split-cluster refactor

Verified against the cloned `openshift/console-operator` and `openshift/console` repos. This
is the primary upstream work item. Goal: run the operator control-plane-side; create the
**operand** (bridge Deployment/Service/ConfigMap/Secret/PDB/SA) on the **management** cluster
(MGMT), while reconciling **config** objects (config.openshift.io CRs, OAuthClient,
ConsolePlugin, `openshift-config*` configmaps, Route) on the **guest** cluster (GUEST).

### 14.1 Entry point and client construction (today = single kubeconfig)

- Binary `cmd/console/main.go:18-43` → subcommand `operator` (`pkg/cmd/operator/cmd.go:17-33`),
  built on library-go `controllercmd.NewControllerCommandConfig("console-operator", ..., starter.RunOperator, ...)`.
  `controllercmd` registers `--kubeconfig` and yields **one** `*rest.Config` as
  `controllerContext.KubeConfig` (+ protobuf variant `.ProtoKubeConfig`).
- `RunOperator(ctx, controllerContext)` (`pkg/console/starter/starter.go:94`) builds all clients
  from that one config (`starter.go:96-129`): `kubeClient` (Proto), `configClient`,
  `operatorConfigClient`, `consoleClient`, `routesClient`, `oauthClient`, `dynamicClient`,
  plus `policyClient` (`:627`) and `operatorClient` (`:185-192`). Informer factories per
  namespace at `:131-208`.

### 14.2 Required change #1 — dual clients

**Paved-path precedent: CNO (`cluster-network-operator`).** This exact dual-cluster topology —
operator runs mgmt-side, operand on MGMT, config on GUEST, two clients in one process — is
already in production in this repo and upstream. CNO registers `--extra-clusters=management=<kubeconfig>`
(`cmd/cluster-network-operator/main.go:78-80`), builds a `map[string]*OperatorClusterClient`
(`pkg/client/client.go:98-125`), and selects per-object via `ClientFor(name)`
(`client.go:135-144`, `ManagementClusterName`/`DefaultClusterName` `names.go:230-234`). Console
adopts the **same flag idiom and deployment shape** (see STUDY_COMPONENTS_DEPLOYMENT_PATTERNS.md
"CNO's multi-cluster client"). **But** CNO routes objects to clusters via a
`network.operator.openshift.io/cluster-name` annotation + one generic `apply.ApplyObject`
(`apply.go:38`) — console-operator has **no** such central apply/routing (typed clients per
controller), so the second client must be threaded through constructors by hand. See §14.10 for
the resulting change-weight.

`controllercmd` provides only one kubeconfig. Add a second. Recommended: keep `controllercmd`
for MGMT (leader election, events, operand clients) and add a `--guest-kubeconfig` flag; load a
second `*rest.Config` in `RunOperator` and build a `guestKubeClient` + guest-scoped informer
factories. Then route each consumer per this table (verified in `starter.go`):

| Resource | Today | Target |
|---|---|---|
| Deployment/Service/ConfigMap/Secret/SA (`openshift-console`) | `kubeClient` + `kubeInformersNamespaced` (`:133`) | **MGMT** |
| PodDisruptionBudget | `policyClient` (`:627`) | **MGMT** |
| Route (`console`, `downloads`) | `routesClient` (`:116`) | **GUEST** |
| OAuthClient `console` | `oauthClient` (`:121`) | **GUEST** |
| config.openshift.io CRs (Infrastructure, Ingress, Proxy, Console, Authentication, FeatureGate, ClusterVersion, OAuth, APIServer) | `configClient` (`:101`) | **GUEST** |
| operator.openshift.io `Console` (spec/status) | `operatorClient`/`operatorConfigClient` (`:106,:185`) | **GUEST** |
| ConsolePlugin / ConsoleCLIDownloads / ConsoleNotifications | `consoleClient` (`:111`) | **GUEST** |
| `openshift-config` cm/secrets (logo, etc.) | `kubeInformersConfigNamespaced` (`:145`) | **GUEST** |
| `openshift-config-managed` cm (`console-public` write, `oauth-serving-cert`, `default-ingress-cert`) | `kubeInformersManagedNamespaced` (`:139`) | **GUEST** |
| Nodes (arch/OS for config) | `coreV1.Nodes()` off `kubeClient` (`operator.go:152`) | **GUEST** (guest workers) — confirm intent |

Note: `configClient`, `operatorConfigClient`, `consoleClient`, `routesClient`, `oauthClient`,
`dynamicClient`, `operatorClient` are **all guest-side** after the split (they manage guest
CRs). Only the `kubeClient`-backed core resources split between operand (MGMT) and config
namespaces (GUEST). So the cleanest cut is: **split `kubeClient` into `mgmtKubeClient`
(operand ns) and `guestKubeClient` (`openshift-config*` ns), and point all typed CR clients at
the guest config.**

### 14.3 Required change #2 — configurable operand namespace

Operand namespace is the const `api.TargetNamespace = "openshift-console"`
(`pkg/api/api.go:41`; also `OpenShiftConsoleNamespace :64`), consumed pervasively (informer
`starter.go:133`, deployment sync `sync_v400.go:377-383`). Parameterize it (flag/env) so the
operand lands in the per-HostedCluster HCP namespace. Related consts to review: `api.go:25-69`
(largest mechanical change). Operator-local ns `openshift-console-operator` (telemetry,
migration cleanup) stays where the operator runs.

### 14.4 Required change #3 — off-cluster bridge Deployment generation

- Base Deployment YAML `bindata/assets/deployments/console-deployment.yaml:59-63` has NO
  `-k8s-mode` flag → bridge defaults to `in-cluster` (`console cmd/bridge/main.go:108`), which
  would talk to the MGMT apiserver. Wrong for our topology.
- Inject off-cluster flags in `withConsoleContainerImage` (`pkg/console/subresource/deployment/deployment.go:395-419`,
  where `Containers[0].Command` is mutated), following the existing `withLogLevelFlag` pattern:
  ```
  --k8s-mode=off-cluster
  --k8s-mode-off-cluster-endpoint=<guest KAS URL>   # e.g. https://kube-apiserver.<hcp-ns>.svc:6443
  --k8s-mode-off-cluster-service-account-bearer-token-file=/var/run/secrets/guest/token
  --ca-file=/var/run/secrets/guest/ca.crt
  ```
  Bridge off-cluster wiring confirmed at `console cmd/bridge/main.go:514-542`; CA selection
  `:727-730`; auth apply `:738`.
- Add a guest-SA-token volume + CA to the operand Deployment (via `withConsoleVolumes`
  `deployment.go:277-363`). **Guest SA token provisioning/rotation is RESOLVED** (see §16): the
  CPO v2 component injects the standard HyperShift **token-minter sidecar**
  (`InjectTokenMinterContainer`, `TokenType: KubeAPIServerToken`, audience = `hcp.Spec.IssuerURL`)
  which mints a bound SA bearer token to a file that this flag consumes — the same pattern CNO
  uses. The `-ca-file` uses the guest KAS serving CA available in the HCP namespace. No new
  provisioning code.
- Note: `console-config.yaml` `masterPublicURL`/auth already derive from the GUEST
  `Infrastructure`/`Authentication` CRs (`configmap.go:58,65`; `config_builder.go:202-243,403-405`),
  so the browser-facing apiserver URL is already correct. The bridge's internal k8s **proxy
  target** (the `-k8s-mode-off-cluster-endpoint` above) is a separate concern from
  `masterPublicURL`.

### 14.5 Plugin discovery — no code change, runtime path only

Plugin endpoints are generated as `https://<svc>.<ns>.svc.cluster.local.:<port>`
(`getServiceURL` `pkg/console/subresource/configmap/configmap.go:255-262`), from ConsolePlugin
CRs via the guest `consolePluginLister` (`sync_v400.go:886-901`). URL generation needs no
change. The runtime path (MGMT bridge → guest `*.svc.cluster.local`) is solved by the
konnectivity socks5 sidecar + the bridge proxy-env fix (§9.1, §9.3) — see the DNS resolution
mechanism in §5.1.

### 14.6 OAuthClient skip-when-OIDC — already handled

`syncOAuthClient` (`oauthclients/oauthclients.go:226-258`) creates the guest OAuthClient, but
is skipped for OIDC at three layers: sync switch `oauthclients.go:125-132` (proceeds only for
`""`/`IntegratedOAuth`/`None`), the switched informer `controllers/util/informers.go:119-140`,
and the management-state gate `oauthclients.go:203-222`. So D2 (OIDC-first, oauth-return-later)
needs no new skip logic; when oauth returns, this path re-activates automatically.

### 14.7 Closest existing precedent — study it first

`IsExternalControlPlaneWithIngressDisabled` (`starter.go:250`) already handles an
external-control-plane topology: route/healthcheck controllers are skipped and `consoleURL`
comes from `operator.Spec.Ingress.ConsoleURL` instead of a Route (`starter.go:242-257`). This
is the nearest existing model for a HyperShift-style split and should be the starting point for
the refactor.

### 14.8 The thorniest piece — resourceSyncer

**What it does.** `getResourceSyncer`/`startStaticResourceSyncing` (`starter.go:739-776`) is a
library-go `ResourceSyncController` that copies two configmaps cross-namespace with ONE kube
client, watching source+dest and re-copying on change:

| ConfigMap | From | To | Purpose |
|---|---|---|---|
| `oauth-serving-cert` | `openshift-config-managed` | `openshift-console` | CA bundle so the bridge trusts the **integrated OAuth server's** serving cert |
| `default-ingress-cert` | `openshift-config-managed` | `openshift-console` | legacy ingress CA, superseded by oauth-serving-cert since 4.9 (`starter.go:604`) |

Both land in the operand ns and are mounted into the bridge at `/var/oauth-serving-cert`
(`deployment.go:606-608`), feeding the bridge's auth-server CA trust (`deployment.go:84-87`).
A third user is custom-logo sync (`sync_v400.go:866-884`). `resourcesynccontroller` assumes a
single client, so split mode (source=GUEST, dest=MGMT) needs a two-client variant.

**Why it is genuinely deferrable — but ONLY on the OIDC path (D2).** Verified in
`sync_v400.go:209-221`: `oauth-serving-cert` is fetched and mounted **only when
`authnConfig.Spec.Type` is `""`/`IntegratedOAuth`/`None`**. For **OIDC**, that switch arm never
runs, so the synced configmap has **no consumer** — syncing it is dead weight on our path.
`default-ingress-cert` is the older superseded variant, likewise unused. Custom-logo is
branding, non-core. So for **OIDC-first core console + plugins, resourceSyncer has no live
consumer** and can be **stubbed/omitted** in the first cut without breaking login, TLS, or
plugins. (Console TLS is independent regardless — it uses the reused api-server wildcard cert,
D3 (a).)

**How the auth-server CA is actually obtained under OIDC (no syncer).** `sync_v400.go:119-134`:
the operator reads the CA **directly** from `openshift-console/<oidcProvider.Issuer.
CertificateAuthority.Name>` (a configmap named by the `Authentication` CR) via a plain lister
`Get` — **not** through resourceSyncer. In our GCP topology the OIDC issuer is a public Google
endpoint with a well-known CA, so `CertificateAuthority.Name` is typically empty
(`:130-134`, "no lookup required") and no CA configmap is needed at all. This is why OIDC login
works without the syncer.

**How resources get synced when we DO need it (integrated OAuth returns, D2 phase 2).** Three
options, preferred order:
1. **Direct fetch, no syncer (cleanest for single items).** Mirror the OIDC path above: read the
   cert from the GUEST lister and mount it via a generated/projected configmap in the operand
   ns. Fewer moving parts than a sync controller; the operator already does this for the OIDC CA.
2. **Two-client syncer variant.** Source = GUEST `openshift-config-managed` (where the guest
   cluster-authentication-operator publishes `oauth-serving-cert`), dest = MGMT operand ns —
   the §14.2 `guestKubeClient`→`mgmtKubeClient` split applied to configmap copying. Requires a
   two-client fork of `ResourceSyncController` or a small bespoke copy-controller.
3. **CPO/HCCO provides it.** The guest CA material is HCP-managed and already exists
   control-plane-side; the bridge mounts the mgmt-side copy directly, sidestepping cross-cluster
   sync entirely — consistent with D3 (a).

**Correction to earlier framing:** resourceSyncer is not universally unnecessary. It is
deferrable *specifically because we are OIDC-first*; when integrated OAuth is added back, option
(1) or (2) is required.

### 14.9 Unverifiable items requiring design decisions (not code reads)

1. ~~Guest SA token provisioning/rotation~~ — **RESOLVED**, see §16. Use the standard
   token-minter sidecar (`KubeAPIServerToken`) for the bridge and `InjectServiceAccountKubeConfig`
   for the operator; both framework-rotated, no new code.
2. ~~Whether node arch/OS should come from guest vs mgmt nodes~~ — **RESOLVED: GUEST.** The node
   list (`operator.go:152` → `getNodeComputeEnvironments` `sync_v400.go:930-947`) feeds
   console-config `nodeArchitectures`/`nodeOperatingSystems` (`config_builder.go:420-425`), which
   is **UI metadata** — the `oc`/`kubectl` download page arch/OS options and the Lightspeed gate
   (all-arches ∈ `{amd64}`, `config_builder.go:692-711`) — **not** any container-image selection.
   Must read the **guest** worker fleet; MGMT nodes would advertise the wrong (management) arch.
   Multi-arch images are irrelevant here (no image is chosen). `getNodeComputeEnvironments`
   already tolerates an empty/early node list (warns, disables Lightspeed, default downloads), so
   "guest, tolerate empty at early reconcile" is complete. Fix rides the §14.2 client split.
3. ~~Exact console-operator guest SA name/namespace to authenticate as~~ — **RESOLVED: use the
   CVO-created SAs, not a bespoke one.** `InjectServiceAccountKubeConfig` mints a cert for
   `system:serviceaccount:<ns>:<name>`; the RBAC that makes it usable is **subject-pinned** in the
   kept guest payload (§19.1). So the values are dictated, not chosen:
   **operator** = `console-operator`/`openshift-console-operator` (`06-sa.yaml`; bindings
   `04-rbac-rolebinding-cluster.yaml:15-18,56-59`); **bridge/operand** (§16 token-minter) =
   `console`/`openshift-console` (binding `:77-80`). Any other name → authenticates with zero
   bindings → `Forbidden`. Mirrors ingress-operator authenticating as its own upstream SA.
   Optional future: tighten the guest ClusterRole to config-only (superset in split mode).

**Verified finding — `oidcClients` status write is NOT mandatory for login.** The operator's
`oidcSetupController` / `cliOIDCClientStatusController` write `authentication.config/cluster
.status.oidcClients` (component=console/cli). This is **status/observability only**: the bridge
never reads it (it does OIDC directly against the external issuer using the clientID/secret from
`.spec.oidcProviders`), and guest KAS token acceptance is driven by its spec-derived
structured-authentication OIDC config, not by this status field. Implication for the split
refactor: these two controllers are **lower-risk** and their guest status writes can be
**deferred** if awkward in split mode without breaking OIDC login. (One item outside both repos
worth confirming: guest KAS structured-auth OIDC config lives in the kube-apiserver/auth-operator
— in HCP it is CPO-controlled — and nothing found makes it depend on the status write.)

### 14.10 Change-weight in the console-operator codebase

Honest sizing of the upstream refactor. Verdict: **medium-heavy mechanical refactor**,
not a rewrite and not a copy-paste of CNO. Larger than CNO's own port because console-operator
was not designed multi-cluster (no annotation routing, no central apply — typed clients wired
per controller; verified: `ClientFor`/`ApplyObject`/`cluster-name` absent from the tree).

| Change | Location | Weight | Notes |
|---|---|---|---|
| Add `--guest-kubeconfig`, load 2nd `*rest.Config`, split `kubeClient`→`mgmtKubeClient`+`guestKubeClient`, re-home typed CR clients to guest | `starter.go:94-129`, `cmd/operator/cmd.go` | **Moderate** | Contained to client construction |
| Re-point guest-namespaced informer factories (openshift-config, -config-managed, -console-operator) to guest; operand factory (openshift-console→HCP ns) stays mgmt | `starter.go:133-183` | **Moderate** | ~6 factories |
| Thread the correct client (mgmt vs guest) through controller constructors | ~14 `New*Controller` + `NewConsoleOperator` (`starter.go:260-295`) | **Bulk of the work** | 92 client-var refs in `starter.go` to audit; **no logic change**, only which existing client each call receives |
| Parameterize operand namespace (`api.TargetNamespace` const → flag/param) | `api/api.go` + consumers | **Wide but mechanical** | **48 refs across 19 files** (verified); no logic change |
| Inject off-cluster bridge flags | `deployment.go:395-419` | **Small** | §14.4 |
| `resourceSyncer` single→two-client variant | `starter.go:739-776` | **Only real design unknown** | **Deferrable/stubbable** — core console+plugins don't need it (§14.8) |

**Summary:** ~4-6 files of real edits (`starter.go`, `cmd/operator`, `deployment.go`, api consts,
optionally the syncer) plus a mechanical const→param sweep across ~19 files. No controller
*logic* is rewritten. The single biggest line item is the `api.TargetNamespace` sweep; the only
genuine *design* work is resourceSyncer (deferrable) and the upstream flag API (§14.3). Deployment/
topology/framework risk is ~zero (fully paved by CNO); code risk is a moderate, well-bounded
mechanical refactor. Feasible as a focused PR series, not a multi-quarter effort.

---

## 15. Recommended implementation sequencing

Ordered to produce a testable core console early and defer the hard/optional parts. Each
phase references the detailed sections above.

**Phase 0 — spike / de-risk (no upstream dependency).**
- Manually deploy the bridge in `off-cluster` mode against a guest KAS with OIDC (using the
  companion manifest as a starting point) to confirm login + browse end-to-end from the HCP
  namespace. Proves §3/§4/§9.1 in a live cluster before writing controllers.

**Phase 1 — HyperShift API + exposure plumbing (independent of console-operator).**
- Add `ServiceType` const `Console` + CRD enum; publishing-strategy validation (§8.D row 1-2).
- Wire console TLS to reuse the api-server cert-manager wildcard `*.<domain>` secret (D3 (a);
  no `pki/console.go`) + populate `InfraStatus.ConsoleHost` (§8.D rows 4-5).
- Add the Service + passthrough Route (labeled `HCPRouteLabel`) mirroring
  `reconcileOAuthServerService`; confirm the HCP router adopts the SNI backend (§6, §8.D row 3).
- CLI hostname wiring + private ExternalName svc for GCP PSC (§8.D row 6).
- Feed `console.<domain>` into KAS `ConsolePublicURL` (§8.G).
- Verifiable with a placeholder backend before the operator exists.

**Phase 2 — console bridge upstream change (small).**
- Land the `server.go:524` `Proxy: http.ProxyFromEnvironment` fix (only needed for plugin
  asset proxying); optional websocket monitoring proxy env (§9.1). Independent PR;
  image-overridable for dev (D5) until merged.

**Phase 3 — console-operator split-cluster refactor (upstream, the big one).**
- Dual clients (§14.2) → configurable operand namespace (§14.3) → off-cluster bridge flag
  injection (§14.4). Use `IsExternalControlPlaneWithIngressDisabled` as the model (§14.7).
- Defer `resourceSyncer` two-client variant (§14.8) — for the core console it only affects
  cert/logo syncing; stub or single-cluster it initially.
- Guest SA auth is already decided (§16): token-minter sidecar for the bridge,
  `InjectServiceAccountKubeConfig` for the operator. No provisioning design needed.

**Phase 4 — CPO v2 `consoleoperator` component (HyperShift).**
- Create `component.go` / `deployment.go` / `assets/console-operator/deployment.yaml` (§13).
- Add `IsConsoleCapabilityEnabled` + register in `registerComponents` (§13.6-13.7).
- Wire konnectivity sidecar + guest SA kubeconfig injection (§13.3). Uses the Phase-3 operator
  image (dev-overridable).

**Phase 5 — full replacement + plugins.**
- Strip the guest console-operator payload via the conditional `preparePayloadScript` mechanism,
  gated on the placement flag; keep the capability gate; keep publishing guest console status
  (§8.F, §18, Gap 7).
- Enable plugin reconciliation across planes (§8.E, §14.5) + validate asset proxying via
  konnectivity (depends on Phase 2).

**Phase 6 — tests + rollout safety.**
- Unit + E2E (§8.H): OIDC login, browse, zero-node, public LB + private PSC, data-plane plugin
  load. Confirm no NodePool config-hash impact (control-plane-only).

**Critical path:** Phase 3 (console-operator refactor) gates Phase 4/5 and is the longest lead
item because it must be accepted upstream. Phases 1-2 can proceed in parallel immediately.
Phase 0 should happen first to validate assumptions cheaply.

---

## 16. Guest kube-apiserver authentication — RESOLVED

Both the console-operator and the console bridge authenticate to the **guest kube-apiserver**
using existing, standard HyperShift mechanisms. No new provisioning or rotation code is needed;
both are handled by the CPO v2 component framework and auto-rotated. Verified against the
codebase.

### 16.1 Two standard mechanisms (verified)

1. **Per-SA client-cert kubeconfig** — `InjectServiceAccountKubeConfig`
   (`support/controlplane-component/builder.go:116`; `kubeconfig.go:21-89`; `pki/kas.go:76-108`).
   Framework generates secret `<name>-service-account-kubeconfig`, a client-cert kubeconfig
   whose identity is `system:serviceaccount:<ns>:<name>` (cert signed by the cluster CSR-signer
   CA that the guest KAS trusts), server = service-network KAS, mounted at a chosen `MountPath`,
   read via `KUBECONFIG`. **ingress-operator uses this verbatim**
   (`ingressoperator/component.go:68-73`).

2. **Bound SA bearer token via token-minter** — `InjectTokenMinterContainer`,
   `TokenType: KubeAPIServerToken` (`support/controlplane-component/token-minter-container.go:68-73,121-122`;
   builder `:109`). A sidecar mints a bound SA token (audience = `hcp.Spec.IssuerURL`, accepted
   by the guest KAS via OIDC) to a **token file**, refreshed before expiry. **CNO uses this**
   (asset-wired: `assets/cluster-network-operator/deployment.yaml:72-193`; `cno/deployment.go:31-37`).

For GCP **cloud** resources (not the kube API) the standard is GCP Workload Identity Federation
via `InjectTokenMinterContainer{CloudToken}` + a `*-creds` external_account JSON
(`support/gcputil/gcputil.go:34-67`). **The console needs no guest cloud access, so WIF does not
apply to the console.**

### 16.2 Decision

| Component | Mechanism | Why |
|---|---|---|
| **console-operator** (reconciles guest CRs) | `InjectServiceAccountKubeConfig` (#1) | Exactly the ingress-operator pattern; already proposed in §13.3. Least-privilege named SA, framework-rotated. |
| **console bridge** (guest KAS as k8s proxy target) | token-minter sidecar `KubeAPIServerToken` (#2) | The bridge flag `-k8s-mode-off-cluster-service-account-bearer-token-file` wants a **bearer token file** — exactly what the token-minter produces. Same as CNO. `-ca-file` = guest KAS serving CA from the HCP namespace. |
| **konnectivity sidecar** (Service→ClusterIP resolution, §5.1) | reuse the operator's per-SA `service-account-kubeconfig` | One guest credential, reused via `KubeconfingVolumeName` (as ingress-operator points konnectivity at `admin-kubeconfig`). |

- **Owner:** CPO v2 component framework at component-build time. Not HCCO, not a hand-synced
  secret.
- **Rotation / failure:** framework-managed. Cert kubeconfig auto-rotates; token-minter sidecar
  refreshes the bound token (restart policy `token-minter-container.go:82-112`); the bridge
  re-reads the token file.
- **Discard** the companion manifest's hand-rolled `console-guest-kubeconfig` Opaque secret with
  `REPLACED_BY_CPO` placeholders — it is not how HyperShift does this.

### 16.3 Follow-ups feeding other sections
- **Guest SA name/namespace** (§14.9 item 3): use the CVO-created SAs —
  operator = `console-operator`/`openshift-console-operator`; bridge = `console`/`openshift-console`.
  Dictated by the subject-pinned RoleBindings in the kept payload, not chosen (§19.1).
- **Guest RBAC** for both SAs: §19.1 — CVO applies the upstream RBAC as-is; bridge SA minimal (user
  token carries browse), operator SA via its own upstream ClusterRole.
- **Operator konnectivity mode:** §13.10 — operator → HTTPS; bridge → Socks5.

---

## 17. Considered alternative — direct-CPO ("start simple"), and why rejected

A HyperShift-only path was evaluated as a way to avoid the upstream console-operator refactor
(§14). It is recorded here as a rejected alternative, plus the findings it produced that
**de-risk the chosen Option A** (§16, D1).

### 17.1 The alternative

**Direct-CPO:** a CPO v2 component creates and owns the console **operand** directly
(Deployment/Service/ConfigMap/SA/PDB/session/cert) in the HCP namespace, **without** porting the
console-operator. Two flavors:
- **C-static:** hand-written `console-config` (OIDC + clusterInfo), no dynamic guest reconciliation.
  Core console only; plugins would need manual config / restart. **Rejected** — not full-featured.
- **C-dynamic:** direct operand **plus** a bespoke CPO reconciler that watches guest ConsolePlugin
  CRs, regenerates `console-config`, and rolls the bridge (to get full plugin support). **Rejected**
  — this re-implements the console-operator's core logic (`GetAvailablePlugins` →
  `getPluginsEndpointMap` → config-hash → rollout, entangled in `sync_v400`) inside CPO, and would
  hand-maintain `console-config` generation against upstream indefinitely.

### 17.2 Why rejected (and what it confirmed)

**Rejected because:** full-featured console (dynamic plugin discovery + custom logo + upgrade
banners + guest status) is a requirement, and the maintainable, release-evolving way to get it is
the operator itself — not a parallel CPO reimplementation. Avoiding **code duplication** and
**tracking future OCP releases automatically** are the deciding factors (D1).

**What the investigation confirmed (these de-risk Option A):**
1. **`oidcClients` status write is not mandatory** for OIDC login — status/observability only
   (§14.9). Lowers the risk of the oidcSetup controllers in split mode.
2. **`console-config` schema is stable and leniently parsed** — versioned
   `console.openshift.io/v1`, ~2–4 optional additive fields/yr, parsed non-strict (`yaml.v2`,
   `serverconfig/config.go:145`); a lagging config emitter degrades to "missing feature", never
   a bridge crash. Confirms the config_builder logic being ported is a slow-moving target.
3. **Operand objects are ~80% reusable across the two approaches** — console-operator's
   `ApplyDeployment` adopts a same-name Deployment and overwrites its spec (no ownerRef guard),
   so had we started with C-then-A, the operand would largely survive **provided** names +
   the immutable selector (`app: console`, `component: ui`) match. Two consequences carried into
   Option A: keep stock object names/labels; **never run two `Controller=true` owners on the
   same Deployment** (CPO-HCP ref vs operator-Console ref = reconcile ping-pong) — the ported
   operator is the sole operand owner.

**GKE constraints noted during the evaluation (apply to Option A too):**
- Operand namespace is the **HCP namespace**, not `openshift-console` — the ported operator's
  `--operand-namespace` must target it and every `api.TargetNamespace`/`OpenShiftConsoleNamespace`
  reference must route through it (partial wiring = split brain; §14.3).
- **No service-ca operator on GKE** → console serving cert uses the reused api-server cert-manager
  wildcard (D3 (a)), not a service-serving-cert.

---

## 18. Removing the data-plane console — capability gate + placement flag (Gap 7)

Full detail and open sub-items live in the companion `CONSOLE_CONTROL_PLANE_GAPS.md` (Gap 7).
Summary of the model and the verified mechanism:

### 18.1 Two orthogonal switches
1. **`Console` capability gate (kept unchanged).** Disabled → no console anywhere. Enabled →
   console exists; go to switch 2.
2. **Placement flag (new; GCP platform today, could become custom).** Off (default) → today's
   guest path (CVO deploys console-operator + operand in the guest). On → console runs
   control-plane-side; guest CVO console manifests are stripped.

These are independent: removal from the guest happens **only when placement=on**, never
unconditionally, and **never** by disabling the capability.

### 18.2 Mechanism (verified — `cvo/deployment.go:208-275`)
`preparePayloadScript(platformType, oauthEnabled, featureSet)` prunes the CVO payload before it is
applied to the guest. It already supports flag-conditional console pruning:
- blanket strips `*_deployment.yaml` / `*_servicemonitor.yaml` /
  `0000_50_cluster-update-console-plugin_*` — **in `$payload/manifests/`** (the `/manifests` copy,
  `:213-216`), NOT `release-manifests/`;
- removes the static `manifestsToOmit` list from **`release-manifests/`** with per-platform
  exceptions (`:151-206,232-239`) — this list currently contains **no console-operator files**;
- **precedent:** conditionally removes `0000_50_console-operator_01-oauth.yaml` from
  `release-manifests/` when `!oauthEnabled` (`:240-242`) — this is the ONLY console strip today;
- **implication:** the console-operator payload (`0000_50_console-operator_*`) lives in
  `release-manifests/` and is therefore untouched by the blanket `:214` strip. The placement-on
  workload removal is **net-new logic** in release-manifests (conditionally extend `manifestsToOmit`
  or add a placement-gated `rm` mirroring the oauth line);
- actively deletes already-applied objects via `resourcesToRemove` →
  `0000_01_cleanup.yaml` with `release.openshift.io/delete: "true"` (`:243-273`) — needed when a
  cluster migrates placement off→on.

### 18.3 Consequences to honor
- **Keep publishing guest status.** Capability stays enabled ⇒ the control-plane operator must
  still write guest `clusteroperator/console` and `console.config .status.consoleURL` (its guest
  client, §14.2), else the guest shows an enabled-but-missing console CO.
- **The operand never lives in the guest** when placement=on — CVO ships the *operator*, and the
  operand Deployment is created by the operator (now control-plane-side, in the HCP namespace).
- **Confirm real payload filenames** (dev-repo `07-operator.yaml` etc. are re-prefixed as
  `0000_50_console-operator_*`). The blanket `rm *_deployment.yaml` (`:214`) targets `/manifests`,
  not `release-manifests/` where console lives, and `manifestsToOmit` lists no console files today
  — so the console workload strip is net-new removal logic in release-manifests (see §18.2).

---

## 19. RBAC — guest-side and management-side

Two independent RBAC axes. Both reuse established patterns; verified against code.

### 19.1 Guest-side RBAC — CVO applies the upstream console manifests as-is

The upstream console-operator already ships all guest RBAC, annotated
`include.release.openshift.io/hypershift: "true"` + `capability.openshift.io/name: Console`, in
files **separate** from the workload. So we keep them in the CVO payload (strip only the workload,
§18/Gap 7) and CVO applies them unchanged. Identities and roles (verified in
`_console-operator-research/manifests/`):

| Guest identity (SA) | Namespace | Gets | Source |
|---|---|---|---|
| `console-operator` | `openshift-console-operator` | ClusterRole `console-operator` (config.openshift.io reads, consoles CRUD, oauthclients, consoleplugins CRD patch, clusteroperators/status, authentications/status) + namespaced Roles (openshift-config, -config-managed, -monitoring, -console) + `system:auth-delegator` + `extension-apiserver-authentication-reader` | `03-rbac-role-cluster.yaml:1-170`, `03-rbac-role-ns-*.yaml`, `04-rbac-rolebinding*.yaml` |
| `console` (bridge) | `openshift-console` | ClusterRole `console` (CRD/webhook/catalog/packagemanifest reads) + `console-configmap-reader` + `console-user-settings-admin` + `extension-apiserver-authentication-reader` + `system:auth-delegator` | `03-rbac-role-cluster.yaml:172-219`, ns roles, `04-rbac-rolebinding-cluster.yaml:102-122` |
| `downloads` | `openshift-console` | none (pure workload identity) | — |

**Auth delegation:** `console` and `console-operator` SAs are bound to `system:auth-delegator`
(`04-rbac-rolebinding-cluster.yaml:39-59,102-122`) → TokenReview + SubjectAccessReview. The
**bridge SA uses this only for the `/metrics` gate** (`console server.go:622-624`), NOT user
login. End-user resource requests forward the **user's** bearer token
(`middleware.go:31`, reverse proxy passthrough), so the bridge SA needs **no broad resource
access** — it stays minimal.

**Namespaces kept:** `02-namespace.yaml` (`openshift-console`, `openshift-console-operator`,
`openshift-console-user-settings`) stays in the payload — not to host workloads, but because the
guest **SA identities and namespaced RBAC** live there.

**Bifurcation — the `console` SA is dual-purpose:** its *workload* (operand Deployment/SA) is
management-side in the HCP namespace; its *guest identity* (auth-delegator + guest reads) must
exist guest-side in `openshift-console`. The bridge's §16 token-minter mints a token for the
**guest** `openshift-console/console` SA (created + bound by the kept CVO manifests). This is the
join between §16 (auth mechanism), §18 (CVO keep-list), and this section.

### 19.2 Management-side RBAC — ship a Role+RoleBinding+SA in the component assets (CNO pattern)

The ported operator runs on management and creates its operand (Deployment/Service/ConfigMap/
Secret/PDB/SA/Route) **in the HCP namespace**. It needs HCP-namespace RBAC for that. Verified:

- The CPO framework **auto-applies** any `serviceaccount.yaml`/`role.yaml`/`rolebinding.yaml` in
  `v2/assets/<component>/` to the HCP namespace, rewriting the RoleBinding subject namespace
  (`controlplane-component.go:274-311`, esp. `:289-294`; embed+walk `assets/assets.go:20-21,143-157`).
  It creates **nothing** on its own.
- **Precedent = cluster-network-operator** (the exact twin: an operator managing mgmt-namespace
  workloads). It ships `v2/assets/cluster-network-operator/{serviceaccount,role,rolebinding}.yaml`;
  CNO's `role.yaml` rules already mirror upstream console `03-rbac-role-ns-console.yaml`
  (deployments/services/configmaps/secrets/events/pods/replicasets in `""`+`apps`,
  `route.openshift.io/routes`, `policy/poddisruptionbudgets`).
- Action: clone CNO's trio into `v2/assets/console-operator/`, name the SA `console-operator`, and
  set `serviceAccountName: console-operator` on the deployment.
- Do **not** reuse the broad `control-plane-operator` SA (per-component isolation;
  `v2/assets/control-plane-operator/role.yaml` is bound only to the CPO SA).

ingress-operator ships **no** mgmt RBAC because its operands live in the guest (accessed via
kubeconfig) — that is *not* the console model; CNO is.
