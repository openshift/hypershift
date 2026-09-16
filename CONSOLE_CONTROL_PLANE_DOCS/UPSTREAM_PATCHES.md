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

## Notes / candidates not yet filed

- **`hypershift` (control-plane-operator):** the router `console`/`downloads` backend cases
  (`v2/router/config.go`, `manifests/ingress.go`) are currently local changes in *this* repo/branch,
  not upstream. If the control-plane-side console graduates beyond a spike, these need to land in
  `openshift/hypershift` (as a generic labeled-Route mechanism or an owned component) rather than a
  per-route hardcode. No PR/Jira yet.
- **`openshift/console-operator` / CVO:** none required for Part 1. A future phase that makes the
  control-plane-side console operator-managed (capability gate, placement flag) would touch these —
  add rows here when that work starts.

## When a patch ships

1. Confirm the fix is in the release payload we run (`oc adm release info --image-for=<component> <release>`).
2. Remove the local workaround (custom image / flag) from `console/kustomize/`.
3. Update the row to `shipped`, and close the tracking Jira.
