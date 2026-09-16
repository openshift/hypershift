# Phase 1 Implementation Plan: Prove Core Console Runs Control-Plane-Side (GCP HCP)

**Status:** Draft plan — local, not committed
**Companions:** `CONSOLE_CONTROL_PLANE_STUDY.md` (feasibility + file:line), `CONSOLE_CONTROL_PLANE_POC_PLAN.md` (roadmap), `console-control-plane-manifests.example.yaml` (full-featured illustration)
**Goal of Phase 1:** Prove the **core** OpenShift console runs in an existing HostedControlPlane (HCP) namespace on the management cluster and works fully end-to-end — DNS, publicly-trusted cert, static/no auth, and the guest-network path for both **PublicAndPrivate** and **Private** GCP clusters. No console-operator, no HyperShift lifecycle plumbing (that comes later).

**Scope decisions (locked):**
- Auth: started as `-user-auth=disabled` + static guest bearer token; **now upgraded to
  per-user Google OIDC login** (`-user-auth=oidc`), validated live. See §10 (Resolved) and
  `GOOGLE_OIDC_CLIENT_SETUP.md`.
- Guest KAS TLS: verified via the `root-ca` secret (not skip-verify).
- Endpoint modes: **PublicAndPrivate** and **Private** (Public not supported → out of scope).
- Exposure: **minimal CPO code change** — make the HCP HAProxy router accept a generic labeled Route. Route/Service/cert/DNS are **hand-applied**. Router acceptance is **always-on** (no annotation/flag gate).
- No console code changes in Part 1. (Custom image + 1-line fix only in Part 2 for plugins.)

**Split:** **Part 1 = core console deployment.** **Part 2 = plugin path (adapted).** Deploy and validate Part 1 first.

---

# PART 1 — Core console deployment

## 1. Architecture (what we are building)

```
browser ──TLS──► [PublicAndPrivate: router public LB :443]  ──► HCP HAProxy router :8443
                 [Private: PSC endpoint ► ServiceAttachment ► internal LB]     │ SNI passthrough (mode tcp)
                                                                               ▼
                                                                   console Service (ClusterIP :8443)
                                                                               ▼
                                                          console bridge pod (HCP namespace, mgmt cluster)
                                                            terminates TLS (cert-manager wildcard/leaf)
                                                            -user-auth=disabled + static token
                                                                               │
                                                                  off-cluster k8s proxy
                                                                               ▼
                                             guest KAS: kube-apiserver.<hcp-ns>.svc:6443  (same-ns ClusterIP, NO konnectivity)
```

Verified facts underpinning this:
- Guest KAS is a plain in-namespace ClusterIP Service `kube-apiserver:6443` reachable directly from any pod in the HCP namespace — **no konnectivity** (`manifests/infra.go:11,37-44`; `support/config/constants.go:36`; `v2/kas/kubeconfig.go:275-280`). Core console never touches the guest pod/service network, so **no konnectivity sidecar in Part 1**.
- Guest KAS serving CA to trust = secret/configmap `root-ca` (key `ca.crt`) in the HCP namespace (`manifests/pki.go:21,34-41`; `support/certs/tls.go:38`).
- Public browser-facing KAS URL on GCP = `https://api.<domain>:6443` (`HCP.Status.ControlPlaneEndpoint`; `hostedcontrolplane_controller.go:791-793`; `v2/kas/kubeconfig.go:286-288`).
- For PublicAndPrivate & Private GCP, `UseHCPRouter=true` → the per-HCP HAProxy router pod exists (`router/util/util.go:15-28`; `support/netutil/visibility.go:234-283`).
- Router is SNI passthrough; both the public LB (`443→8443`) and the Private PSC service-attachment front the **whole** router, so a new SNI host needs only a labeled Route + a router backend + DNS — **no per-host LB/PSC change** (`router_config.template:6,23-34`; `ingress/router.go:76-95`; `gcpprivateserviceconnect/…`).

## 2. The ONE code change (HyperShift CPO)

**Component:** `control-plane-operator` (CPO) — HCP HAProxy router config generator.
**Why:** the router only builds HAProxy backends for a hardcoded set of route names via a `switch route.Name` (`v2/router/config.go:113-136`). A hand-applied `console` Route labeled `HCPRouteLabel` is currently **skipped** (matches no case) → no backend, no SNI acl. We add a generic path so any labeled passthrough Route becomes a backend.

**File:** `control-plane-operator/controllers/hostedcontrolplane/v2/router/config.go`
**Function:** `generateRouterConfig` (the `switch route.Name` loop, ~`:113-136`).

**Change (minimal, always-on):** add a `default` case to the switch that emits a generic backend from the labeled route's own fields. `svcsNameToIP` is already populated for every labeled route (`config.go:46-60`), and the template already ranges over `.Backends` generically (`router_config.template:27-34,46-49`), so no template change is required.

```go
// after the existing known cases in the switch (config.go ~:136)
default:
    // Generic passthrough backend for any other HCP-labeled Route
    // (e.g. a manually-applied "console" route in Phase 1).
    port := int32(443)
    if route.Spec.Port != nil && route.Spec.Port.TargetPort.IntVal != 0 {
        port = route.Spec.Port.TargetPort.IntVal
    }
    p.Backends = append(p.Backends, backendDesc{
        Name:                 route.Name,
        HostName:             route.Spec.Host,
        DestinationServiceIP: svcsNameToIP[route.Spec.To.Name],
        DestinationPort:      port,
    })
```

Notes / correctness:
- `backendDesc.Name` becomes the HAProxy backend + acl name; `route.Name = "console"` yields `acl is_console` / `use_backend console`. Safe (alphanumeric).
- The console Service targetPort is `8443` (see §5), so set the Route `spec.port.targetPort: https` (resolves to 8443) — the `default` case reads `TargetPort.IntVal`; if you use a **named** targetPort in the Route, either switch to `int` targetPort `8443` in the Route, or extend the default case to resolve named ports. **Simplest: set the Route `spec.port.targetPort: 8443` (integer).** (See §7 Route manifest — uses integer to match this code.)
- Live-reload caveat: the router config regenerates on router component reconcile. There is a known TODO that config is not hot-reloaded on route changes (`v2/router/component.go:74-75`). After applying the console Route, trigger a router reconcile (e.g. `kubectl rollout restart deploy/router -n <hcp-ns>` or bump the HCP) so the new backend is picked up. Document this as a manual step for Phase 1.

**No other CPO changes.** No new component, no capability gate, no CVO changes, no API/CRD changes.

**Build & deploy the patched CPO:** use the repo skill `Build CPO Image` to build/push a CPO image, then point the HostedCluster at it via the image-override annotation (`hypershift.openshift.io/image-overrides`) or your dev HO install. The router pod runs the CPO image, so the patched router-config logic ships with it.

## 3. Images

**Part 1 needs NO custom images.**
- **console bridge:** use the stock image from the cluster's release payload (`GetImage("console")`). Binary `/opt/bridge/bin/bridge`, assets `/opt/bridge/static` (`_console-research/Dockerfile.product:27-35`). Resolve the exact ref from the release, e.g.:
  ```
  oc adm release info <release-pullspec> --image-for=console
  ```
  Use that digest ref directly in the Deployment (§6).
- **CPO:** the patched CPO image from §2 (temporary, e.g. `quay.io/patmarti/control-plane-operator:console-phase1`).

`quay.io/patmarti` custom **console** image is **only** needed in Part 2 (plugin proxy fix).

## 4. Static guest token (auth disabled)

Auth is disabled; the bridge uses one static bearer token for **all** k8s API calls (`_console-research/cmd/bridge/config/auth/authoptions.go:38,79,226`). Mint a token for a privileged guest SA (dev only):

```bash
# Using the guest kubeconfig (admin) for the target HostedCluster:
oc --kubeconfig <guest-admin.kubeconfig> -n kube-system create token default --duration=24h
# or bind a dedicated SA to cluster-admin and mint its token; the token is what the bridge presents.
```

Put the token into a secret in the HCP namespace (§7, `console-static-token`). Because auth is disabled, **every browser user acts as this identity** — never use outside a spike.

> Token expiry: `create token` is bound/expiring. For a longer-lived spike, mint with a longer `--duration` or use a legacy SA token secret. Re-mint when it expires; the bridge re-reads the file if you use `-k8s-auth-bearer-token-file` (note: the flag `-k8s-auth-bearer-token` takes the literal token; to read from a file use the file variant if present, else bake the token into the secret and restart). For Phase 1 simplicity: put the literal token in the secret and pass it via env/arg; re-apply + restart on expiry.

## 5. Serving certificate (cert-manager)

Browser client ⇒ pod must serve a **publicly-trusted** leaf covering the console host. cert-manager is assumed installed + a working `ClusterIssuer` exists (public CA, e.g. Let's Encrypt via DNS01 for GCP).

Two options:
- **(a) Reuse the api-server wildcard `*.<domain>`** if it already exists as a secret in the HCP namespace (study D3). Mount it directly; skip the Certificate resource. Preferred if present.
- **(b) Dedicated Certificate** for `console.<domain>` (or the private host). Manifest in §7 (`console-serving-cert`). DNS01 is the right challenge for GCP (works for private hosts too, no inbound needed).

The pod mounts the resulting `kubernetes.io/tls` secret at `/var/serving-cert` and serves it via `-tls-cert-file`/`-tls-key-file`.

## 6. Hostname & DNS per mode

| Mode | Console host | DNS mechanism | Manual step |
|---|---|---|---|
| **PublicAndPrivate** | `console.<baseDomain>` (in the managed zone, same as `api.`/`oauth.`) | external-dns watches the labeled Route and registers `spec.host` in-zone (`support/netutil/route.go:124-125`) | none beyond applying the Route (external-dns auto-registers) |
| **Private** | a host under `*.apps.<cluster>.hypershift.local` (e.g. `console.apps.<cluster>.hypershift.local`) | already covered by the existing PSC wildcard A record `*.apps.<cluster>.hypershift.local` → PSC endpoint IP (`gcpprivateserviceconnect/.../dns.go:476-480`) | none — wildcard already resolves |

Both modes: the fronting LB/PSC passes SNI through to the shared router; the router serves the new host once the backend exists (§2). **No per-host LB or PSC/service-attachment change.**

`-base-address` and the Route `spec.host` must match the chosen host exactly.

## 7. Manifests (hand-applied to the HCP namespace)

Placeholders: `${HCP_NAMESPACE}` (e.g. `clusters-myhc`), `${CONSOLE_HOST}` (PublicAndPrivate: `console.<baseDomain>`; Private: `console.apps.<cluster>.hypershift.local`), `${API_DOMAIN}` (public KAS domain, → `https://api.<domain>:6443`), `${CONSOLE_IMAGE}` (stock release console ref), `${CLUSTER_ISSUER}` (existing cert-manager ClusterIssuer).

```yaml
---
# 1. Static guest token (auth disabled). DEV ONLY.
apiVersion: v1
kind: Secret
metadata:
  name: console-static-token
  namespace: ${HCP_NAMESPACE}
  labels: { app: console }
type: Opaque
stringData:
  token: "REPLACE_WITH_GUEST_SA_TOKEN"
---
# 2. Serving cert via cert-manager (option (b)). Skip if reusing the api wildcard.
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: console-serving-cert
  namespace: ${HCP_NAMESPACE}
  labels: { app: console }
spec:
  secretName: console-serving-cert         # produces kubernetes.io/tls secret
  dnsNames:
    - ${CONSOLE_HOST}
  issuerRef:
    name: ${CLUSTER_ISSUER}
    kind: ClusterIssuer
  # DNS01 solver config lives on the ClusterIssuer (assumed configured).
---
# 3. Console bridge Deployment (core; NO operator, NO konnectivity, NO OIDC)
apiVersion: apps/v1
kind: Deployment
metadata:
  name: console
  namespace: ${HCP_NAMESPACE}
  labels: { app: console }
spec:
  replicas: 2
  selector: { matchLabels: { app: console } }
  template:
    metadata:
      labels: { app: console }
    spec:
      automountServiceAccountToken: false   # bridge uses the static guest token, not its own SA
      containers:
        - name: console
          image: ${CONSOLE_IMAGE}
          command: [ /opt/bridge/bin/bridge ]
          args:
            - -listen=https://0.0.0.0:8443
            - -base-address=https://${CONSOLE_HOST}
            - -public-dir=/opt/bridge/static
            # ---- off-cluster: guest KAS in-namespace, verified via root-ca ----
            - -k8s-mode=off-cluster
            - -k8s-mode-off-cluster-endpoint=https://kube-apiserver.${HCP_NAMESPACE}.svc:6443
            - -ca-file=/var/run/guest-ca/ca.crt
            - -k8s-public-endpoint=https://api.${API_DOMAIN}:6443
            # ---- auth OFF: static token used for ALL requests ----
            - -user-auth=disabled
            - -k8s-auth-bearer-token=$(GUEST_TOKEN)
            # ---- TLS at the pod (router is passthrough) ----
            - -tls-cert-file=/var/serving-cert/tls.crt
            - -tls-key-file=/var/serving-cert/tls.key
          env:
            - name: GUEST_TOKEN
              valueFrom:
                secretKeyRef: { name: console-static-token, key: token }
          ports:
            - { name: https, containerPort: 8443, protocol: TCP }
          volumeMounts:
            - { name: serving-cert, mountPath: /var/serving-cert, readOnly: true }
            - { name: guest-ca,     mountPath: /var/run/guest-ca, readOnly: true }
          readinessProbe:
            httpGet: { path: /health, port: 8443, scheme: HTTPS }
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            httpGet: { path: /health, port: 8443, scheme: HTTPS }
            initialDelaySeconds: 30
            periodSeconds: 30
          resources:
            requests: { cpu: 10m, memory: 100Mi }
      volumes:
        - name: serving-cert
          secret:
            secretName: console-serving-cert    # or the reused api wildcard secret
            defaultMode: 0640
        - name: guest-ca
          configMap:
            name: root-ca                        # guest KAS serving CA (key ca.crt)
            items: [ { key: ca.crt, path: ca.crt } ]
---
# 4. Console Service (ClusterIP). Router backend targets this.
apiVersion: v1
kind: Service
metadata:
  name: console
  namespace: ${HCP_NAMESPACE}
  labels: { app: console }
spec:
  selector: { app: console }
  ports:
    - { name: https, port: 8443, targetPort: 8443, protocol: TCP }
  type: ClusterIP
---
# 5. Console Route — passthrough, labeled so the HCP router adopts it as an SNI backend.
#    integer targetPort 8443 to match the CPO default-case code (§2).
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: console
  namespace: ${HCP_NAMESPACE}
  labels:
    app: console
    hypershift.openshift.io/hosted-control-plane: ${HCP_NAMESPACE}   # HCPRouteLabel; VALUE MUST equal the namespace
spec:
  host: ${CONSOLE_HOST}
  to: { kind: Service, name: console, weight: 100 }
  port: { targetPort: 8443 }
  tls:
    termination: passthrough
    insecureEdgeTerminationPolicy: None
---
# 6. PodDisruptionBudget (optional; supports zero-node since bridge is control-plane-side)
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: console
  namespace: ${HCP_NAMESPACE}
  labels: { app: console }
spec:
  minAvailable: 1
  selector: { matchLabels: { app: console } }
```

> Important: the `HCPRouteLabel` **value must equal the namespace** — `AddHCPRouteLabel` sets `labels[HCPRouteLabel] = namespace` (`support/netutil/route.go:146-151`) and the router only checks presence of the key (`config.go:109`), but keep the value = namespace to match convention and any future validation.

> `root-ca` is a ConfigMap in the HCP namespace (`manifests/pki.go:34-41`). If only the Secret form exists in your namespace, mount that instead (key `ca.crt`).

## 8. Deploy & validate (runbook)

1. **Patch + ship CPO** (§2): build/push patched CPO (skill `Build CPO Image`), set image override on the HostedCluster, wait for the new CPO + router to roll.
2. **Resolve console image** (§3): `oc adm release info … --image-for=console`.
3. **Mint guest token** (§4) → fill `console-static-token`.
4. **Cert** (§5): apply the `Certificate` (or identify the reused wildcard secret). Wait until the `console-serving-cert` secret is `Ready`.
5. **Apply** manifests §7 items 3–6 into `${HCP_NAMESPACE}`.
6. **Force router config regen** (live-reload TODO, §2): `kubectl -n ${HCP_NAMESPACE} rollout restart deploy/router`. Confirm the HAProxy configmap now contains `backend console` and `acl is_console`:
   ```
   kubectl -n ${HCP_NAMESPACE} get cm <router-config-cm> -o yaml | grep -A2 console
   ```
7. **DNS:** PublicAndPrivate → confirm external-dns created the `console.<domain>` record; Private → confirm `*.apps` wildcard resolves the host to the PSC endpoint IP.
8. **Validate:**
   - Pod `1/1`, `/health` 200.
   - `curl -v https://${CONSOLE_HOST}/health` → 200, valid TLS chain (no `-k`).
   - Browser: `https://${CONSOLE_HOST}` loads the UI with no TLS warning.
   - Browse guest resources (Projects/Pods/Nodes lists) — proves the off-cluster k8s proxy + static token + guest KAS path.
   - **Zero-node check:** works on a guest cluster with no workers (proves control-plane-side).
   - **Private mode:** validate from a client inside the PSC-reachable network.

## 9. Part 1 acceptance criteria

Status against `pat-console` (GCP, zero-node):

- [x] CPO router serves the `console` (and `downloads`) SNI host — backend present in HAProxy
  config (verified live). Note the router change grew from the originally-planned single
  `default` case into named `console`/`downloads` cases (plus their `-private` variants), and CPO
  now owns the Routes themselves — see §22 / `PRIVATE_ENDPOINT_ACCESS.md`.
- [x] `https://<console host>` reachable with publicly-trusted TLS in **PublicAndPrivate** (direct
  public `curl` 200) and **Private** (via an in-VPC bastion, `curl` 200) — `PRIVATE_ENDPOINT_ACCESS.md`.
  Browser UI load itself was confirmed earlier on the public path.
- [x] Console browses guest cluster resources via the in-namespace guest KAS (no konnectivity),
  TLS verified with `root-ca` (patched bridge, no skip-verify — §22).
- [x] **Per-user login via Google OIDC** (upgraded from the planned `-user-auth=disabled` static
  token): browser login → session → guest KAS accepts the token → user browses as their own
  identity. See §10 (Resolved) and `GOOGLE_OIDC_CLIENT_SETUP.md`.
- [x] Works zero-node (validated: `pat-console` has 0 nodes).
- [~] Console code: one small bridge patch was needed after all (the `-ca-file` off-cluster fix,
  §22 / `UPSTREAM_PATCHES.md`), not zero. CPO changes are the router cases + console/downloads
  Route ownership + Private ExternalName services.

## 10. Known limitations carried out of Part 1 (by design)

- Router config not hot-reloaded — manual router restart after applying the Route (CPO TODO `component.go:74-75`).
- No plugins / no monitoring (needs the guest pod-network path) — **Part 2**.
- Everything hand-applied (no operator, no lifecycle) — later phases.
- **Multi-replica sessions:** the bridge keeps sessions in a per-pod in-memory
  map (`server_session.go`), and the SNI-passthrough router (`mode tcp`) cannot
  do cookie-based affinity, so with >1 replica a browser can land on a pod that
  doesn't hold its session. A shared session store is a bridge code change,
  deferred. (Login was validated live with 2 replicas — it works when requests
  land on the same pod; it is not robust across pods.)

Resolved during implementation (no longer limitations):
- **Per-user OIDC login: DONE (verified live end-to-end).** Replaces the Phase 1
  `-user-auth=disabled` + static token. Each user logs in via Google and browses
  as their own identity/RBAC. Requirements, all hand-replicated because there is
  no console-operator: (1) `-user-auth=oidc` + issuer/client-id + client-secret
  file; (2) the console client ID added to the guest KAS OIDC `audiences` (the
  only HC-spec change — an `oidcProviders[].oidcClients` entry is *not* usable
  here, its admission requires `status.oidcClients` which only a running guest
  console-operator writes); (3) `-user-auth-oidc-token-scopes=email,profile` so
  the ID token carries the `email` claim the KAS maps to username (with only the
  default `openid` scope Google omits `email` and KAS rejects every request with
  `oidc: parse username claims "email": claim not present`); (4) session cookie
  keys (32B AES + 64B HMAC), normally operator-generated, hand-provided via a
  Secret. See `GOOGLE_OIDC_CLIENT_SETUP.md` and `STUDY.md` §22.
- **CLI-downloads server: DONE**, incl. the UI link. Deployed control-plane-side with a
  TLS-terminating oauth-proxy sidecar + `cli-artifacts` image + dedicated CPO router `downloads`
  backend; serves real `oc` binaries end-to-end. The UI "Command Line Tools" page is wired by
  hand-applying the `ConsoleCLIDownloads` CRD + an `oc-cli-downloads` CR to the guest
  (`console/guest/`), since the guest `Console` capability is disabled and there's no
  console-operator. Open design question (deferred): who installs that CRD and reconciles the CR
  long-term — see `STUDY.md` §22.
- **Off-cluster `-ca-file` verification: DONE (patched image; upstream PR open).** Runs with
  `-ca-file`-verified `root-ca` trust and NO skip-verify. Tracking: `UPSTREAM_PATCHES.md`,
  Jira GCP-1219, PR openshift/console#17185. See `STUDY.md` §22.

---

# PART 2 — Plugin path (adapted)

Deploy and validate **after Part 1 passes.** Plugins are the one piece that needs the guest **pod/service network** (plugin asset backends are guest ClusterIP Services like `<plugin-svc>.<ns>.svc.cluster.local`), which the core console never touches. This is where konnectivity + the one console code fix + a custom image come in.

## 11. What changes vs Part 1

1. **Console code fix (1 line)** — the plugin **asset** transport is hand-built and ignores `HTTP_PROXY` (`_console-research/pkg/server/server.go:520-525`). Add `Proxy: http.ProxyFromEnvironment` at `server.go:524` so plugin asset fetches go through the konnectivity socks5 proxy. (The plugin **API** proxy `/api/proxy` already honors proxy env — `server.go:555`; and the off-cluster k8s proxy already sets `UseProxyFromEnvironment: true` — `cmd/bridge/main.go:541`.)
   - Build a patched console image → push to **`quay.io/patmarti/console:console-phase2`** (public repo). Use it as `${CONSOLE_IMAGE}` in Part 2.

2. **Konnectivity socks5 sidecar** on the bridge pod — tunnels `HTTP(S)_PROXY` traffic into the guest pod network via the reverse konnectivity tunnel. It resolves guest Service names → ClusterIP by reading the Service object from the guest API (resolver step 2, study §5.1), then dials through konnectivity. In the real component this is injected by `.InjectKonnectivityContainer({Mode: Socks5})`; for a manual spike, hand-roll it from the CPO image (`control-plane-operator konnectivity-socks5-proxy run`, listens `:8090`, mounts konnectivity client cert `konnectivity-client` + CA `konnectivity-ca`). See `console-control-plane-manifests.example.yaml` object 9 sidecar as the shape.

3. **Guest kubeconfig for the socks5 resolver** — the sidecar needs a guest client to resolve Service→ClusterIP. For a spike, reuse a guest kubeconfig secret mounted at the sidecar's `KUBECONFIG`. (Production: framework-injected per-SA kubeconfig, study §16.)

4. **Bridge proxy env** — set on the console container:
   ```
   HTTP_PROXY=socks5://127.0.0.1:8090
   HTTPS_PROXY=socks5://127.0.0.1:8090
   NO_PROXY=kube-apiserver.${HCP_NAMESPACE}.svc,localhost,127.0.0.1
   ```
   `NO_PROXY` **must** include the guest KAS in-namespace name so KAS traffic stays direct (not tunneled) — matches the oauth pattern (`v2/oauth/deployment.go:57-65`).

5. **console-config with plugins** — Part 1 runs flags-only with no plugins. To load a plugin without the operator, provide a `console-config.yaml` ConfigMap listing the plugin `name → https://<svc>.<ns>.svc.cluster.local:<port>` and mount it (`-config`). Endpoints resolve in-guest via the socks5 sidecar. (Study §14.5 confirms URL shape `getServiceURL` `configmap.go:255-262`.) Dynamic discovery of guest `ConsolePlugin` CRs is operator work — **not** in Phase 1; here we wire one plugin by hand to prove the path.

## 12. Part 2 validation

- [ ] Patched console image (`quay.io/patmarti/console:console-phase2`) runs.
- [ ] Konnectivity socks5 sidecar healthy; resolves a known guest plugin Service.
- [ ] A hand-configured plugin loads in the console UI (assets fetched through the tunnel — proves the `server.go:524` fix + socks5 path).
- [ ] Core console (Part 1) still works unchanged with the sidecar present.

## 13. Part 2 non-goals

- Dynamic plugin discovery (guest `ConsolePlugin` CR watch → config regen → rollout) — operator work, later phase.
- Monitoring (Thanos/Alertmanager) — same konnectivity path, out of Phase 1 scope.

---

# Appendix A — File:line evidence index

| Claim | Reference |
|---|---|
| Router switch skips unknown route names | `v2/router/config.go:113-136` |
| Router config ranges Backends generically (template) | `v2/router/router_config.template:27-34,46-49` |
| `svcsNameToIP` built for every labeled route | `v2/router/config.go:46-60` |
| Router live-reload TODO | `v2/router/component.go:74-75` |
| `HCPRouteLabel` const + value=namespace | `support/netutil/route.go:17,146-151` |
| Only label KEY presence checked | `v2/router/config.go:109` |
| `UseHCPRouter` true for GCP Private/PublicAndPrivate | `router/util/util.go:15-28`; `support/netutil/visibility.go:234-283` |
| Public router LB `443→8443`, selector `app: private-router` | `ingress/router.go:24-106`; `manifests/ingress.go:68-75` |
| Router container port 8443, health 9444 | `v2/router/assets/router/deployment.yaml:28-42`; `router_config.template:23-24,42-45` |
| External route DNS via external-dns watching Route | `support/netutil/route.go:102-127` |
| Private PSC wildcard `*.apps…` → PSC endpoint IP | `gcpprivateserviceconnect/.../dns.go:470-480` |
| Guest KAS = in-namespace ClusterIP `kube-apiserver:6443`, no konnectivity | `manifests/infra.go:11,37-44`; `v2/kas/kubeconfig.go:275-280` |
| Guest KAS serving CA = `root-ca` (ca.crt) | `manifests/pki.go:21,34-41`; `support/certs/tls.go:38` |
| Public KAS URL `api.<domain>:6443` | `hostedcontrolplane_controller.go:791-793`; `v2/kas/kubeconfig.go:286-288` |
| Bridge off-cluster flags + static token | `_console-research/cmd/bridge/main.go:514-541`; `config/auth/authoptions.go:38,79,226` |
| Off-cluster k8s proxy honors proxy env | `_console-research/cmd/bridge/main.go:541` |
| Bridge binary/assets paths | `_console-research/Dockerfile.product:27-35` |
| `/health` endpoint | `_console-research/pkg/server/server.go:350` |
| Plugin asset transport ignores proxy (Part 2 fix) | `_console-research/pkg/server/server.go:520-525` |
