# Etcd Sharding by Resource Kind (Tech Preview)

Etcd sharding splits a hosted cluster's managed etcd into several independent
etcd clusters, each owning a set of resource kinds. High-churn kinds — events
and leases above all — stop competing with the rest of the cluster's state for
etcd write bandwidth, compaction, and defragmentation.

!!! important

    `EtcdSharding` is a **management cluster** feature gate in the
    `TechPreviewNoUpgrade` set. Install the HyperShift Operator with
    `hypershift install --tech-preview-no-upgrade`; without it,
    `spec.etcd.managed.shards` is not part of the installed `HostedCluster`
    CRD. See [Feature Gates](feature-gates.md) for the distinction between
    management cluster and hosted cluster gates.

## How it works

Each shard becomes its own StatefulSet, client and discovery Services, serving
and peer certificates, and — at three replicas — a PodDisruptionBudget. The
kube-apiserver is then given one `--etcd-servers-overrides` entry per sharded
resource, pointing at the owning shard's client Service.

Because routing goes through `--etcd-servers-overrides`, only resource types
compiled into the kube-apiserver binary are routed. CRD-backed and
aggregated-API resources always stay in the default etcd.

Sharding is a **create-time only** decision. There is no mechanism to migrate
existing keys between etcd clusters, so the API rejects adding, removing,
renaming, or re-pointing shards after creation.

## Configuring shards

Set `spec.etcd.managed.shards` on the `HostedCluster`:

```yaml
spec:
  etcd:
    managementType: Managed
    managed:
      shards:
      - name: events
        replicas: 3
        storage:
          type: EmptyDir
        resources:
        - apiGroup: ""
          resource: events
        - apiGroup: events.k8s.io
          resource: events
      - name: leases
        replicas: 3
        storage:
          type: PersistentVolume
        resources:
        - apiGroup: coordination.k8s.io
          resource: leases
```

Events are a natural fit for `EmptyDir` storage: they are already TTL'd and
losing them on restart is acceptable, so the shard trades durability for
throughput.

Resources must not overlap across shards, and `replicas` must be 1 or 3.

### From the CLI

`hypershift create cluster` exposes the same thing through a repeatable
`--etcd-shard` flag:

```shell
hypershift create cluster aws \
  --control-plane-availability-policy=HighlyAvailable \
  --etcd-shard "name=events,resources=/events;events.k8s.io/events,replicas=3,storage=EmptyDir" \
  --etcd-shard "name=leases,resources=coordination.k8s.io/leases,replicas=3,storage=PersistentVolume" \
  ...
```

The value is a comma-separated list of `key=value` fields:

| Field | Required | Description |
|---|---|---|
| `name` | yes | Shard name. The StatefulSet is `etcd-<name>`. |
| `resources` | yes | Semicolon-separated `<apiGroup>/<resource>` list. Use a leading slash for the core group, e.g. `/events`. |
| `replicas` | no | `1` or `3`. Defaults to `3`. |
| `storage` | no | `PersistentVolume` or `EmptyDir`. |
| `storageClassName` | no | Only valid with `PersistentVolume`; implies it when set. |

Shards run three members by default, so pair them with
`--control-plane-availability-policy=HighlyAvailable`.

## E2E coverage

Coverage lives in the v2 e2e framework only, in
`test/e2e/v2/tests/etcd_sharding_test.go` under the `etcd-sharding` label. The
shard layout and the assertion helpers live in `test/e2e/util/etcd_sharding.go`.

The specs run against the `etcd-sharded` cluster variant defined in
`test/e2e/v2/lifecycle/{aws,azure}.go`, which is selected through `TEST_PLAN`.
They skip when the hosted cluster under test has no shards, so they are inert
on any other cluster variant.

They validate the three things the feature promises:

- **Configuration and deployment** — the `HostedCluster` round-trips the
  shards; each shard's StatefulSet reaches its replica count; storage matches
  the requested type; Services, TLS Secrets, PodDisruptionBudget and
  `ControlPlaneComponent` are all present and available.
- **Resource routing** — kube-apiserver's `--etcd-servers-overrides` matches
  the shard layout exactly, and `etcdctl` run inside the shard pods confirms
  that a marker Event lands in the events shard and not in the default etcd,
  that leases exist only in the leases shard, and that configmaps (an
  unsharded kind) exist only in the default etcd.
- **Cluster operations with sharding enabled** — the hosted cluster completes
  rollout and stays healthy; a shard member is deleted and the shard must
  converge and keep serving writes for the kind it owns.

Day-2 immutability is also asserted: adding, removing, renaming, and
re-pointing a shard are all rejected.

### Running locally

Against a cluster that already has shards:

```shell
make e2ev2
E2E_HOSTED_CLUSTER_NAME=<name> E2E_HOSTED_CLUSTER_NAMESPACE=<ns> \
  bin/test-e2e-v2 --ginkgo.label-filter="etcd-sharding" --ginkgo.v
```

## CI

The `etcd-sharded` variant is deliberately **not** in either platform's default
`TestMatrix()`, so the regular AWS and Azure jobs do not pay for an extra
cluster. It is selected by dedicated test plans that ship in the
`hypershift-tests` image:

- `test/e2e/v2/testplans/aws-etcd-sharding.yaml`
- `test/e2e/v2/testplans/azure-etcd-sharding.yaml`

These drive two periodics in openshift/release, both installing the operator
with `--tech-preview-no-upgrade`:

| Job | Cadence |
|---|---|
| `periodic-ci-openshift-hypershift-main-e2e-aws-etcd-sharding` | daily |
| `periodic-ci-openshift-hypershift-main-e2e-azure-etcd-sharding` | weekly |

Each plan runs the `etcd-sharding` specs and then a
`hosted-cluster-health || control-plane-workloads` pass, so the health check
also covers recovery from the shard member restart. See
[CI Pipeline Configuration](ci/v2-testing/ci-pipeline.md) for how test plans
and workflows fit together.
