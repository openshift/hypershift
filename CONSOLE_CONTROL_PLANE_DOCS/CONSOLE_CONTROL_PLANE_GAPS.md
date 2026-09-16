# Open Gaps: Console Control-Plane-Side Implementation

**Status:** Draft. Base design resolved; this file tracks only what remains **open**.
**Companion files:**
- `CONSOLE_CONTROL_PLANE_STUDY.md` — the design study (resolved decisions live here).
- `CONSOLE_AUTH_OPTIONS.md` — authoritative console-auth reference (definitive for Gap 4).
- `console-control-plane-manifests.example.yaml` — illustrative manifests.
- `STUDY_COMPONENTS_DEPLOYMENT_PATTERNS.md` — CVO-shipped vs control-plane-side patterns (CNO precedent).

**Strategy (D1):** port the console-operator upstream (split-cluster refactor), full-featured
(plugins in scope). The direct-CPO "start simple" alternative was rejected (study §17).

## Status roll-up

| Gap | Status | Where |
|-----|--------|-------|
| 1 — Guest KAS auth | ✅ RESOLVED | study §16 |
| 2 — Dual-client refactor | ◑ De-risked; residual is mechanical only | study §14, §14.10 — **open items below** |
| 3 — Upstream flag API | ◑ De-risked (CNO precedent); naming/framing open | **open items below** |
| 4 — GCP OIDC client model | ⛔ **BLOCKER open** | `CONSOLE_AUTH_OPTIONS.md` §7 — **summary below** |
| 5 — Konnectivity mode | ✅ RESOLVED | study §13.10 |
| 6 — Guest + mgmt RBAC | ✅ RESOLVED | study §19 |
| 7 — CVO console removal | ◑ Mechanism verified; specifics open | **open items below** |

Resolved gaps (1, 5, 6, and the resolved parts of 2) are documented in the study; they are **not**
repeated here. Only open work follows.

---

## Gap 4 — GCP OIDC console client model ⛔ (the primary blocker)

**Full analysis:** `CONSOLE_AUTH_OPTIONS.md` §7. Summary only here.

**What's solved:** the OIDC base works on live GCP HCP; HCCO owns the guest `Authentication` CR and
already **delivers** any `oidcClients[]` entry + client secret to the guest. Bridge login is
spec-driven. (§7.1–7.2)

**The blocker:** the console bridge is a **confidential web app** that needs its **own Google OAuth
"Web application" client** (real secret + registered `console.<domain>/auth/callback` redirect). The
default/shared Google client works only as a **KAS audience**, never as the bridge's client (§7.3).
And **Google web-client creation is not automatable** (no API/gcloud/Terraform) and **wildcard
redirect URIs are prohibited** (§7.4). This is **topology-independent** — it hits data-plane console
too, not just the control-plane-side port.

**Hard constraint:** creating a hosted cluster must **not** require a manual Google step. Any design
where Google sees a per-cluster URL fails ⇒ eliminates shared-client (opt 1), manual-per-cluster
(opt 3), and IntegratedOAuth+OpenID-IdP (opt 5, redirect just moves). **Survivors:**
- **Option 2 — state-based redirect broker:** one fleet-fixed Google redirect URI; target cluster in
  OAuth `state`; adding clusters needs zero Google changes. Works for **private clusters** (Google
  never dials the redirect URI — it 302s the private-side browser; a private broker + private
  redirect URI is fine). Needs **upstream bridge changes** (bridge can't target a separate broker
  today) + new fleet auth infra. Lead candidate. (§7.5)
- **Option 4 — alternate IdP with dynamic client registration** fronting Google. Larger arch change.

**Secret isolation:** the **day-2 pattern** (`hosted-cluster-sourced` annotation,
[OCPSTRAT-2173](https://redhat.atlassian.net/browse/OCPSTRAT-2173)) keeps the client secret **only in
the guest** (HCCO skips the copy) — currently ARO-HCP-only; GCP would need to extend honoring. (§7.6)

**OPEN:**
1. **Decide the client model** (realistically option 2 or 4). Gating; needs product + platform +
   security. This is the single biggest open risk in the whole study.
2. If option 2: design + build the broker (state signing/allowlist, per-connectivity-domain
   reachability for private clusters) and the upstream bridge change to target it.
3. Extend day-2 `hosted-cluster-sourced` honoring to GCP (if secret-isolation is required).
4. Confirm `ConsolePublicURL` → KAS (`kas/params.go:77`, `kas/config.go:151`) for the `oc-oidc`
   login command when the operator runs control-plane-side. Minor.

---

## Gap 7 — Remove data-plane console via CVO (mechanism verified; specifics open)

**Refs:** study §18; `control-plane-operator/controllers/hostedcontrolplane/v2/cvo/deployment.go`.

**Model (decided) — two orthogonal switches:**
1. `Console` **capability** gate (existing, unchanged): disabled → no console anywhere; enabled →
   proceed.
2. **Placement flag** (new; GCP-platform-keyed today, could be a custom flag): off → today's guest
   deployment; on → console runs control-plane-side and the guest CVO must not deploy its copy.
   Removal is applied **only when placement=on**, not unconditionally.

**Mechanism (verified):** `preparePayloadScript(platformType, oauthEnabled, featureSet)`
(`cvo/deployment.go:208-275`) already supports (a) filename stripping and (b) active deletion of
applied objects via `resourcesToRemove` → `0000_01_cleanup.yaml` (`release.openshift.io/delete`).
Precedent for flag-conditional console removal: the `!oauthEnabled` strip of
`0000_50_console-operator_01-oauth.yaml` (`:240-242`).

**OPEN:**
1. **Exact payload filenames — keep vs strip.** STRIP the workload (operator Deployment `07-operator*.yaml`,
   `05-service.yaml`, `05-config.yaml`, servicemonitor/prometheusrbac); KEEP the guest RBAC/SA/CR/namespace
   (`03-rbac-*`, `04-rbac-*`, `06-sa.yaml`, `02-namespace.yaml`, `01-operator-config.yaml`) so CVO
   applies them as-is (guest RBAC per study §19.1). **Confirm real release-image filenames** (dev-repo
   names are re-prefixed `0000_50_console-operator_*`). **Verified:** the blanket
   `rm *_deployment.yaml`/`*_servicemonitor.yaml` (`:214-215`) hits `/manifests`, NOT
   `release-manifests/` where console lives; `manifestsToOmit` lists no console files. So the
   placement-on workload strip is **net-new removal logic** in release-manifests (extend
   `manifestsToOmit` conditionally, or a placement-gated `rm` mirroring the oauth line).
2. **Migration cleanup:** a cluster flipping placement off→on must delete the already-applied guest
   console-operator via `resourcesToRemove`/`0000_01_cleanup.yaml`. Enumerate the objects.
3. **Keep publishing guest status:** capability stays enabled, so the control-plane operator must
   still write guest `clusteroperator/console` + `console.config .status.consoleURL` (via its guest
   client). Confirm it owns these writes when placement=on.
4. **Where the placement flag lives** (HostedCluster spec field vs platform-derived vs annotation)
   and how it reaches `preparePayloadScript`.

---

## Gap 3 — Upstream console-operator flag API (de-risked; naming/framing open)

**Refs:** study §14.3, §14.4, §14.10; CNO `--extra-clusters` precedent
(`cmd/cluster-network-operator/main.go:78-80`, `pkg/hypershift/hypershift.go:117`).

The dual-cluster + configurable-operand-namespace behavior needs new operator inputs. A merged
upstream precedent exists (CNO's `--extra-clusters` + `HYPERSHIFT` env), so this is naming/framing,
not novel API.

**OPEN:**
1. Exact surface for: second kubeconfig (`--guest-kubeconfig` vs CNO's `--extra-clusters` map form),
   configurable operand namespace (`api.TargetNamespace` param), guest KAS endpoint, console image
   override. Flag vs env vs config-file.
2. Upstream-acceptable framing ("manage console on a remote cluster", not HyperShift-specific) —
   follow CNO's merged framing. Must be accepted upstream.

---

## Gap 2 — Dual-client refactor: residual mechanical work

De-risked (CNO is the proven dual-cluster twin) and design sub-items (2a resourceSyncer deferral,
2b node source = guest, 2c reuse CVO-created SAs) are **resolved in the study** (§14, §14.9, §14.10).
No design decisions remain — only implementation, tracked here for completeness:

**OPEN (implementation, not design):**
1. Split `kubeClient` → `mgmtKubeClient`/`guestKubeClient`; re-home typed CR clients to guest; thread
   through ~14 controller constructors (study §14.10).
2. `api.TargetNamespace` const → parameter (48 refs / 19 files).
3. Off-cluster bridge flag injection (`deployment.go:395-419`).
4. Deferred design (post-OIDC only): resourceSyncer two-client variant (study §14.8) — not needed
   for OIDC-first core console + plugins.
