# Etcd Sharding by Resource Kind (Tech Preview)

Etcd sharding splits a hosted cluster's managed etcd into several independent
etcd clusters, each owning a set of resource kinds.

For example, you can use sharding to put high-churn kinds (events, leases) into
a dedicated etcd so they don't compete with the rest of the cluster's state for
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

## Verifying

Each shard gets its own StatefulSet named `etcd-<shard>` in the control plane
namespace, alongside the default `etcd`:

```shell
oc get statefulset -n clusters-<cluster>
```

To confirm the kube-apiserver is routing to them, read the generated config.
It carries one `etcd-servers-overrides` entry per sharded resource, in
`<apiGroup>/<resource>#<endpoint>` form — the API group is empty for core
resources, so core events appear as `/events#...`:

```shell
oc get configmap kas-config -n clusters-<cluster> \
  -o jsonpath='{.data.config\.json}' \
  | jq '.apiServerArguments["etcd-servers-overrides"]'
```
