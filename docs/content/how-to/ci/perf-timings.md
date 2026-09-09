# Performance Timings from CI Artifacts

Every HyperShift e2e job already dumps the full resource tree with `hypershift dump cluster`
(an `oc adm inspect` layout). Those dumps carry `metadata.creationTimestamp` and condition
`lastTransitionTime` for every HostedCluster, NodePool and CAPI Machine, which means bring-up
and node-join timings are recoverable *after the fact* from artifacts CI has been collecting all
along — no test instrumentation required.

`hack/perf/hcp-timings.py` mines those artifacts. It works on both e2e suites, because both go
through the same dump command:

| Suite | Dump location |
|-------|---------------|
| v1 | `$ARTIFACT_DIR/<TestName>/namespaces/...` |
| v2 | `$ARTIFACT_DIR/<clusterName>/namespaces/...` |

## What Gets Mined

| Resource | Metric | Duration measured |
|----------|--------|-------------------|
| `hostedclusters.hypershift.openshift.io` | `control_plane_bring_up` | creation → `Available` |
| `hostedclusters.hypershift.openshift.io` | `cluster_version_rollout` | `status.version.history[].startedTime` → `completionTime` |
| `nodepools.hypershift.openshift.io` | `nodepool_node_join` | creation → `Ready` |
| `machines.cluster.x-k8s.io` | `machine_provision` | creation → `Ready`, one record per Machine |

Each record also carries per-phase offsets from creation:

- **Bring-up**: `InfrastructureReady`, `EtcdAvailable`, `KubeAPIServerAvailable`, `Available`
- **Node join**: `ValidMachineTemplate`, `ReachedIgnitionEndpoint`, `AllMachinesReady`, `AllNodesHealthy`, `Ready`
- **Machine**: `BootstrapReady`, `InfrastructureReady`, `NodeHealthy`, `Ready`

The Machine split is the useful one for triage: `InfrastructureReady` is when the cloud provider
finished creating the instance, `NodeHealthy` is when the node joined and went healthy. A
regression in the first is a slow cloud; a regression in the gap between them is slow bootstrap.

## Reading One Job

```shell
# Pull a Prow job's artifacts (requires `gcloud auth login`)
gcloud storage cp -r \
  gs://test-platform-results/pr-logs/pull/openshift_hypershift/<pr>/<job>/<build>/artifacts \
  /tmp/artifacts

./hack/perf/hcp-timings.py extract /tmp/artifacts
```

```
======================================================================
HostedCluster: e2e-clusters-f92vm/create-cluster-km2vj
======================================================================
Created: 2026-09-11T14:50:33Z
Platform: AWS

Control Plane Bring-Up Phases:
  InfrastructureReady           : +  1.0m (60s)
  EtcdAvailable                 : +  1.3m (77s)
  KubeAPIServerAvailable        : +  1.7m (100s)
  Available                     : + 31.9m (1911s)
  Total                         :   31.9m (1911s)

Cluster Version Rollout:
  4.21.10                       :   28.4m (1704s)

NodePool: create-cluster-km2vj (2 replicas, m5.large)
  ValidMachineTemplate          : +  1.1m (66s)
  AllMachinesReady              : +  4.5m (270s)
  ReachedIgnitionEndpoint       : +  5.6m (338s)
  AllNodesHealthy               : + 10.8m (648s)
  Ready                         : + 11.0m (660s)

Machines (2):
  BootstrapReady                : p50   0.0m  min   0.0m  max   0.1m
  InfrastructureReady           : p50   3.3m  min   3.2m  max   3.3m
  NodeHealthy                   : p50   9.4m  min   9.1m  max   9.6m
  Ready                         : p50   9.4m  min   9.1m  max   9.6m
```

Phases print in the order they were actually reached, not in canonical order, so the timeline
reads top to bottom. Milestones that were never reached are listed as `not reached`.

## Building Baselines

`extract --json` writes one JSON record per line, which accumulates across runs:

```shell
for build in /tmp/runs/*; do
  ./hack/perf/hcp-timings.py extract "$build" --format json --json records/all.json --append
done
```

`compare` aggregates those records into P50/P95/P99 distributions, grouped by platform:

```shell
./hack/perf/hcp-timings.py compare 'records/*.json'
```

```
platform: AWS
  control_plane_bring_up: n=42 p50=1899.0s p95=2410.5s p99=2688.1s (min=1601.0s max=2712.0s)
    EtcdAvailable: p50=78.0s p95=131.0s p99=160.4s
    ...
```

Freeze a baseline once the distribution looks representative:

```shell
./hack/perf/hcp-timings.py compare 'records/*.json' \
  --write-baseline hack/perf/baselines/aws.json
```

## Gating on Regressions

```shell
./hack/perf/hcp-timings.py compare 'records/new-*.json' \
  --baseline hack/perf/baselines/aws.json \
  --threshold 20 \
  --min-samples 5 \
  --report $ARTIFACT_DIR/perf-report.json
```

```
OK         AWS control_plane_bring_up: p50 current=1904.0s baseline=1899.0s delta=+0.3% (threshold +20.0%)
REGRESSION AWS machine_provision/InfrastructureReady: p50 current=380.0s baseline=200.0s delta=+90.0% (threshold +20.0%)
```

Exit codes: `0` no regression, `1` regression detected, `2` usage or input error — so the command
drops straight into a periodic job as a gate.

Useful flags:

| Flag | Purpose |
|------|---------|
| `--platform` | Restrict to one provider (`AWS`, `Azure`, `KubeVirt`, …). Also read from `HYPERSHIFT_PERF_PLATFORM`. |
| `--metric` | Restrict to a single metric. |
| `--threshold` | Percent regression tolerated. Default 20; also read from `HYPERSHIFT_PERF_THRESHOLD`. |
| `--compare-stat` | Statistic compared: `p50` (default), `p95`, `p99` or `mean`. |
| `--min-samples` | Ignore metrics with fewer samples, so a single unlucky run cannot fail the gate. |
| `--fail-on-missing-baseline` | Treat a metric with no baseline entry as a failure instead of reporting it. |

Baselines are per platform and, in practice, only comparable within the same machine size and
release stream — the node-join records carry `instanceType` and `releaseImage` metadata so a
baseline can be checked for apples-to-apples before it is trusted.

## Accuracy Caveats

These are real limits of mining dumps rather than instrumenting the tests, and they matter when
reading the numbers:

- **`lastTransitionTime` is the *last* transition, not the first.** If a condition flapped —
  `Available` going `True → False → True` in a test that breaks things on purpose — the reported
  duration is an upper bound. Per-Machine metrics are the more reliable signal here.
- **Dumps are a single snapshot taken at the end of a test.** Resources deleted before the dump,
  such as machines replaced during an upgrade, are simply absent.
- **Deletion timings cannot be recovered at all.** The objects are gone by the time the dump runs.

Anything needing first-transition accuracy, teardown timings, or in-flight observation requires
real instrumentation; mining dumps covers bring-up and node-join, which is where the current
baselines are needed.

## Tests

```shell
make verify-perf-timings
```

The suite runs against fixtures under `hack/perf/testdata/artifacts/` that reproduce a real dump
layout, and is wired into `make verify-parallel` and `make verify-ci`.
