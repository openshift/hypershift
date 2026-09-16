# Console kustomize layers

Three layers, each building on the last:

| Layer | What it is | Builds standalone? |
|---|---|---|
| `origin/` | Verbatim upstream `openshift/console-operator` static assets for the `console` **and** `downloads` operands (deployment/service/route/pdb/serviceaccount each). Cited, unmodified, for diffing. | No — has operator-only placeholders (`${IMAGE}`, no volumes, no `spec.host`). |
| `hypershift/` | Generic, cluster-agnostic patch of `origin` for running the core console control-plane-side with **no console-operator** (Phase 1). Every patch is commented with *why* (what the operator would otherwise inject). | No — still has `REPLACE_*` placeholders. |
| `pat-console/` | Per-HostedCluster overlay: real namespace, hostnames, image digest, and guest token for the live `pat-console` cluster. | Yes — `./apply.sh` deploys it. |

Add a new per-cluster overlay by copying `pat-console/` and swapping its values; `hypershift/` should rarely need to change.

## Downloads operand (CLI download server)

The `downloads` operand serves the `oc`/CLI download page. Two Phase 1 notes:

- **TLS sidecar:** the HCP router is SNI passthrough only and can't do the
  upstream downloads Route's `edge` TLS termination, so the pod runs an
  `oauth-proxy` sidecar (auth bypassed via `-skip-auth-regex=^/`) that
  terminates TLS on 8443 and forwards to the verbatim upstream
  download-server on `127.0.0.1:8080`.
- **UI link gap (not wired):** the console UI's "Command Line Tools" page
  lists `ConsoleCLIDownloads` CRs read from the guest cluster. On this HC the
  `Console` capability is disabled, so that CRD isn't installed and no CRs
  exist — the downloads server is reachable at its own host, but the UI link
  stays empty until the CRD + an `oc-cli-downloads` CR (pointing at the
  downloads host) are added to the guest. That guest-side wiring is deferred.

See `CONSOLE_CONTROL_PLANE_DOCS/CONSOLE_CONTROL_PLANE_PHASE1_PLAN.md` and `CONSOLE_CONTROL_PLANE_STUDY.md` for the design this implements.
