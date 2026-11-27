# GMP monitoring parity guard

This package keeps the Google Managed Prometheus (GMP) monitoring backend from
drifting behind the default `monitoring.coreos.com` backend.

HyperShift components expose metrics through CoreOS `ServiceMonitor` and
`PodMonitor` resources. The GMP path needs equivalent scrape targets. Rules are
out of scope; see [Rules](#rules). This first PR generates a complete set of
`gmp-podmonitoring.yaml` files — every CoreOS scrape target that can run on a
GMP platform, including the management-level monitors `hypershift install`
constructs in Go. Component files
sit beside the component's CoreOS assets in `v2/assets/<component>`; the
management-level files sit in `cmd/install/assets/gmpmonitoring`. The CPO skips
every `gmp-` manifest by default, so nothing applies them yet. A later gated
runtime PR will opt in. The flag is not a runtime switch in this PR.

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
   at reconcile time. The management-level monitors built by
   `hypershift install` are added from their Go constructors; their generated GMP
   equivalents live under `cmd/install/assets/gmpmonitoring/operator` and
   `cmd/install/assets/gmpmonitoring/external-dns`, because they are not
   component assets. The CoreOS discovery skips the `gmp-` files that share a
   component directory, so a generated file is never mistaken for a CoreOS
   manifest.
2. **Resolves** each CoreOS `ServiceMonitor`/`PodMonitor` endpoint to a
   `scrapeTarget`: namespace, effective pod selector, container port, path and
   scheme. Named Service ports take precedence over `targetPort`; an empty path
   defaults to `/metrics` and an empty scheme to `http`. Discovery failures
   (no matching Service, no backing pod template, no such port) are errors, not
   skips, because silently dropping a target would make the guard pass while the
   target is unscraped.
3. **Loads generated GMP assets** from `v2/assets/<component>/gmp-podmonitoring.yaml`
   and `cmd/install/assets/gmpmonitoring`, rendering component templates where
   needed. The CPO skips these files, so this PR does not reconcile them.
4. **Compares** coverage on `namespace|selector|port|path|scheme|tlsServerName`.
   The component name is deliberately excluded: one GMP `PodMonitoring` may
   consolidate endpoints that CoreOS splits across several monitors. A narrower
   or wider selector (including `matchExpressions` on a pod selector), a
   different port, path, scheme or TLS server name all count as uncovered, so selector and port drift is detected, not just fully missing
   monitors. The server name is part of the key because a name copied from one
   cluster's assets makes the client verify the wrong certificate everywhere
   else. Service DNS names are compared with their namespace label normalized to
   the `HCP_NAMESPACE` placeholder: CoreOS assets may carry a concrete namespace
   because their adapters derive the live one at reconcile time, while GMP
   assets must stay cluster-independent. Comparing raw values would flag that
   inert difference as uncovered coverage, or hide a real service-name mismatch
   behind it.
5. **Applies waivers.** Targets that are intentionally not ported are listed in
   [`waivers.yaml`](waivers.yaml) with a `tracking` issue and a `reason`.

## Adding coverage

When you add a GMP `PodMonitoring` for a component that already has a CoreOS monitor, the target becomes covered.
Delete the corresponding waiver in the same change. The test fails on a stale
waiver, so it cannot outlive the gap it documents.

When you add a component or monitor on the CoreOS path, the test fails until
you either port it or add a waiver. That is the point: the decision
is recorded in review instead of discovered when a dashboard goes blank.

## Waivers

Every CoreOS scrape target currently has a generated GMP equivalent, so
`scrapeTargets` is empty:

```yaml
scrapeTargets: []
```

A scrape-target waiver names `component`, `namespace`, `selector`, `port`,
`path`, `scheme` and `tlsServerName`; the selector is sorted `key=value` pairs,
comma-joined, and a pod selector's `matchExpressions` follow after `;` as
`key Op (v1,v2)`. `tracking` and `reason` are both required. Duplicate waivers,
waivers for targets that are now covered, and waivers whose `component` does not
match the uncovered target all fail the test. A waiver key includes
`tlsServerName`, so a waiver for a TLS endpoint has to reproduce the name it
waives.

## Rules

The test does not compare `PrometheusRule` groups with GMP `Rules`. The rules
consumed on GCP are defined and evaluated in `gcp-hcp-infra` (the `hcp.apiserver`
recording rules on `prometheus-hcp`), and the rules HyperShift ships are evaluated
there but not exported, because their names are not on the `exportMatch`
allowlist. A GMP copy of the HyperShift rules would compute series nothing reads.
This holds only while no HyperShift rule output is added to that allowlist, which
nothing in this repository can detect.

## Limits

This is an offline check of rendered manifests. CoreOS endpoints with no
interval inherit the GCP management Prometheus `60s` scrape interval, which
generated GMP endpoints state explicitly. Review the GCP Prometheus setting
during migration.

Components that can never run on a GMP platform get no `PodMonitoring`. They are
listed with a reason in `metrics.GMPUnsupportedComponents`
(`support/metrics/gmp_monitoring.go`), today `karpenter` and `karpenter-operator`
because Karpenter runs only on AWS and Azure. The generator stages nothing for
them and this test skips them, so they need no waiver. A component that can run
on GCP but that the generator cannot represent yet does not belong there: waive
its target in `waivers.yaml` with a tracking issue.

CoreOS `jobLabel` has no GMP equivalent: GMP always derives the `job` label from
the `PodMonitoring` name. The generator rejects a `jobLabel` that selects a real
Service or pod label, because copying the endpoint would silently change the
`job` value, and accepts one that no target carries (the staged management
monitors set `jobLabel: component`, which selects nothing).

prometheus-operator sets a ServiceMonitor's `job` label to the Service name, so
the generator names each `PodMonitoring` after the Service it resolved, not
after the monitor. etcd's monitor is named after the shard (`etcd`) and its
Service after the client endpoint (`etcd-client`); the staged `PodMonitoring` is
`{{ .ClientServiceName }}`, so both backends report `job="etcd-client"`. A
pod-targeting monitor's `job` defaults to `<namespace>/<name>`, which a
`PodMonitoring` name cannot hold; the generator keeps the monitor name, and
dashboards or rules that key on those `job` values need adjusting.

The other target labels are made to match too:

- `targetLabels.metadata` is `[container, pod]`. GMP's default also adds
  `top_level_controller_name` and `top_level_controller_type`, which CoreOS
  series do not carry. GMP adds `namespace` itself.
- prometheus-operator adds `service` (ServiceMonitor only) and `endpoint` (the
  Service or pod port name, or the `targetPort` as written). GMP has no such
  labels, so each endpoint starts with constant `replace` rules that add them.
  These are metric relabeling rules, so they are not applied to the synthetic
  `up` and `scrape_*` series, and they overwrite a same-named label exposed by
  the target where prometheus-operator would rename it to `exported_<name>`.
- `instance` cannot match: GMP sets `<pod>:<port name>` and forbids relabeling
  it; prometheus-operator uses `<pod IP>:<port>`.

`TestGeneratedTargetLabelsMatchCoreOS` derives the CoreOS label set for every
endpoint independently of the generator and compares it with what the staged
`PodMonitoring` produces, excluding `instance` and pinning the PodMonitor `job`
difference.

The generator preserves static `metricRelabelings` and rejects unsupported
actions. A staged file can only encode relabeling from its source asset. The
runtime GMP adapter for a component owns the same two things the CoreOS adapter
owns: it must replace the filter rules in `metricRelabeling` with the output of
the component's `support/metrics` function for the cluster's `MetricsSet`, and
it must append the `cluster_id` label. It must keep the generated `service` and
`endpoint` rules. A component whose CoreOS adapter only adds relabelings at
reconcile time (kube scheduler, OLM, catalog operator and the route controller
manager, for example) therefore stages only those target-label rules, and its
GMP adapter must supply the filter rules.

The runtime adapters do not exist yet, so nothing here pins the staged filter
rules to the `MetricsSetTelemetry` output or checks that an adapter keeps the
target labels. The gated runtime PR adds
`TestGeneratedMetricRelabelingMatchesTelemetrySet` and
`TestAdaptedPodMonitoringKeepsTargetLabels` with the adapters.

TLS coverage stops at the server name and the names of the referenced Secrets.
GMP only accepts Secret references, so each CA ConfigMap a CoreOS monitor trusts
(`root-ca`, `etcd-ca`) is translated to a CA-only mirror Secret (`root-ca-cert`,
`etcd-ca-cert`) that carries just the ConfigMap's `ca.crt`. The monitors never
reference the signer Secrets (`root-ca`, `etcd-signer`): those hold the CA
private key, and RBAC grants a whole Secret, so referencing them would give the
GMP collector the signing keys. The control-plane-operator reconciles the
mirrors when GMP monitoring is enabled: `pki.ReconcileCASecretMirror` recomputes
them from the ConfigMap on every reconcile, so they follow CA rotation. The GMP
runtime reconciliation must still grant the collector read access to them by
`resourceNames`.

A server name that addresses a Service in the control-plane namespace is staged
with the `HCP_NAMESPACE` placeholder in place of the namespace label (for
example `node-tuning-operator.HCP_NAMESPACE.svc`). The component framework only
replaces `metadata.namespace`, so the runtime reconciliation must substitute the
live namespace into `tls.serverName`, as the CoreOS NTO adapter does, before the
asset is applied.

The management-level monitors are staged with the `MANAGEMENT_NAMESPACE`
placeholder instead, because the namespace `hypershift install` deploys into is
configurable. Whatever applies those assets substitutes the live install
namespace; the parity check normalizes the placeholder to the `hypershift`
default so both sides compare in the same namespace.

`TestGeneratedTLSSecretsHaveProducers` in `hack/gmp-config-gen` fails when a
staged CA reference is not a mirror declared in `caConfigMapSecrets`, when a
client cert or key reference is not a Secret the control-plane-operator
reconciles, or when any reference names a signer Secret. The generator's unit
tests pin the CA ConfigMap-to-Secret mapping to the control-plane-operator's
Secret constructors. Whether the collector can actually present those
credentials is part of the live validation below.

This check also does not prove that CPO reconciles the staged GMP assets, that
that the recorded time series preserve dashboard label contracts, that the GMP collector can scrape the endpoints, that
the `monitoring.googleapis.com` CRDs are installed, or that the credentials and
RBAC behind them work. Live validation of the GCP deployment is tracked in
GCP-1295.

## Running

```bash
go test ./test/parity/gmpmonitoring/ -count=1
```

The test needs no cluster and no CRDs. It runs with the rest of the repository's
unit tests under `make test` (`go test ./...`), which is what `ci/prow/unit`
runs. It is deliberately not in a `.github/workflows/test-reusable.yaml` shard.
`make verify-gmp-config` (run by `make lint`) fails when the generated files
drift from their sources.
