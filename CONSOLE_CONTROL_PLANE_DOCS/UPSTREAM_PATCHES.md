# Upstream patches tracker

Changes this spike needs in repos we don't own. One row per upstream change so
we can tell, at a glance, what's carried as a local/fork patch vs. merged and
shipped in a release payload (at which point we drop the workaround).

**Status legend:** `local` = patched only in our fork/custom image · `pr-open` =
PR filed upstream · `merged` = merged upstream · `shipped` = in a release payload
we consume (safe to drop the local workaround).

| Repo | Change | Why we need it | Jira | PR | Status | Local workaround (drop when shipped) |
|------|--------|----------------|------|----|--------|--------------------------------------|
| `openshift/console` | Honor `-ca-file` for the off-cluster k8s resource proxy TLS trust (wire it into `serviceProxyTLSConfig.RootCAs`, `cmd/bridge/main.go`) | Off-cluster bridge can't verify the HyperShift guest KAS (private `root-ca`) without it; only skip-verify worked | [GCP-1219](https://redhat.atlassian.net/browse/GCP-1219) | [openshift/console#17185](https://github.com/openshift/console/pull/17185) | pr-open | Custom image `quay.io/patmarti/console:*` (built by `console/build-console.sh`, branch `off-cluster-ca-file-trust`). Revert overlay `images:` to the stock release console digest once shipped. |
| `openshift/hypershift` (CPO) | Own the console/downloads exposure Routes + Private ExternalName services + router backends (GCP) | The HCP router only builds backends for known route names, and Private needs ExternalName services for external-dns → PSC; console/downloads aren't HyperShift service types | [GCP-1202](https://redhat.atlassian.net/browse/GCP-1202) | [openshift/hypershift#9622](https://github.com/openshift/hypershift/pull/9622) (draft/RFC) | pr-open | Custom CPO image (`console/build.sh`); carried on branch `console-control-plane-study`. Console/downloads-specific hardcode — RFC proposes a generic mechanism. |

## Notes / candidates not yet filed

- **`hypershift` (control-plane-operator):** local changes in *this* repo/branch, not upstream —
  CPO owns the console/downloads exposure end to end (GCP only), mirroring the KAS model:
  1. Router `console`/`downloads` backend cases, incl. the `-private` route names
     (`v2/router/config.go`).
  2. Console/downloads Route ownership: public vs `-private` variant per `endpointAccess`, host
     derived from the APIServer host (`console/route.go`, `infra/infra.go`, `manifests/ingress.go`).
  3. Private-mode `console`/`downloads` ExternalName services so external-dns publishes their
     records to the PSC endpoint (`gcpprivateserviceconnect/psc_endpoint_controller.go`,
     `manifests/infra.go`).

  All are console/downloads-specific hardcodes. If the control-plane-side console graduates beyond
  a spike, they should become a generic labeled-Route mechanism (or an owned component) in
  `openshift/hypershift`. Draft/RFC PR (preview, not for merge as-is; docs + kustomize included
  for context): https://github.com/openshift/hypershift/pull/9622. Needs a `CNTRLPLANE-`/`OCPBUGS-`
  prefix to merge. See `PRIVATE_ENDPOINT_ACCESS.md`.
- **`openshift/console-operator` / CVO:** none required for Part 1. A future phase that makes the
  control-plane-side console operator-managed (capability gate, placement flag) would touch these —
  add rows here when that work starts.

## When a patch ships

1. Confirm the fix is in the release payload we run (`oc adm release info --image-for=<component> <release>`).
2. Remove the local workaround (custom image / flag) from `console/kustomize/`.
3. Update the row to `shipped`, and close the tracking Jira.
