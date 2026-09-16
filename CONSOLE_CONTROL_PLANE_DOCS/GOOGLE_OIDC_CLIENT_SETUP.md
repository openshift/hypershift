# Creating a Google OAuth client for console login

How to create a Google Cloud OAuth 2.0 **web application** client (ID + secret)
so the control-plane-side console can do real per-user login via OIDC, replacing
the Phase 1 `-user-auth=disabled` + static token.

## Background

- The guest **kube-apiserver** already trusts Google as an external OIDC issuer
  (`HostedCluster.spec.configuration.authentication`, `type: OIDC`, issuer
  `https://accounts.google.com`). Its `audiences` entry
  (`32555940559.apps.googleusercontent.com`) is the public gcloud CLI client —
  used to *validate* tokens, not to log users in from a browser.
- The **console bridge** needs its OWN OAuth client (a Google "Web application"
  client with a redirect URI back to the console) to run the browser login flow
  and obtain ID tokens. That is what this doc creates.
- For the KAS to accept the console-issued tokens, the console client's ID must
  be an accepted **audience** on the KAS OIDC config (see "Wire it up" below).

## Prerequisites

- Access to the Google Cloud project's **APIs & Services → Credentials** page
  (or `gcloud`), in a project where you can manage OAuth clients.
- The console's public base address, e.g.
  `https://console.pat-console-user.us-central1-nkcw-1.int.gcp-hcp.openshiftapps.com`.

## Create the OAuth client (Console UI)

1. Google Cloud Console → **APIs & Services → OAuth consent screen**. If not
   already configured, set it up (Internal user type if this is a Google
   Workspace org and you only need org users; otherwise External). Add the
   scopes `openid`, `email`, `profile`.
2. → **Credentials → Create Credentials → OAuth client ID**.
3. **Application type: Web application.** Name it e.g. `pat-console-web`.
4. **Authorized redirect URIs** — add exactly (path is fixed by the bridge,
   `AuthLoginCallbackEndpoint = /auth/callback`):
   ```
   https://<console-base-address>/auth/callback
   ```
   e.g. `https://console.pat-console-user.us-central1-nkcw-1.int.gcp-hcp.openshiftapps.com/auth/callback`
5. (Authorized JavaScript origins are not required for the server-side flow;
   leave empty.)
6. Create → copy the **Client ID** and **Client secret**.

## Create the OAuth client (gcloud, alternative)

`gcloud` has no first-class command to mint OAuth *client* credentials
(consent-screen clients are managed in the Console UI / IAP API). Use the
Console UI above. (Do not confuse this with service-account keys — those are a
different credential type and won't work for browser login.)

## Wire it up (console bridge)

Store the secret in a gitignored file (never commit it) and pass the bridge
these flags (replacing the Phase 1 `-user-auth=disabled` / static token):

```
-user-auth=oidc
-user-auth-oidc-issuer-url=https://accounts.google.com
-user-auth-oidc-client-id=<CLIENT_ID>.apps.googleusercontent.com
-user-auth-oidc-client-secret-file=/var/oidc/clientSecret   # mount the secret (key: clientSecret); or -user-auth-oidc-client-secret=<...>
-user-auth-oidc-token-scopes=email,profile                  # REQUIRED — see the email-claim note below
-cookie-encryption-key-file=/var/session/encryptionKey      # 32-byte AES key
-cookie-authentication-key-file=/var/session/authenticationKey  # 64-byte HMAC key
```

Notes:
- `-user-auth=oidc` is mutually exclusive with `-k8s-auth-bearer-token` /
  `-user-auth=disabled` (the bridge rejects both together).
- The bridge validates the issuer's discovery doc at startup
  (`https://accounts.google.com/.well-known/openid-configuration`), reachable
  from the console pod on the management cluster.
- **KAS audience:** for the guest KAS to accept the ID token the console
  forwards, add the console client ID to the KAS OIDC `audiences`
  (`spec.configuration.authentication.oidcProviders[].issuer.audiences`).
  Otherwise resource calls are rejected with an audience mismatch even though
  login succeeds.
- **`email,profile` scopes are required.** This HC maps `username ← email`; with
  only the default `openid` scope Google omits the `email` claim and the guest
  KAS rejects every request with `oidc: parse username claims "email": claim not
  present` (surfacing as a generic "Authentication error" *after* a successful
  login). The bridge builds the redirect scope as `<token-scopes> + openid`, so
  `-user-auth-oidc-token-scopes=email,profile` yields `email profile openid`.
  (`groups ← hd` is also mapped; consumer/non-Workspace accounts have no `hd`
  claim, which is tolerated — they just get no groups.)
- **Session cookie keys are required for `-user-auth=oidc`.** The bridge stores
  the ID token in an encrypted (AES, 32-byte key) + signed (HMAC, 64-byte key)
  cookie. Normally the console-operator generates these; here supply a
  `console-session` Secret (keys `encryptionKey` 32B, `authenticationKey` 64B)
  and point the two `-cookie-*-key-file` flags at it. Regenerating the keys
  invalidates all existing sessions.

## Sequencing: the client ID and the KAS audience are coupled

The hard constraint that drives sequencing: **the KAS OIDC `audiences` entry for
the console must equal the OAuth client ID.** For the browser authorization-code
flow the console uses, Google always sets the ID token `aud` to the OAuth client
ID and does not allow a custom audience (per Google's OIDC docs: `aud` "must be
one of the OAuth 2.0 client IDs of your application"). So the audience can't be
an arbitrary string like `console` or the console URL — it is the client ID, and
it's unknown until the client exists. (Custom `aud` values are only possible in
Google's service-account / `target_audience` token flows, which browser
user-login does not use.)

Because of that coupling, there are two viable shapes. Both still require the
**client secret** to be supplied out-of-band (never in git); which cluster it
lands in differs.

**Option A — enable console from the start (client ID known at HC-create time).**
The admin creates the GCP OAuth client first, then creates the HostedCluster with
the client ID already in the KAS `audiences`. Day-2, the admin injects the client
**secret** guest-side. This is essentially what the PoC does (client already
existed, so the HC manifest carries the audience up front).

**Option B — enable console after the cluster is up.** Create the HostedCluster
first (no console audience yet), bring it up, create the GCP OAuth client against
the now-known console host/redirect URI, then update the HostedCluster **via the
management API** to add the client ID to the KAS `audiences`, and create the
client-secret Secret.

**Open item — the `oidcClients` spec entry.** Beyond the audience, the "proper"
registration is an `oidcProviders[].oidcClients[]` entry (`componentName: console`
+ client-secret ref). Today that entry is **not admissible in this topology**: the
guest `authentication/cluster` CEL requires a matching `status.oidcClients`, which
only a running guest console-operator writes — with the guest `Console` capability
off / zero-node, nothing writes it, so including the spec entry blocks the whole
auth-config push (including the audience). We therefore run with **audience +
bridge flags only** and omit the `oidcClients` entry; login works. Making the
`oidcClients` entry usable requires a control-plane-side owner of
`status.oidcClients` (see `CONSOLE_CONTROL_PLANE_STUDY.md` §23). Note the API
also requires `oidcClients[].clientID` to be non-empty (`MinLength=1`), reinforcing
that the client must exist before it can be registered.

**Productization note:** the HCP frontend / lifecycle layer needs a "enable
console with *this* OIDC client ID (+ secret ref)" operation covering both options
above — producing the KAS audience entry, the bridge OIDC flags, the session-key
Secret, and eventually the `oidcClients` wiring once the status owner exists. This
is the operator/lifecycle piece that replaces the hand-applied manifests here.

## Redirect URI must match per cluster/host

The redirect URI is tied to the console's base address. Under **Private**
endpointAccess the browser still uses the same public hostname (only the DNS
target changes to the PSC IP), so the redirect URI is unchanged. If the console
host changes, add the new `/auth/callback` URI to the Google client.

## Security

- The client secret is a credential — store it in a Kubernetes Secret / a
  gitignored file, mount it via `-user-auth-oidc-client-secret-file`, never in
  the manifest or git.
- Restrict the OAuth consent screen to the intended org/users; with
  `groups` ← `hd` (hosted domain), only users in the expected Workspace domain
  map to the `redhat.com`-style group.
