# POC Plan: Core OpenShift Console Control-Plane-Side (GCP HCP)

**Status:** Draft plan — local, not committed
**Companions:** `CONSOLE_CONTROL_PLANE_STUDY.md` (feasibility + file:line refs), `console-control-plane-manifests.example.yaml` (illustrative full-featured manifests)
**Goal:** Prove the core console runs in an HCP namespace on the management cluster and is reachable from a browser, with the absolute minimum of moving parts. Then enumerate the follow-on tracks.

---

## 0. Scope discipline

This plan front-loads a **Minimum Viable POC (MVP)** that deliberately drops everything optional from the study:

- **No** console-operator (no dynamic plugin discovery, no config generation).
- **No** HyperShift/CPO integration (no v2 component, no capability/placement flag, no CVO strip).
- **No** OIDC (`-user-auth=disabled`, static token).
- **No** konnectivity sidecar (core console never touches guest ClusterIP services — plugins/monitoring only, which are out of scope).
- Manual DNS + manual cert + manual token, by hand.

Everything the study calls "optional and gated" (§3) is off. What remains is a stateless Go reverse-proxy pointed at the guest KAS, exposed over the existing router rails.

MVP verdict from the study: the bridge boots and serves the core UI (login + browse k8s/OCP resources) with only the k8s proxy target + a serving cert + static assets (§3, §4). All confirmed against the console source in `_console-research/`.

---

## 1. MVP — manual console into an existing HCP namespace

### 1.1 Objective

Deploy the console **bridge** container into an already-running HostedControlPlane namespace, pointed at that cluster's guest kube-apiserver, auth disabled, and confirm from a browser (or `curl`) that it serves the UI and can browse guest resources.

### 1.2 Preconditions

- A working HostedCluster on GCP HCP with a reachable guest KAS in-namespace at `kube-apiserver.<hcp-ns>.svc:6443`.
- Admin access to the management cluster (to `kubectl apply` into the HCP namespace).
- A guest cluster admin token or SA token (for the static bearer token; see 1.4).
- The console bridge image ref (from a release payload: `GetImage("console")`, or a dev build).

### 1.3 Bridge flags for MVP (verified in `_console-research/cmd/bridge/main.go`)

```
/opt/bridge/bin/bridge
  -listen=https://0.0.0.0:8443
  -base-address=https://console.<domain>
  -public-dir=/opt/bridge/static
  # off-cluster: talk to the guest KAS in-namespace
  -k8s-mode=off-cluster
  -k8s-mode-off-cluster-endpoint=https://kube-apiserver.<hcp-ns>.svc:6443
  # auth OFF for MVP: static token used for ALL requests
  -user-auth=disabled
  -k8s-auth-bearer-token=<GUEST_ADMIN_OR_SA_TOKEN>
  # TLS at the pod (router is passthrough)
  -tls-cert-file=/var/serving-cert/tls.crt
  -tls-key-file=/var/serving-cert/tls.key
  # DEV: skip guest KAS cert verification to avoid mounting the guest CA (optional)
  -k8s-mode-off-cluster-skip-verify-tls=true
```

Verified facts:
- `-user-auth=disabled` is a valid value (`cmd/bridge/config/auth/authoptions.go:72`); when disabled, `-k8s-auth-bearer-token` is "used for all requests" (`authoptions.go:38,79,226`).
- off-cluster branch builds the k8s proxy from `-k8s-mode-off-cluster-endpoint` and, if `-k8s-mode-off-cluster-skip-verify-tls`, skips guest KAS cert verification (`main.go:514-537`). This removes the guest-CA mounting step for the first spike.
- Production/next-step swaps `skip-verify` for `-ca-file=<guest KAS serving CA>` and `-k8s-auth-bearer-token` for `-k8s-mode-off-cluster-service-account-bearer-token-file`.

### 1.4 Static token (MVP shortcut)

For the first spike, use a guest cluster-admin token (e.g. from the guest kubeconfig, or `oc create token` against a privileged guest SA). Because auth is disabled, **every** browser session acts as that identity — dev only, never production. This sidesteps the entire §16 token-minter machinery for the MVP.

### 1.5 Serving cert (manual)

Browser client ⇒ the pod must serve a **publicly-trusted** leaf whose SAN covers `console.<domain>` (study §6, D3). MVP options, cheapest first:

1. **Reuse the api-server cert-manager wildcard `*.<domain>` secret** already present alongside the HostedCluster (study D3 (a)). Mount it as the serving cert. No new cert resource. **Preferred.**
2. If no wildcard exists, mint a leaf for `console.<domain>` from a publicly-trusted issuer manually and drop it in a `kubernetes.io/tls` secret.
3. Self-signed (browser warns) — acceptable only to prove the plumbing; not for a clean demo.

### 1.6 Exposure (manual DNS)

Path: `browser → public LB / PSC → HCP router → console pod` (study §6). Reuse the shared router rails:

- **Service** (ClusterIP) `console` → pod `:8443`.
- **Route** `console`, `tls.termination: passthrough`, labeled with `netutil.HCPRouteLabel` so the per-HCP HAProxy router adopts it as an SNI backend (`v2/router/config.go:109-136`).
- `spec.host: console.<domain>`.
- **DNS:** external-dns normally auto-registers `spec.host` in-zone (`support/netutil/route.go:124-125`). If external-dns isn't wired for arbitrary hosts in your env, **register the DNS record by hand** pointing `console.<domain>` at the same LB/PSC frontend used by `api.<domain>` / `oauth.<domain>`.

The example manifest objects 10 (Service) and 11 (Route) in `console-control-plane-manifests.example.yaml` are the MVP shapes — minus the konnectivity/OIDC/operator extras.

### 1.7 What to strip from the example manifest for MVP

Start from `console-control-plane-manifests.example.yaml`, delete/skip:
- Object 2, 3 (console-operator SA + RBAC) — no operator.
- Object 4 (guest kubeconfig secret) — replaced by static token flag.
- Object 5 (OIDC client secret) — auth disabled.
- Object 7 (session secret) — not needed with auth disabled.
- Object 8 (`console-config` ConfigMap) — flags-only bridge; no operator-generated config.
- In Object 9 (bridge Deployment): **remove the konnectivity socks5 sidecar**, all `HTTP(S)_PROXY`/`NO_PROXY` env, the guest-kubeconfig/oidc/session/config volumes. Keep only the bridge container + serving-cert volume. Swap the OIDC args for the 1.3 MVP flags.
- Object 13 (console-operator Deployment) — no operator.

Keep: Object 1 (bridge SA — optional; MVP pod identity), Object 6 (serving cert secret — the wildcard), Object 9 (trimmed bridge Deployment), Object 10 (Service), Object 11 (Route), Object 12 (PDB — optional).

### 1.8 Likely code change needed in console (MVP)

**Expected: zero.** Core console with no plugins needs no bridge changes (study §9.1). Off-cluster mode is first-class and production-usable.

Watch items (only if hit during the spike):
- The one known optional fix (`server.go:524`, add `Proxy: http.ProxyFromEnvironment`) is **plugin-asset-only** — not exercised by core console. Skip for MVP.
- If the bridge's k8s proxy needs a public browser-facing KAS URL for in-browser JS, add `-k8s-public-endpoint=https://api.<domain>:6443` (present in example manifest line 307). Verify during the spike whether browse works without it.

### 1.9 MVP acceptance criteria

- Pod runs 1/1 in the HCP namespace; `/health` returns 200.
- `https://console.<domain>` loads the console UI in a browser with no TLS warning (given cert option 1/2).
- The UI lists guest cluster resources (Projects/Pods/Nodes list — read via the static token through the guest KAS proxy).
- Works on a **zero-node** guest cluster (no workers), proving the control-plane-side claim (study §4).

### 1.10 MVP task checklist

1. Obtain console bridge image ref.
2. Obtain guest admin/SA token.
3. Create/identify the `*.<domain>` wildcard TLS secret in the HCP namespace.
4. Apply trimmed bridge Deployment + Service.
5. Apply passthrough Route labeled `HCPRouteLabel`; confirm router picks up the SNI backend.
6. Ensure `console.<domain>` DNS resolves to the LB/PSC frontend (external-dns or manual).
7. Browse from a client; validate 1.9.
8. Record: did browse work without `-k8s-public-endpoint`? Any bridge code change required? (feeds next steps)

---

## 2. Next steps (post-MVP tracks)

Each track is independent-ish and builds toward the study's Option A end-state. Ordered by increasing coupling. Detailed file:line references live in the study sections cited.

### 2.1 Track A — Authentication via user day-2 OIDC client-secret injection

Replace `-user-auth=disabled` + static token with real per-user OIDC (GCP), and stop relying on a cluster-admin token.

- Switch bridge to `-user-auth=oidc` with `-user-auth-oidc-issuer-url`, `-user-auth-oidc-client-id`, `-user-auth-oidc-client-secret-file` (study §9.1; example manifest lines 309-312).
- Replace static token with a per-request user token path; the bridge forwards the **user's** bearer token to the guest KAS (study §19.1, `middleware.go:31`). Guest KAS must accept the OIDC token via its spec-derived structured-authentication config (already live on GCP HCP per study D2).
- **Day-2 OIDC client secret injection:** use the HostedCluster auth spec `OIDCProviders[0].OIDCClients[]` (keyed `openshift-console`/`console`); HCCO copies the secret guest-side (`resources.go:1566+`). Study §8.G, D2.
- **Open blocker (study D2 / Gap 4):** Google web OAuth client creation is not automatable and forbids wildcard redirects. Provisioning a console client per hosted cluster without a manual Google step is the gating problem. Options (state-based redirect broker / alternate IdP with dynamic registration) live in `CONSOLE_AUTH_OPTIONS.md` §7. **This track cannot fully close until that is decided.**
- MVP-adjacent milestone: prove OIDC login end-to-end with a **manually pre-created** Google client (bypassing automation) before solving provisioning.

Deliverable: browser login as a real GCP identity, per-user RBAC enforced by the guest KAS.

### 2.2 Track B — console-operator plumbing (management-cluster side)

Port `openshift/console-operator` to run control-plane-side with split targets: operand (bridge Deployment/Service/etc.) on the **management** cluster, config CRs + plugin discovery on the **guest**. This is the study's primary upstream cost (§9.2, §14).

Work items (study §14.10 sizing — medium-heavy mechanical refactor, ~4-6 files + a const sweep across ~19 files):
- **Dual clients:** add `--guest-kubeconfig`, split `kubeClient` → `mgmtKubeClient` + `guestKubeClient`, re-home typed CR clients to guest (§14.2). Precedent: CNO's multi-cluster client.
- **Configurable operand namespace:** `api.TargetNamespace` const → flag/param (`--operand-namespace`), route the HCP namespace through it (§14.3).
- **Off-cluster bridge flag injection** in generated Deployment (`deployment.go:395-419`) (§14.4).
- **resourceSyncer:** deferrable/stubbable on the OIDC path — core console + plugins don't need it (§14.8).
- Model to copy first: `IsExternalControlPlaneWithIngressDisabled` (`starter.go:250`) (§14.7).
- Until upstream merges: run patched operator/bridge via image overrides (study D5).

Deliverable: the operator (not hand-YAML) generates and reconciles the operand + guest config, incl. dynamic plugin discovery.

### 2.3 Track C — HyperShift / CVO plumbing (control-plane-side deploy of the operator)

Wire the ported operator into HyperShift as a CPO v2 component and manage guest payload placement. Study §13, §18.

- **CPO v2 component** `consoleoperator`: `component.go`/`deployment.go` + `v2/assets/console-operator/` manifests; register in `registerComponents`; gate on `IsConsoleCapabilityEnabled` (§13.1-13.7).
- **Guest KAS auth (framework):** `InjectServiceAccountKubeConfig` for the operator; token-minter (`KubeAPIServerToken`) sidecar for the bridge (§16). Replaces the MVP static token entirely.
- **Konnectivity sidecars:** operator = HTTPS mode; bridge = Socks5 (only once plugins/monitoring are in scope) (§13.10).
- **Exposure as first-class API:** add `ServiceType` const `Console` + CRD enum + publishing-strategy validation; Service/Route reconcile mirroring oauth; `InfraStatus.ConsoleHost`; CLI hostname + PSC ExternalName (§8.D). This replaces the MVP hand-applied Route.
- **Cert wiring:** reuse api-server wildcard named-cert programmatically (§8.D, D3).
- **KAS `ConsolePublicURL`** → `console.<domain>` (§8.G).
- **CVO placement flag + payload strip:** capability gate unchanged; new placement flag strips the guest console-operator **workload** via `preparePayloadScript` (`cvo/deployment.go:208-275`), keeps guest RBAC/namespaces/status (§18, §19.1). Net-new removal logic in `release-manifests/` (§18.2).
- **Plugins across planes** + `server.go:524` bridge fix for asset proxying (§8.E, §9.1).

Deliverable: `spec.capabilities` + placement flag on the HostedCluster deploys the full-featured console control-plane-side; guest data-plane console removed; guest status still published.

### 2.4 Track D — Tests + rollout safety

- Unit: component config gen, sidecar injection, plugin endpoint injection, OIDC config, Service/Route + label, cert SANs (§8.H).
- E2E (GCP): OIDC login + browse; zero-node; data-plane plugin load via konnectivity; public LB + private PSC (§8.H).
- Fleet-rollout check: confirm console config is control-plane-only and feeds no NodePool config hash (`hashStruct`/`configHash`) — repo rule.

---

## 3. Dependency / sequencing summary

```
MVP (manual, auth off)  ──►  Track A (OIDC)  ─┐
                                              ├─►  Track C (HyperShift/CVO) ──► Track D (tests)
Track B (console-operator refactor) ──────────┘
```

- **MVP** has no upstream dependency; do it first to validate §3/§4/§9.1 cheaply (study Phase 0).
- **Track A** and **Track B** can proceed in parallel after MVP.
- **Track C** depends on Track B (needs the ported operator) and benefits from Track A.
- **Track A**'s Google-client provisioning is the single biggest open risk (study Gap 4 / `CONSOLE_AUTH_OPTIONS.md` §7) and can block the production auth story independent of the plumbing.

---

## 4. Does this make sense? (assessment of the proposed approach)

Yes. The MVP framing is sound and matches the study's Phase 0 spike (§15) almost exactly. Refinements baked into this plan:

- **Auth disabled is genuinely enough for MVP** — confirmed `-user-auth=disabled` + `-k8s-auth-bearer-token` in source. No OIDC needed to prove reachability + browse.
- **"Minimal code change in console" is likely zero** for core console — off-cluster mode is first-class. The one optional fix is plugin-only, out of MVP scope. Keep it as a watch item, not a planned change.
- **Cert:** don't hand-mint if the api-server wildcard `*.<domain>` already exists — reuse it (D3). Cheapest publicly-trusted path.
- **DNS:** external-dns may already auto-register the Route host; manual DNS is the fallback, not the default.
- **Token minter / guest CA mounting** are correctly deferred to Track C — MVP uses skip-verify + static token to avoid them.
- Sequencing (A/B parallel, C depends on B) matches study §15's critical path (console-operator refactor gates the CPO component).
```
