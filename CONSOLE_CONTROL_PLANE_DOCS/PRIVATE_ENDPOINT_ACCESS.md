# Private endpointAccess — console/downloads exposure & how to reach it

How the control-plane-side console and CLI-downloads server are exposed when the
HostedCluster runs with `spec.platform.gcp.endpointAccess: Private`, and how to
reach them from a browser (there is no public path in Private mode).

Validated live on `pat-console` (zero-node, GCP, `endpointAccess: Private`):
console `/health` and downloads `/` both return HTTP 200 through the PSC path
with a valid (non-`-k`) TLS chain.

## What changes vs PublicAndPrivate

Under Private, GCP tears down the public router LoadBalancer. All traffic
reaches the HCP via a **Private Service Connect (PSC) endpoint** inside the
customer VPC (`patmart-b3bb-network`), reachable only from inside that VPC (or a
peered/VPN network). The per-host public DNS records are repointed from the
public LB IP to the PSC endpoint IP.

For `pat-console` the PSC endpoint IP is `10.0.0.5`; from inside the VPC:

```
api.pat-console-user…        → 10.0.0.5
oauth.pat-console-user…      → 10.0.0.5
console.pat-console-user…    → 10.0.0.5
downloads.pat-console-user…  → 10.0.0.5
```

The user-facing hostnames are unchanged (`*.pat-console-user…`); only the A
record target changes. The Let's Encrypt wildcard cert still validates because
the router is SNI passthrough and presents the same cert regardless of path.

## How the DNS repoint happens (api/oauth and console/downloads)

external-dns (mgmt-cluster-wide, `hypershift` namespace) owns the
`*.pat-console-user…` A records. It runs with
`--label-filter=hypershift.openshift.io/route-visibility!=private`, i.e. it
**ignores Routes labeled `route-visibility=private`** and instead sources the
record from a small **ExternalName Service** annotated with
`external-dns.alpha.kubernetes.io/hostname=<host>` and pointing at the PSC IP.

So per user-facing host, under Private there are two objects:

1. A **Route** labeled `route-visibility=private` (external-dns ignores it) —
   this is what the HCP HAProxy router turns into an SNI backend.
2. An **ExternalName Service** (`<name>-private-external`, `externalName: <PSC
   IP>`, hostname annotation) — this is what external-dns publishes as the A
   record → PSC IP.

CPO already did this for `api`/`oauth`. This work extends the same pattern to
`console`/`downloads` (see below).

## CPO ownership of the console/downloads routes (GCP)

CPO reconciles the console/downloads exposure Routes itself (GCP only; not gated
by the console capability yet — Phase 2). It mirrors the KAS public/private
route model:

- **Public / PublicAndPrivate:** a public Route (`console` / `downloads`), no
  visibility label → external-dns publishes the host to the public router LB.
- **Private:** a `-private` Route (`console-private` / `downloads-private`)
  labeled `route-visibility=private` + `internal-route=true`, plus a matching
  ExternalName Service (`console-private-external` /
  `downloads-private-external`) → external-dns publishes the host to the PSC IP.

CPO derives the host from the APIServer host by swapping the first DNS label
(`api.<domain>` → `console.<domain>` / `downloads.<domain>`), so console/
downloads live in the same zone as api/oauth with no separate ingress-domain
plumbing. The router config generator maps both the public and `-private` route
names to the same backend (`:8443`).

Code:
- `control-plane-operator/controllers/hostedcontrolplane/console/route.go` —
  host derivation + public/private Route reconcile.
- `control-plane-operator/controllers/hostedcontrolplane/infra/infra.go` —
  `reconcileConsoleRoutes` (GCP-gated), swaps public/private per `IsPublicHCP`.
- `control-plane-operator/controllers/hostedcontrolplane/manifests/ingress.go`
  — `ConsoleRoute`/`ConsolePrivateRoute`/`DownloadsRoute`/`DownloadsPrivateRoute`.
- `control-plane-operator/controllers/hostedcontrolplane/manifests/infra.go` —
  `ConsoleExternalPrivateService`/`DownloadsExternalPrivateService`.
- `control-plane-operator/controllers/gcpprivateserviceconnect/psc_endpoint_controller.go`
  — creates the console/downloads ExternalName Services under Private (hosts
  derived the same way).
- `control-plane-operator/controllers/hostedcontrolplane/v2/router/config.go` —
  router backends for both route-name variants.

The kustomize tree (`console/kustomize/`) no longer manages these Routes; the
`hypershift` layer `$patch: delete`s the verbatim upstream Routes since CPO owns
them. The console Deployment's `-base-address` (per-cluster overlay) must match
CPO's derived host.

> This is per-route hardcoding for the spike. See `UPSTREAM_PATCHES.md` for the
> note that it should become a generic labeled-Route mechanism (or an owned
> component) upstream if it graduates.

## Reaching console/downloads from a browser (SSH SOCKS bastion)

Private endpoints resolve/route only inside the VPC, so browse through a small
bastion VM in `patmart-b3bb-network` using an SSH dynamic (SOCKS5) tunnel — no
extra software on the VM, TLS/SNI preserved end-to-end.

Live bastion (project `patmarti-hcp-test`):

| Resource | Value |
|----------|-------|
| VM | `console-bastion`, zone `us-central1-a`, in `patmart-b3bb-network`/`patmart-b3bb-subnet` |
| External IP | `34.135.189.195` (ephemeral) |
| Firewall | `console-bastion-allow-ssh` (tcp:22, source `0.0.0.0/0`, target tag `console-bastion`) |

In-VM validation (proves the private path):

```
$ getent hosts console.pat-console-user…   → 10.0.0.5
$ getent hosts downloads.pat-console-user… → 10.0.0.5
$ curl … https://console.pat-console-user…/health   → http_code=200
$ curl … https://downloads.pat-console-user…/       → http_code=200
```

### Browser runbook

1. Open the SOCKS tunnel (leave it running):

   ```
   gcloud compute ssh console-bastion --project patmarti-hcp-test --zone us-central1-a -- -D 1080 -N
   ```
   (or, with the key directly: `ssh -D 1080 -N patmarti@34.135.189.195`)

2. Firefox → Settings → Network Settings → Manual proxy configuration:
   - SOCKS Host `127.0.0.1`, Port `1080`, **SOCKS v5**
   - **Enable "Proxy DNS when using SOCKS v5"** (about:config
     `network.proxy.socks_remote_dns = true`) — DNS must resolve at the VM,
     where `console…` → `10.0.0.5`.
   - Tip: use FoxyProxy or a dedicated Firefox profile to scope the proxy to
     `*.pat-console-user…` instead of proxying all traffic.

3. Browse:
   - `https://console.pat-console-user.us-central1-nkcw-1.int.gcp-hcp.openshiftapps.com`
   - `https://downloads.pat-console-user.us-central1-nkcw-1.int.gcp-hcp.openshiftapps.com`

### Teardown

```
gcloud compute instances delete console-bastion --project patmarti-hcp-test --zone us-central1-a
gcloud compute firewall-rules delete console-bastion-allow-ssh --project patmarti-hcp-test
```

> Security note: the SSH firewall rule allows tcp:22 from `0.0.0.0/0` (key-only
> auth). Lock `--source-ranges` to your egress IP if the bastion is long-lived.
