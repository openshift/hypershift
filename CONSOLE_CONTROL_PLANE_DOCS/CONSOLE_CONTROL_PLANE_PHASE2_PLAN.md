# Phase 2 Implementation Plan: Day-0 console UX — pod terminal + monitoring

**Status:** In progress — **Part A (pod terminal): DONE (verified live).**
**Part B (monitoring): backend path DONE (verified live: console → konnectivity
socks5 → guest Thanos = HTTP 200, PromQL `up` returns data).** Browser
verification of Observe → Metrics/Alerts pending. Required a guest VPC firewall
fix (geneve UDP 6081) — see B.6.
**Companions:** `CONSOLE_CONTROL_PLANE_PHASE1_PLAN.md` (core console, DONE),
`CONSOLE_CONTROL_PLANE_STUDY.md` (feasibility + file:line, esp. §5.1 DNS/resolver
and §13.10/§14 konnectivity), `console-control-plane-manifests.example.yaml`
(sidecar shape).

**Goal:** Make the two pieces of core day-0 console UX work control-plane-side on
a HyperShift/GCP HostedCluster: the **pod terminal** (Pods → Terminal) and the
**monitoring UI** (Observe → Metrics / Alerts / Dashboards / Targets). Plugins are
deliberately deferred to Phase 3 — these two are what an operator expects to work
out of the box.

## Why these two, and why now

Phase 1 proved the core console (resource browsing, OIDC login, CLI-downloads,
exposure). The next thing a user reaches for is "show me a shell in this pod" and
"show me metrics/alerts." Both are standard console features, not plugins.

They differ in **how they reach the workload**, which sets the effort:

| Feature | Data path | Guest pod/service network? | Effort |
|---|---|---|---|
| **Pod terminal** | k8s proxy → guest KAS `…/pods/<pod>/exec` (WebSocket/SPDY) | **No** — via the KAS we already reach | **DONE** — works with no new plumbing (verified live) |
| **Monitoring** | `-k8s-mode-off-cluster-thanos` / `-alertmanager` → `thanos-querier` / `alertmanager-main` in `openshift-monitoring` (guest ClusterIP) | **Yes** — guest service network | Higher — needs the konnectivity socks5 tunnel |

## Prerequisites (met on `pat-console`)

- Worker nodes Ready (Phase 1 scaled to 4). This is what let CMO deploy the
  monitoring stack.
- The guest monitoring stack is up: `prometheus-k8s-0`, `thanos-querier`,
  `alertmanager-main-0` Running in `openshift-monitoring` (verified live).
- Phase 1 console with per-user OIDC login working (RBAC is per-user, so what a
  user can see in terminal/monitoring follows their guest RBAC).

---

# Part A — Pod terminal (DONE — works with no new plumbing)

**Status: DONE (verified live).** The pod terminal works in the console UI with
zero Phase 2 changes — it rides entirely on the Phase 1 k8s resource proxy → guest
KAS path. No konnectivity sidecar, no flags, no image change.

## A.1 How it works

The Pods-page terminal is **not** the Web Terminal Operator / DevWorkspace proxy
(`pkg/terminal`, `/api/terminal/proxy`, which needs that operator and *does* use
the guest pod network). The plain pod terminal opens a **WebSocket to the k8s
resource proxy**: `/api/kubernetes/api/v1/namespaces/<ns>/pods/<pod>/exec…`. The
bridge's k8s proxy forwards it to the guest KAS, which streams to the pod via
kubelet. The proxy explicitly supports WebSocket upgrade and special-cases
`/exec` (`_console-research/pkg/proxy/proxy.go` — `Upgrade` handling, `isExec`).

Since Phase 1 already proxies `/api/kubernetes/*` to the guest KAS over
`-ca-file`-verified TLS, **the terminal should work with no new plumbing.**
`kubectl exec` against a guest pod already succeeds live, proving the KAS exec
path is functional.

## A.2 What was validated

Confirmed live: an interactive shell in a guest pod via the console UI. This
proves the full chain end-to-end — browser → console (TLS) → k8s proxy WebSocket
→ guest KAS `pods/exec` → kubelet → pod — works through the SNI-passthrough HCP
router without any special handling (the router forwards the TLS stream opaquely;
the console terminates TLS and does the WebSocket upgrade itself). Access is
governed by the logged-in OIDC user's guest RBAC (`pods/exec` create).

## A.3 Part A acceptance

- [x] Interactive shell in a guest pod via the console UI, as the logged-in
  OIDC user (subject to their RBAC). **Verified live.**

---

# Part B — Monitoring (konnectivity socks5 + bridge flags)

## B.1 The blocker: guest service-network reachability

The console runs control-plane-side. Thanos/Alertmanager are **guest ClusterIP
services** (`thanos-querier.openshift-monitoring.svc`,
`alertmanager-main.openshift-monitoring.svc`) on the guest pod/service network,
which the console pod cannot reach (verified: the name is NXDOMAIN from the
console pod, and there is no tunnel sidecar). Unlike the guest KAS — a plain
in-namespace mgmt-side ClusterIP — the monitoring services are only reachable via
the **konnectivity reverse tunnel** into the guest.

## B.2 The bridge already has the hooks

In `off-cluster` mode the bridge builds the Thanos/Alertmanager proxies **only
if** the flags are set (`_console-research/cmd/bridge/main.go` — the
`fK8sModeOffClusterThanos` / `fK8sModeOffClusterAlertmanager` blocks). Those
proxies already use `serviceProxyTLSConfig` (our `-ca-file` trust) and honor
proxy env for dialing. So the two ingredients are: (1) a socks5 tunnel the bridge
can dial, (2) the two URL flags pointing at the guest services.

## B.3 Work

1. **Konnectivity socks5 sidecar on the console pod.** Tunnels the bridge's
   `HTTP(S)_PROXY` traffic into the guest network via the reverse konnectivity
   tunnel, resolving guest Service → ClusterIP (study §5.1 resolver). In the real
   component this is `InjectKonnectivityContainer({Mode: Socks5, …})` from the CPO
   v2 framework (see `v2/oauth/component.go` for the precedent:
   `Socks5Options{ResolveFromGuestClusterDNS: true, …}`). For the spike, hand-roll
   it in kustomize from the CPO image:
   `control-plane-operator konnectivity-socks5-proxy run`, listens `:8090`, mounts
   the konnectivity **client cert** (`konnectivity-client`) + **CA**
   (`konnectivity-ca`). Shape: `console-control-plane-manifests.example.yaml`
   object 9 sidecar; study §13.10 / §14.

2. **Guest kubeconfig for the socks5 resolver.** The sidecar needs a guest client
   to resolve Service → ClusterIP. Spike: mount a guest kubeconfig at the
   sidecar's `KUBECONFIG`. (Production: framework-injected per-SA kubeconfig,
   study §16.)

3. **Bridge proxy env** on the console container:
   ```
   HTTP_PROXY=socks5://127.0.0.1:8090
   HTTPS_PROXY=socks5://127.0.0.1:8090
   NO_PROXY=kube-apiserver.<HCP_NAMESPACE>.svc,localhost,127.0.0.1
   ```
   `NO_PROXY` **must** keep the guest KAS in-namespace name direct (not tunneled),
   matching the oauth pattern (`v2/oauth/deployment.go` NO_PROXY handling). This is
   also what keeps Part A (terminal, via the KAS) unaffected.

4. **Monitoring flags** on the bridge (currently in the flag help as "DEV ONLY",
   which is the off-cluster convention — the operator sets the in-cluster hosts by
   default):
   ```
   -k8s-mode-off-cluster-thanos=https://thanos-querier.openshift-monitoring.svc:9091
   -k8s-mode-off-cluster-alertmanager=https://alertmanager-main.openshift-monitoring.svc:9094
   ```
   Resolve/confirm the exact ports and paths — the bridge appends `/api` to each
   URL. Thanos tenancy endpoints (9092/9093) collapse to a single off-cluster URL
   (study/main.go note), so some tenancy-scoped features may degrade vs a real
   in-cluster deployment; validate the common Observe views first.

5. **TLS / auth to Thanos & Alertmanager — DONE.** Thanos/Alertmanager present
   service-serving certs signed by the **service-ca**
   (`openshift-service-serving-signer`), a *different* signer than the KAS
   `-ca-file` (root-ca). The stock off-cluster bridge trusts only `-ca-file` for
   every proxy — a real gap. Fixed by teaching the off-cluster branch to honor
   `-service-ca-file` for the service proxies (mirrors in-cluster), keeping
   `-ca-file` for KAS; falls back to `-ca-file` when unset. Mounted from the
   HCP-namespace `service-serving-ca` ConfigMap. See
   `_console-research/OFF_CLUSTER_SERVICE_CA_FILE_PLAN.md`, STUDY §22. Auth:
   `AuthMiddleware` already injects `Authorization: Bearer <user.Token>`; Thanos
   9091 authorizes via `cluster-monitoring-view`, satisfied by our OIDC admin's
   cluster-admin.

## B.4 Part B acceptance

- [x] Konnectivity socks5 sidecar healthy on the console pod; resolves + dials
  `thanos-querier.openshift-monitoring.svc`. **Verified live.**
- [x] Backend path proven: `console → socks5 → thanos-querier:9091/-/healthy` =
  **HTTP 200**, and `.../api/v1/query?query=up` returns
  `{"status":"success",...}` with real metric series. **Verified live.**
- [x] Alertmanager reachable over the same path (returns an RBAC 403 to a
  low-privilege token — i.e. the request reaches AM and authorizes; the console's
  cluster-admin OIDC user passes). **Verified live.**
- [ ] Browser: Observe → **Metrics** (`up`) and Observe → **Alerts** render.
  (Backend proven; UI click-through pending.)
- [ ] Core console (Phase 1) + terminal (Part A) still work with the sidecar and
  proxy env present (NO_PROXY keeps KAS direct). Pods came up 2/2; regression
  browser check pending.

## B.6 Guest VPC firewall fix (geneve) — REQUIRED, discovered live

The socks5 sidecar + flags + TLS were correct, but the first live test still got
`504 Gateway Timeout` from konnectivity. Root cause was **not** the console and
**not** NetworkPolicy — it was the **guest VPC firewall silently dropping OVN-K
geneve overlay traffic (UDP 6081) between worker nodes**, which broke *all*
cross-node pod networking (konnectivity agent on node A could not reach a pod on
node B).

Diagnosis chain (all verified live):
- Console → socks5 → thanos = 504; NetworkPolicy allow-all made no difference →
  not an NP problem.
- konnectivity agent logs: `dial tcp <podIP>:<port>: i/o timeout` for cross-node
  targets; kubelet (node-IP:10250) worked.
- Pod→pod reachability matrix: same-node OK, **every cross-node pair FAIL** (both
  directions, all 4 nodes).
- OVN was fully configured (chassis + geneve tunnels to all peers) but tunnel
  interface stats showed **tx>0, rx=0** on every tunnel → underlay dropping
  geneve.
- Guest VPC (`patmart-b3bb-network`) firewall had only `tcp:22` (bastion) and
  `tcp:10250` (kubelet); GCP implied-deny dropped everything else, incl. geneve.

Fix: one INGRESS allow rule, `udp:6081` from the node subnet (`10.0.0.0/24` on
this cluster). Geneve alone was sufficient — OVN-K encapsulates *all* pod/service
traffic inside geneve, so no pod-CIDR/service-port rules were needed. After the
rule: geneve rx became nonzero, cross-node TCP recovered (timeout → connection
established), and console → socks5 → thanos went **504 → HTTP 200**.

- Repro/fix script: `console/guest/allow-geneve-firewall.sh`.
- **Productization:** this is a HyperShift GCP infra-provisioning gap — the node
  VPC must ship a geneve allow rule. Being fixed in `gcp-hcp-ctl`
  (cross-node-traffic fix). Not console work, but a hard prerequisite for any
  guest pod/service-network feature (monitoring, plugins) on GCP.

## B.5 Non-goals / risks

- **User-workload monitoring tenancy** (per-namespace scoped Thanos): off-cluster
  collapses the tenancy hosts, so scoped views may be limited — out of scope for
  the first pass.
- **Dashboards** (Grafana/console dashboards) beyond what Thanos serves — confirm
  which are backend-independent.
- **Productization:** the sidecar + flags are hand-applied in kustomize here; the
  real form is the CPO v2 `InjectKonnectivityContainer` on an owned console
  component (study §13/§14) — a later phase, tracked with the other operator/
  lifecycle deferrals in the Phase 1 plan §13.

---

# Sequencing

1. **Part A (terminal): DONE.** Worked with no changes (rides the Phase 1 KAS
   exec path); confirmed day-0 exec UX and that WebSocket-through-the-router is a
   non-issue.
2. **Part B (monitoring): backend DONE.** Built the konnectivity socks5 sidecar
   (the reusable piece Phase 3 plugins also need), wired the two flags, fixed the
   off-cluster `-service-ca-file` trust gap, and — critically — fixed the guest
   VPC geneve firewall that was silently breaking all cross-node pod networking
   (B.6). Verified live: console → socks5 → thanos = HTTP 200 + PromQL data.
   Remaining: browser click-through of Observe → Metrics/Alerts, and the
   productization moves (CPO-injected sidecar; geneve rule in gcp-hcp-ctl).
