# Diagnostic hints

You MUST use these only as starting points when triaging a presubmit **candidate**. You
MUST verify the actual signature in the surrounding logs and later runs, and MUST NOT turn a
matching string into a conclusion on its own.

- `failed to acquire lease` — infrastructure capacity or lease failure (often flake/infra).
- `etcdserver: leader changed` / `waiting for etcd cluster` — control-plane stability.
- `failed to create VirtualMachine` / `node not ready` — virtualization or management-cluster health.
- `BareMetalHost provisioning failed` — bare-metal provisioning.
- `upgrade precondition failed` / `ClusterVersion degraded` — upgrade / version compatibility.
- `exceeded quota` / `Found more than one resource` — cloud quota or resource ambiguity.
- `oidc: token verification failed` — identity-provider configuration.

A common, actionable signature repeated across independent PR heads points to a **real
break** (→ permafailing); unrelated failures each run point to **flaky** or per-PR issues.
`ERROR`/`ABORTED` and infra-only sparkline slots are a retest tax, not product failures.
