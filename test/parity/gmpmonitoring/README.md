# GMP monitoring parity guard

This package keeps the Google Managed Prometheus (GMP) monitoring backend from
drifting behind the default `monitoring.coreos.com` backend.

HyperShift components expose metrics through CoreOS `ServiceMonitor`,
`PodMonitor` and `PrometheusRule` resources. The GMP path needs equivalent
scrape targets and rules. This first PR generates `PodMonitoring` files into
`v2/gmpassets`, outside the CPO's automatically reconciled `v2/assets` tree.
A later gated runtime PR will load them. The flag is not a runtime switch in
this PR.

`TestGMPMonitoringParity` checks the staged scrape configuration offline: it
renders the CoreOS control-plane assets, staged GMP assets and management-level
install constructors into an inventory, resolves
every CoreOS monitor to the concrete pods, ports, paths and schemes it scrapes,
resolves every GMP `PodMonitoring` the same way, and fails when a CoreOS target
has no equivalent GMP endpoint.

## What the test does

1. **Discovers** every component under
   `control-plane-operator/controllers/hostedcontrolplane/v2/assets`, loading
   each manifest through `cpov2assets.LoadManifestTemplated` so templated
   components (`etcd`, shards such as `etcd-events`) render exactly as they do
   at reconcile time. The management-level monitors and rule groups built by
   `hypershift install` are added from their Go constructors.
2. **Resolves** each CoreOS `ServiceMonitor`/`PodMonitor` endpoint to a
   `scrapeTarget`: namespace, effective pod selector, container port, path and
   scheme. Named Service ports take precedence over `targetPort`; an empty path
   defaults to `/metrics` and an empty scheme to `http`. Discovery failures
   (no matching Service, no backing pod template, no such port) are errors, not
   skips, because silently dropping a target would make the guard pass while the
   target is unscraped.
3. **Loads generated GMP assets** from `v2/gmpassets`, rendering component
   templates where needed. These files are staged data and are not reconciled by
   this PR.
4. **Compares** coverage on `namespace|selector|port|path|scheme`. The component
   name is deliberately excluded: one GMP `PodMonitoring` may consolidate
   endpoints that CoreOS splits across several monitors. A narrower or wider
   selector, a different port, path or scheme all count as uncovered, so selector
   and port drift is detected, not just fully missing monitors.
5. **Checks rules separately.** CoreOS `PrometheusRule` groups must have a GMP
   `Rules` group with the same name in the same namespace and at the audited
   scope. GMP `Rules` only process metric data from their own namespace, so the
   scope is an explicit, audited decision per group rather than a mechanical
   mapping: `auditedRuleScopes` in `parity_test.go` must list every discovered
   group. A group that needs `ClusterRules` or `GlobalRules` fails until the
   vendored API and the reconcilers support it.
6. **Applies waivers.** Targets and rule groups that are intentionally not yet
   ported are listed in [`waivers.yaml`](waivers.yaml) with a `tracking` issue and
   a `reason`.

## Adding coverage

When you add a GMP `PodMonitoring` (or `Rules`) for a component that already has
a CoreOS monitor, the target becomes covered automatically. Delete the
corresponding waiver in the same change. The test fails on a stale waiver, so a
waiver cannot outlive the gap it documents.

When you add a component, monitor or rule group on the CoreOS path, the test
fails until you either port it or add a waiver. That is the point: the decision
is recorded in review instead of discovered when a dashboard goes blank.

When a rule group has a new name or namespace, also add it to
`auditedRuleScopes` so its GMP scope is chosen deliberately.

## Waivers

```yaml
scrapeTargets:
  - component: operator
    namespace: hypershift
    selector: name=operator   # sorted key=value pairs, comma-joined
    port: 9000
    path: /metrics
    scheme: http
    tracking: GCP-1293
    reason: HyperShift operator metrics ServiceMonitor has no GMP PodMonitoring counterpart yet.

rules:
  - namespace: hypershift
    group: hypershift.rules
    scope: namespace
    tracking: GCP-1293
    reason: HyperShift operator recording rules have no GMP Rules counterpart yet.
```

`tracking` and `reason` are both required. Duplicate waivers, waivers for targets
that are now covered, and waivers whose `component` does not match the uncovered
target all fail the test.

## Limits

This is an offline check of rendered manifests. The generator preserves static
`metricRelabelings` and rejects unsupported actions, but MetricSet-dependent
relabeling from `support/metrics/sets.go` still needs a runtime adapter and
separate tests. This check does not prove that CPO reconciles the staged GMP
assets, that recording-rule expressions are equivalent,
that the recorded time series preserve dashboard label contracts, or that the GMP
collector can scrape the endpoints, that the `monitoring.googleapis.com` CRDs are
installed, or that the credentials and RBAC behind them work. Live validation of
the GCP deployment is tracked in GCP-1295.

## Running

```bash
go test ./test/parity/gmpmonitoring/ -count=1
```

The test needs no cluster and no CRDs; it runs as part of `go test ./...` in CI.
