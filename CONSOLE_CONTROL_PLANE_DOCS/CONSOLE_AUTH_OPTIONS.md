# OpenShift Console: Authentication Options

Study of how `openshift/console` (bridge) and `openshift/console-operator` support
user authentication, what each mode depends on, and what has to be configured on
the `Authentication.config.openshift.io/cluster` (and related) resources to make
it work. This is scoped to the **console login flow** (who can open the web
console and how), not to the identity providers backing `oc login` against the
kube-apiserver directly (those matter too, see §1, but they're a different knob).

Repos studied:
- `openshift/console` bridge — `_console-research/` (this checkout)
- `openshift/console-operator` — `_console-operator-research/` (this checkout)
- `openshift/api` vendor — `Authentication`/`OAuth` CRD types

## TL;DR

The console bridge supports exactly **two real auth modes** plus a dev-only
bypass, selected by the bridge flag `--user-auth` (`disabled|oidc|openshift`):

| Mode | Driven by cluster `Authentication.spec.type` | Client secret required? |
|---|---|---|
| `openshift` (integrated OAuth) | `""` / `IntegratedOAuth` / `None` | **Yes** — but console-operator generates it for you automatically |
| `oidc` (external OIDC) | `OIDC` | **Yes** — cluster-admin must manually provision a client secret Secret and reference it in the `Authentication` CR |
| `disabled` | n/a (bridge-local flag only) | No — full bypass or single static user |

Bottom line for your original question: **you can't avoid a client secret in
OIDC mode.** The `OIDCClientConfig.clientSecret` field is technically optional
at the API level ("public clients do not require a client secret"), but
console-operator does not implement a public-client flow — if `clientSecret` is
unset, the `oidcSetupController` marks the console `Degraded` with
`OIDCClientMissingSecret` and never provisions the bridge, i.e. **console
login stays broken until you supply one.**

---

## 1. Two layers of "auth" — don't conflate them

- **Cluster identity providers** (`OAuth.spec.identityProviders[]`: HTPasswd,
  LDAP, GitHub, GitLab, Google, Keystone, OpenID, RequestHeader, BasicAuth —
  `vendor/github.com/openshift/api/config/v1/types_oauth.go:172-246`). These
  back the **integrated OAuth server** (`oauth-openshift` pod) when
  `Authentication.spec.type` is `""`/`IntegratedOAuth`/`None`. They have
  nothing to do with OIDC mode — once you move to `Authentication.spec.type:
  OIDC`, `OAuth.spec.identityProviders` is irrelevant to console/API-server
  login; your external IdP's own user directory takes over entirely.
- **Console's own auth wiring** (this doc) — how the console *bridge* obtains
  and validates the browser's session token, i.e. whether it round-trips
  through the integrated OAuth server or talks OIDC discovery directly to an
  external IdP.

Both layers are governed by the single cluster-scoped
`Authentication.config.openshift.io/cluster` object
(`vendor/.../types_authentication.go:21-97`); console-operator reads
`.spec.type` off it and reconfigures the console bridge automatically —
no separate console-specific type switch exists.

---

## 2. Mode: `openshift` (integrated OAuth) — the default

### How it works
- `Authentication.spec.type` is `""`, `IntegratedOAuth`, or `None`
  (`config_builder.go:203-213` in console-operator).
- console-operator sets bridge flags: `authType=openshift`, points
  `ClientID` at the `console` `OAuthClient` CR, and CA-trusts the integrated
  OAuth server's serving cert.
- Bridge implementation: `pkg/auth/oauth2/auth_openshift.go` — discovers
  endpoints via the OpenShift OAuth well-known document, exchanges the
  authorization code with the OAuth server, same underlying engine as OIDC
  mode (`pkg/auth/oauth2/auth.go`).
- Login flow: browser → console `/auth/login` → redirect to
  `oauth-openshift` route → user authenticates against whichever
  `OAuth.spec.identityProviders[]` are configured (HTPasswd, LDAP, GitHub,
  etc.) → OAuth server redirects back to console with a code → bridge
  exchanges it for a token scoped as a Kubernetes `OAuthAccessToken`.

### Dependencies / what you must have
1. A running **integrated OAuth server** (`oauth-openshift` pod, its Route,
   its serving cert) — standard on self-managed OpenShift; **not present**
   on HyperShift-hosted clusters unless the HostedCluster's
   `configuration.authentication` selects it (see §4 below for the
   HyperShift angle).
2. At least one identity provider configured in
   `OAuth.spec.identityProviders[]` — otherwise nobody can log in even
   though the OAuth server itself is up.
3. An `OAuthClient` object named `console` in the `openshift-console`
   namespace's cluster scope — **console-operator creates and owns this
   automatically** (`oauthclients/oauthclients.go`); you don't touch it
   directly.
4. A client secret for that `OAuthClient` — **console-operator generates a
   random 256-bit secret itself** and stores it in `Secret
   console-oauth-config` (`openshift-console` ns) —
   `oauthclientsecret/oauthclientsecret.go:112-119`,
   `pkg/crypto.Random256BitsString()`. **No admin action needed.**

### What you configure
Nothing console-specific — just point `Authentication.spec.type` at
`IntegratedOAuth` (or leave it unset) and configure identity providers on the
`OAuth` CR (`cluster` singleton) as normal. This is the zero-config default
on every standard OpenShift cluster.

---

## 3. Mode: `oidc` (external OIDC provider)

### How it works
- `Authentication.spec.type: OIDC`, with exactly one entry in
  `Authentication.spec.oidcProviders[]` (API caps this at 1 —
  `types_authentication.go:87-96`, `+kubebuilder:validation:MaxItems=1`).
- console-operator looks inside that provider's `oidcClients[]` list for an
  entry matching `componentNamespace: openshift-console, componentName:
  console` (`subresource/authentication/cluster.go:26-42`,
  `api.OpenShiftConsoleName="console"`, `api.TargetNamespace=
  "openshift-console"`). If found, it sets bridge flags `authType=oidc`,
  `oidcIssuer=<issuer.url>`, `clientID=<oidcClients[i].clientID>`.
- Bridge implementation: `pkg/auth/oauth2/auth_oidc.go` — standard OIDC
  discovery (`coreos/go-oidc`) against `<issuer>/.well-known/openid-configuration`,
  same `oauth2.Config`/token-exchange engine as `openshift` mode
  (`pkg/auth/oauth2/auth.go:104-106`, `AuthSourceOIDC`).
- **Requires the `ExternalOIDC` (or `ExternalOIDCWithUIDAndExtraClaimMappings`
  / `ExternalOIDCWithUpstreamParity`) FeatureGate** to be enabled —
  `types_authentication.go:92-94`; `oidcsetup.go:127-133` — if the gate isn't
  on, the operator resets all OIDC status conditions and does nothing (API
  validation is assumed to block `spec.type=OIDC` when the gate is off).

### Dependencies / what you must have
1. An external OIDC issuer reachable from the console pod and from
   browsers (HTTPS, valid `/.well-known/openid-configuration`) — e.g. Google,
   Keycloak, Dex, Entra ID.
2. `ExternalOIDC` feature gate enabled on the cluster.
3. **A registered OIDC client on the external IdP for the console itself**,
   with:
   - a `clientID`
   - a redirect URI the IdP will accept:
     `https://console-openshift-console.<cluster-domain>/auth/callback`
   - (per the confidential-client requirement below) **a client secret**
4. A `Secret` in the `openshift-config` namespace holding that client
   secret under the `clientSecret` key
   (`OIDCClientConfig.clientSecret` → `SecretNameReference`,
   `types_authentication.go:531-545`).
5. Optionally, a `ConfigMap` in `openshift-config` with the issuer's CA
   bundle under `ca-bundle.crt`, referenced by
   `oidcProviders[].issuer.issuerCertificateAuthority`
   (`types_authentication.go:299-310`) if the IdP's TLS isn't trusted by the
   system CA pool — console-operator syncs it into `openshift-console` and
   mounts it at `/var/auth-server-ca/ca-bundle.crt`
   (`oidcsetup.go:206-220`, `config_builder.go:237-239`).

### What you configure — `Authentication` CR shape

```yaml
apiVersion: config.openshift.io/v1
kind: Authentication
metadata:
  name: cluster
spec:
  type: OIDC
  oidcProviders:
  - name: my-oidc-provider
    issuer:
      issuerURL: https://accounts.example.com
      audiences:
      - my-console-client-id       # must match the 'aud' claim in issued tokens
      issuerCertificateAuthority:  # optional, only if not publicly-trusted CA
        name: oidc-issuer-ca
    claimMappings:
      username:
        claim: email               # or 'sub', per your IdP
      groups:
        claim: groups
    oidcClients:
    - componentNamespace: openshift-console
      componentName: console
      clientID: my-console-client-id
      clientSecret:
        name: console-oidc-client-secret   # Secret in openshift-config ns
      extraScopes:
      - profile
    # Optional: also register the CLI ('oc') as an OIDC client so
    # 'Copy login command' works in the console UI:
    - componentNamespace: openshift-console
      componentName: cli
      clientID: my-cli-client-id
      # public CLI client: clientSecret can be omitted here in principle,
      # but console-operator's own console component MUST have one (see below)
```

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: console-oidc-client-secret
  namespace: openshift-config
type: Opaque
stringData:
  clientSecret: <the-secret-from-your-IdP>
```

### Client secret is *effectively* mandatory for console, even though the API says "optional"

The `OIDCClientConfig.clientSecret` field doc explicitly says:

> "Public clients do not require a client secret but private clients do
> require a client secret to work with the identity provider."
> (`types_authentication.go:541-542`)

...implying a public-client (no-secret, e.g. PKCE-based) flow is a supported
*concept* in the API. However, **console-operator's implementation does not
support running the console itself as a public client**:

- `oidcSetupController.syncAuthTypeOIDC` (`oidcsetup.go:190-193`): if
  `clientConfig.ClientSecret.Name` is empty, it sets condition
  `Degraded/OIDCClientMissingSecret: "no client secret in the OIDC client
  config"` and returns — **it does not proceed to configure the bridge**.
- `oauthClientSecretController.sync` (`oauthclientsecret.go:124-137`):
  under OIDC, if `clientConfig.ClientSecret.Name` is empty, it sets
  `Degraded/MissingClientSecretConfig` and never populates the
  `console-oauth-config` Secret.
- The bridge itself (`pkg/auth/oauth2/auth.go` + `authoptions.go:157-165` in
  the standalone bridge repo) requires `ClientSecret` or
  `ClientSecretFile` to be non-empty for **both** `openshift` and `oidc`
  auth types at flag-validation time — there is no PKCE/public-client
  code path in the bridge (`golang.org/x/oauth2` PKCE helpers exist in
  vendor but are never invoked).

So: the API schema is forward-looking / shared with other platform
components (e.g. `cli` client above genuinely can be public since `oc
login --exec-plugin oc-oidc` can do PKCE), but **the `console`
componentName specifically is hard-required to have a `clientSecret`** by
both controllers that wire it up. There is currently no way to run console
under OIDC without provisioning a client secret.

### Symptoms if you skip the secret
- `Authentication.status.oidcClients[]` entry for
  `componentNamespace=openshift-console, componentName=console` will show
  `Degraded: True, reason: OIDCClientMissingSecret`.
- Console `ClusterOperator`/operator `Console` resource will report
  Degraded (`OIDCClientConfigDegraded` condition).
- The bridge Deployment is never updated to `authType=oidc`, or is left in
  whatever it previously was — no live browser login will work.

### Extra optional bit: `oc-oidc` CLI login command
If you also register an `oidcClients[]` entry for
`componentNamespace: openshift-console, componentName: cli`
(`api.CLIOIDCClientComponentName = "cli"`), console-operator computes an
`oc login ... --exec-plugin oc-oidc --client-id ...` command
(`subresource/authentication/cluster.go:12-23`) shown in the console's "Copy
login command" dialog. This CLI client *can* be a genuinely public client
(no secret) since `oc-oidc` implements its own PKCE/loopback flow — it's a
separate `componentName` from `console` and is not gated by the same
"secret required" logic above (nothing in `oauthclientsecret.go`/
`oidcsetup.go` reads the `cli` client's secret at all — they only look at
`componentName: console`).

---

## 4. HyperShift-specific angle (relevant to this repo)

On a HostedCluster, the guest `Authentication` CR is written by HCCO from
`HostedCluster.spec.configuration.authentication`
(per `CONSOLE_CONTROL_PLANE_STUDY.md:188-195` in this repo). If an
`oidcClients[]` entry for the console component exists in the HostedCluster
spec, HyperShift copies it verbatim to the guest CR, and HCCO copies its
referenced `ClientSecret` down as well — so the "must provision a secret"
requirement above applies at the **HostedCluster spec** layer too: you need
a `Secret` (management-side, referenced by the HC spec) containing the
OIDC client secret for the console component before HyperShift will produce
a working guest `Authentication` CR for console OIDC login.

The known live gap tracked in this repo (`CONSOLE_CONTROL_PLANE_STUDY.md`
Gap 4) is specifically that **registering a web-client (with secret) on the
external IdP side** (e.g. Google) for the
`https://console.<domain>/auth/callback` redirect is a manual,
topology-independent setup step — it has nothing to do with HyperShift's
plumbing; it's the same external-IdP client registration burden described
in §3 above.

---

## 5. Mode: `disabled` — dev/debug only, do not use in production

Mentioned briefly since it's explicitly not a real option, but it's worth
knowing it exists when standing up a throwaway/dev environment where you
can't get IdP credentials at all:

- Set bridge flag `--user-auth=disabled`.
- With no `--k8s-auth-bearer-token`: authenticator is `nil` — **every
  request is treated as authenticated with no identity check whatsoever.**
  Logged as a loud warning.
- With `--k8s-auth-bearer-token=<token>` set: every request is authenticated
  as one fixed static user/token (`pkg/auth/static/auth.go`) — useful for
  single-operator local testing against a live cluster, still logged as
  `"AUTHENTICATION DISABLED -- for development use only!"`.
- Not wired to the `Authentication` CR at all — it's a bridge-local flag
  with no console-operator involvement (console-operator never sets
  `authType=disabled` itself in normal reconciliation — the only place it
  does is the OIDC-configured-but-no-matching-client fallback
  `config_builder.go:216-224`, which is really "OIDC intended but
  misconfigured", degrading to a broken/no-auth state rather than a
  deliberate choice).

**Do not use this for anything reachable by more than the person who set it
up.**

---

## 6. Summary decision table

| Question | `openshift` | `oidc` | `disabled` |
|---|---|---|---|
| Cluster `Authentication.spec.type` | `""`/`IntegratedOAuth`/`None` | `OIDC` | n/a |
| Feature gate needed | none | `ExternalOIDC` (or successor gates) | none |
| Client secret needed | Yes, but auto-generated by console-operator | Yes, admin must create `Secret` in `openshift-config` and reference it | No |
| Identity provider config | `OAuth.spec.identityProviders[]` (HTPasswd/LDAP/GitHub/...) | Whatever the external IdP manages | None |
| Admin action to enable console login | none beyond having any identity provider | 1) enable feature gate, 2) register client+secret on IdP, 3) create Secret in `openshift-config`, 4) add `oidcClients[]` entry to `Authentication` CR | Set one flag, accept the security hole |
| Production-suitable | Yes | Yes | **No** |

---

## Key file references

**console bridge** (`_console-research/`):
- `cmd/bridge/config/auth/authoptions.go:157-170` — client secret required
  for both `openshift` and `oidc` at flag-validation time
- `cmd/bridge/config/flagvalues/auth_type.go:8-11` — the 3-value enum
- `pkg/auth/oauth2/auth_openshift.go`, `pkg/auth/oauth2/auth_oidc.go` —
  per-mode discovery/login implementations
- `pkg/auth/oauth2/auth.go:104-106,270-276` — shared token-exchange engine
- `pkg/auth/static/auth.go` — `disabled`-mode static authenticator
- `pkg/serverconfig/types.go:89-99` — `Auth` config struct (YAML mirror of
  the flags)

**console-operator** (`_console-operator-research/`):
- `pkg/console/subresource/consoleserver/config_builder.go:202-243` —
  `Authentication.spec.type` → bridge `authType` mapping
- `pkg/console/subresource/authentication/cluster.go` —
  `GetOIDCClientConfig` / `GetOIDCOCLoginCommand` helpers
- `pkg/console/controllers/oidcsetup/oidcsetup.go:190-203` — degrades if
  `console` OIDC client has no secret
- `pkg/console/controllers/oauthclientsecret/oauthclientsecret.go:112-137` —
  secret sync for both modes; OIDC branch requires
  `clientConfig.ClientSecret.Name`
- `pkg/console/controllers/oauthclients/oauthclients.go` — `OAuthClient`
  CR lifecycle, only active for `openshift` mode
  (`oauthclients.go:125-132`)
- `pkg/api/*.go` — component name constants
  (`OpenShiftConsoleName="console"`, `CLIOIDCClientComponentName="cli"`,
  `TargetNamespace="openshift-console"`)

**openshift/api vendor**:
- `vendor/github.com/openshift/api/config/v1/types_authentication.go` —
  `Authentication`, `OIDCProvider`, `OIDCClientConfig` (secret optionality
  doc at lines 531-545), `TokenClaimMappings`
- `vendor/github.com/openshift/api/config/v1/types_oauth.go:172-246` —
  `IdentityProviderType` enum for integrated-OAuth mode

---

## 7. GCP HCP: the console OIDC client model (the Gap-4 blocker)

This section is the **authoritative deep analysis** for `CONSOLE_CONTROL_PLANE_GAPS.md` Gap 4.
The gaps file keeps only a summary + decision and points here.

### 7.1 What already works (verified live + across codebases)

Verified on a live GCP HCP cluster (Console capability *disabled*): guest `authentication/cluster`
mirrors `HostedCluster.spec.configuration.authentication` verbatim — `type: OIDC`, issuer
`https://accounts.google.com`, audience `32555940559.apps.googleusercontent.com`, claims
`username=email`/`groups=hd`, `issuerCertificateAuthority.name: ""`.

| Concern | State | Evidence |
|---|---|---|
| Issuer = Google `accounts.google.com` | ✅ works today | live CR |
| Guest `Authentication` CR write | ✅ **HCCO** owns it (copies HC spec verbatim) | `resources.go:1200-1202`; `globalconfig/authentication.go:20-22` |
| KAS token acceptance | ✅ HCP structured-auth (CPO) | live |
| Issuer CA delivery | ✅ HCCO copies cm → guest `openshift-config` (no-op for Google) | `resources.go:1538-1564` |
| Client-secret **delivery** | ✅ HCCO copies `OIDCClients[].ClientSecret` → guest | `resources.go:1566-1574` |
| Bridge login path | ✅ spec-driven (`authType=oidc`, issuer, clientID) | `config_builder.go:215-243` |
| `status.oidcClients` write | ✅ status-only, NOT required for login | `oidcsetup.go:173` |

### 7.2 Delivery exists — registration does NOT (the trap)

- **Delivery** (once an `oidcClients[]` entry exists): fully automatic — HyperShift copies the entry
  to the guest, HCCO copies the secret. No new code.
- **Registration** (creating the `openshift-console/console` entry): **exists nowhere.**
  console-operator only *reads* it (`cluster.go:26-45`; missing → console Unavailable,
  `oidcsetup.go:187-190`) and writes only `.status`, never `.spec`. HyperShift/CPO/CVO never
  synthesize it. Same in standard OpenShift. ⇒ the console OIDC client is an
  **externally-provisioned input**.

### 7.3 Why the console needs its OWN confidential client (can't reuse the default Google client)

- **KAS audience** uses Google's shared client (`32555940559…`) as an *audience* only — no secret,
  no redirect. ✅ Fine, unchanged.
- **The console bridge** is a **confidential web app**: it does a code→token exchange with a real
  `ClientSecret` (`auth.go:404`, `serverconfig/types.go:95`) and needs a registered web redirect URI
  (`console.<domain>/auth/callback`). The shared/gcloud client is public/installed (loopback/OOB
  redirects only) — **structurally wrong**. So a dedicated Google **Web application** client is
  mandatory.
- **Topology-independent:** the bridge's OIDC path is gated only on `authType==OIDC`
  (`config_builder.go:215`) — identical requirement for **data-plane** and control-plane-side,
  HyperShift or standalone. The control-plane-side port adds no auth risk; it surfaced a pre-existing
  "console + external OIDC on GCP" gap.

### 7.4 The hard Google constraints (verified Sept 2026)

- **No API/gcloud/Terraform to create a Web OAuth client** — Cloud-Console-only.
  (`support.google.com/cloud/answer/15549257`; `google_iap_*` is IAP-only + removed from Terraform,
  terraform-provider-google#28973.)
- **No wildcard redirect URIs** — exact-match, HTTPS, Console-only to add/edit.
- Client secret shown **once** at creation.

⇒ **Hard constraint for a managed fleet:** *creating a hosted cluster MUST NOT require a manual
Google step.* Any design where **Google sees a per-cluster URL** violates this.

### 7.5 Client-model options

| # | Model | No manual per-cluster step? | Secret isolation | Notes |
|---|---|---|---|---|
| 1 | Fleet-shared client, per-cluster redirects | ❌ (manual redirect edit/cluster) | ❌ fleet-wide shared secret | eliminated |
| 2 | **State-based redirect broker** | ✅ | ✅ (design A + day-2, or design B) | **lead candidate** |
| 3 | Manual per-cluster client | ❌ | ✅ per-tenant | simplest, unscalable |
| 4 | **Alternate IdP w/ dynamic registration** fronting Google | ✅ | ✅ | larger architecture change |
| 5 | Switch cluster to IntegratedOAuth + OpenID-IdP→Google | ❌ (IdP redirect still per-cluster) | ✅ | fleet-wide auth-model change |

**Only options 2 and 4 satisfy the hard constraint.**

#### Option 2 — the state-based redirect broker (lead candidate)

- **Mechanism:** one **fixed** Google redirect URI for the whole fleet (registered once). The target
  cluster is carried in the OAuth **`state`** (opaque to Google). Broker receives Google's callback,
  reads `state`, and **302s the browser** to the right console. Adding clusters = **zero Google
  changes**. A *naive* broker that forwards each console's own `redirect_uri` does NOT qualify.
- **`state` handling — anti-open-redirect:** `state` must be **signed/opaque** and resolved to a
  target via an **allowlist**; never trust a raw URL in `state` (that's an open redirect that leaks
  the auth `code`).
- **Two sub-designs:**
  - **A — dumb bouncer (recommended):** broker only 302s the browser; the **bridge** still does the
    token exchange (`auth.go:404`) and owns the session. Broker needs **no KAS/console network
    access**, **never sees tokens** (only a short-lived `code` in one redirect). Client secret stays
    per-bridge → pair with the **day-2 pattern** (§7.6) for secret isolation.
  - **B — broker terminates the exchange:** holds the secret, does the exchange, forwards tokens to
    bridges. Keeps the secret off tenants, but becomes a **fleet-wide token chokepoint** (must reach
    every bridge; high blast radius).
- **Users:** never authenticate *to* the broker; only the **browser** transits it once per login.
  Not in the API/console data path afterward.
- **Broker ≠ console domain:** they can be on **different domains** — only the **broker's** URL is
  registered at Google; console callback URLs stay off Google's radar (browser reaches them via the
  broker's 302). Requirements: both HTTPS, both reachable by the browser. Keep the `state` cookie
  first-party to the console (as today, `auth.go:330-338`) — a dumb bouncer touches no cookies.
- **Private clusters — works, no public endpoint added.** Google **never dials** the redirect URI;
  it 302s the **browser**, which for a private cluster is already private-side. So a **private broker
  with a private HTTPS redirect URI** works end-to-end (Google → private browser → private broker →
  private console). Google accepts a private DNS host at registration (string match; HTTPS required).
  Cost: broker must be browser-reachable → PSC-published **per connectivity domain** (like private
  API/console), i.e. **per-domain, not per-cluster** (the `state` trick still collapses clusters).
  One Google client can list both public and private redirect URIs.
- **Not configurable in the bridge today (needs upstream work).** The bridge's
  `--additional-base-addresses`/`allowedRedirectHosts` + `oauth2ConfigForHost` (`auth.go:281-296`)
  only rewrite the redirect to **the console's own request Host** — never to a separate broker — and
  each such host would still need Google registration. There is no flag to send a fixed broker URL
  as `redirect_uri` with cluster-identity in `state`. So option 2 requires **upstream console/bridge
  changes** (or a broker that fully wraps the flow).

#### Option 5 — IntegratedOAuth + OpenID IdP → Google (recorded for completeness)

Run the integrated OAuth server with an OpenID IdP pointing at Google
(`OAuth.spec.identityProviders[].type: OpenID`, `configrefs/refs.go:165-167`). Console then uses
`--user-auth=openshift` with its **auto-generated `OAuthClient`** — no console-specific Google client.
**But:** (a) flips the cluster's whole auth model back to integrated OAuth (`oauth-openshift` server
returns; `oc`/KAS tokens change; today OFF because `type:OIDC` disables it, `support/util/oauth.go:17-22`);
(b) the `redirect_uri` pain only **moves** to the OAuth server's IdP callback
(`oauth.<domain>/oauth2callback/google`) — still per-cluster, still no wildcard; (c) contradicts GCP
HCP's external-OIDC design. Not a lightweight console fix.

### 7.6 Secret isolation — the day-2 pattern (ARO-HCP precedent, OCPSTRAT-2173)

By **default** the client secret is created **mgmt-side** and HCCO copies it **down** to the guest
(`resources.go:1567-1571`) — so management always sees it. The
**`hypershift.openshift.io/hosted-cluster-sourced`** annotation
(`api/hypershift/v1beta1/hostedcluster_types.go:446-457`) **inverts** this: HCCO **skips the copy**
(`resources.go:1576-1579`), management holds only an empty placeholder, and the **real secret lives
only in the guest**, supplied by the end-user. Exactly the isolation wanted for a console client.

- **Limitation (verified `:454-456`):** honored **only for ARO-HCP** today, only for the OIDC
  clientSecret. **GCP would need to extend honoring to its platform** (small, proven mechanism).
- **Tracking:** [OCPSTRAT-2173](https://redhat.atlassian.net/browse/OCPSTRAT-2173).
- **Trade-off:** solves secret exposure, but the customer must still register the Google client
  manually and create the guest Secret + `oidcClients[]` entry.

### 7.7 What the customer configures (guest-side)

1. **Secret** `openshift-config/<name>` (guest), key `clientSecret`. (Day-2: created in-guest,
   annotated `hosted-cluster-sourced`; else referenced from the HC spec and copied down.)
2. **`oidcClients[]` entry** in the guest `Authentication` CR, keyed `componentName: console` /
   `componentNamespace: openshift-console`, with `clientID` + `clientSecret.name`. (`clientSecret`
   is API-optional but console-operator **hard-requires** it — missing → `OIDCClientMissingSecret`,
   §3.)

### 7.8 Bottom line

Base OIDC + secret **delivery** are solved. The **gating decision** is the client model, bound by
"cluster creation must be Google-manual-step-free" → realistically **option 2 (state-based broker)**
or **option 4 (alternate IdP)**. Both need product/platform + security input; option 2 also needs
upstream bridge changes. Then the `oidcClients[]` entry + secret (via day-2) → login is automatic.
Separately confirm `ConsolePublicURL`→KAS (`kas/params.go:77`, `kas/config.go:151`) for the
`oc-oidc` login command when the operator runs control-plane-side (minor).
