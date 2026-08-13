# Etcd Shard Backup Integration Plan

## Problem

The `HCPEtcdBackup` controller (CNTRLPLANE-2677/2678, merged April 2026) is hardcoded
to snapshot only the default etcd instance. When etcd sharding is enabled
(OCPSTRAT-3150, merged July 2026), resources routed to non-default shards are
**not backed up**, and there is no mechanism to restore shard snapshots. Both
features are behind `TechPreviewNoUpgrade` feature gates, but backup and restore
support must be closed before `EtcdSharding` can promote beyond TechPreview.

### What's hardcoded today

| Component | Hardcoded to default etcd | File |
|-----------|--------------------------|------|
| Health check | `StatefulSet "etcd"` only | `reconciler.go:checkEtcdHealth` |
| Snapshot init container | `etcd-client.<ns>.svc:2379` only | `reconciler.go:createBackupJob` |
| RBAC (fetch-certs) | `etcd-client-tls` secret only | `reconciler.go:ensureRBAC` |
| Status | Single `snapshotURL` | `etcdbackup_types.go:HCPEtcdBackupStatus` |
| HostedCluster status | Single `LastSuccessfulEtcdBackupURL` | `hostedcluster_types.go` |
| Restore | Single `RestoreSnapshotURL` on `ManagedEtcdStorageSpec` | `hostedcluster_types.go` |
| Restore init container | Only injected on default `etcd` StatefulSet | `statefulset.go:adaptStatefulSet` |
| Restore condition | Only checks default `etcd` StatefulSet pods | `hostedcontrolplane_controller.go:etcdRestoredCondition` |

### What shards share vs. what differs

| Resource | Shared across shards? | Notes |
|----------|----------------------|-------|
| CA (`etcd-ca` ConfigMap) | ✅ Yes | Single CA signs all certs |
| Client TLS (`etcd-client-tls`) | ✅ Yes | Shards reuse default client cert |
| Peer/Server TLS | ❌ No | Per-shard: `etcd-<name>-peer-tls`, `etcd-<name>-server-tls` |
| Client service | ❌ No | Default: `etcd-client`, shard: `etcd-client-<name>` |
| StatefulSet | ❌ No | Default: `etcd`, shard: `etcd-<name>` |

**Key insight**: Since shards reuse `etcd-client-tls`, the existing `fetch-certs`
init container and RBAC already provide the credentials needed to connect to any
shard. No RBAC changes are needed.

## Design

### Approach: Sequential snapshot per shard in a single Job

Rather than spawning parallel Jobs (which adds complexity around serial execution
guards, partial failure semantics, and credential/NetworkPolicy lifecycle), the
backup Job will snapshot each etcd endpoint **sequentially** within a single Pod.
This preserves the existing single-Job-per-backup model.

### Changes by layer

#### 1. API (`api/hypershift/v1beta1/etcdbackup_types.go`)

Add per-shard snapshot tracking to `HCPEtcdBackupStatus`:

```go
// shardSnapshots contains the snapshot URLs for each etcd shard that was
// backed up. When etcd sharding is not enabled, this list contains a single
// entry for the default shard. Each entry maps a shard name to its snapshot URL.
// +optional
// +listType=map
// +listMapKey=name
// +kubebuilder:validation:MaxItems=11
ShardSnapshots []HCPEtcdShardSnapshot `json:"shardSnapshots,omitempty"`
```

```go
type HCPEtcdShardSnapshot struct {
    // name is the shard name (e.g., "etcd" for the default, "etcd-events" for a named shard).
    // +required
    Name string `json:"name"`

    // snapshotURL is the URL of the snapshot in cloud storage.
    // +required
    SnapshotURL string `json:"snapshotURL"`
}
```

The existing `SnapshotURL` field remains for backward compatibility and is set to
the **default shard's** snapshot URL. New consumers should prefer `ShardSnapshots`.

#### 2. Controller (`hypershift-operator/controllers/etcdbackup/reconciler.go`)

##### a. Shard discovery

Add a helper that resolves the list of etcd shards to back up:

```go
func (r *HCPEtcdBackupReconciler) etcdShards(hcp *hyperv1.HostedControlPlane) []etcdShardInfo {
    shards := []etcdShardInfo{{
        name:     "etcd",
        endpoint: fmt.Sprintf("https://etcd-client.%s.svc:%d", hcp.Namespace, supportconfig.EtcdClientPort),
    }}
    if hcp.Spec.Etcd.Managed != nil {
        for _, s := range hcp.Spec.Etcd.Managed.Shards {
            shardName := fmt.Sprintf("etcd-%s", s.Name)
            shards = append(shards, etcdShardInfo{
                name:     shardName,
                endpoint: fmt.Sprintf("https://%s.%s.svc:%d",
                    etcdutil.ClientServiceName(shardName), hcp.Namespace, supportconfig.EtcdClientPort),
            })
        }
    }
    return shards
}
```

##### b. Health check (`checkEtcdHealth`)

Change from checking a single StatefulSet to checking **all** shard StatefulSets:

```go
func (r *HCPEtcdBackupReconciler) checkEtcdHealth(ctx context.Context, hcp *hyperv1.HostedControlPlane) (bool, string, error) {
    for _, shard := range r.etcdShards(hcp) {
        sts := &appsv1.StatefulSet{}
        if err := r.Get(ctx, types.NamespacedName{Name: shard.name, Namespace: hcp.Namespace}, sts); err != nil {
            if apierrors.IsNotFound(err) {
                return false, fmt.Sprintf("etcd StatefulSet %q not found", shard.name), nil
            }
            return false, "", err
        }
        desired := ptr.Deref(sts.Spec.Replicas, 1)
        if sts.Status.ReadyReplicas < desired {
            return false, fmt.Sprintf("etcd StatefulSet %q not fully ready: %d/%d",
                shard.name, sts.Status.ReadyReplicas, desired), nil
        }
    }
    return true, "", nil
}
```

##### c. Job construction (`createBackupJob`)

Replace the single `snapshot` init container with one init container **per shard**,
each writing to a shard-named snapshot file:

```
Init containers:
  fetch-certs          (unchanged — shared client TLS works for all shards)
  snapshot-etcd        etcdctl --endpoints=etcd-client.<ns>:2379 snapshot save /backup/etcd.db
  snapshot-etcd-events etcdctl --endpoints=etcd-client-events.<ns>:2379 snapshot save /backup/etcd-events.db

Main container:
  upload               control-plane-operator etcd-upload --snapshot-dir /backup/ ...
```

The upload command changes from `--snapshot-path` (single file) to `--snapshot-dir`
(directory of shard-named files), uploading each as a separate object with the
shard name in the key.

##### d. NetworkPolicy (`ensureNetworkPolicy`)

The current policy selects pods with `app: etcd`. However, shard pods carry
`app: etcd-<name>` (e.g., `app: etcd-events`) because the StatefulSet template
uses `app: {{ .Name }}` where `.Name` is `etcd-events`, not `etcd`. The
NetworkPolicy must be widened.

Option A (recommended): Use a `matchExpressions` selector with a prefix match
via multiple label values. Since shards are known at Job creation time, the
controller can build the list:

```go
np.Spec.PodSelector = metav1.LabelSelector{
    MatchExpressions: []metav1.LabelSelectorRequirement{{
        Key:      "app",
        Operator: metav1.LabelSelectorOpIn,
        Values:   shardAppLabels, // ["etcd", "etcd-events", ...]
    }},
}
```

Option B: Add a shared label (e.g., `hypershift.openshift.io/etcd-component: "true"`)
to all etcd shard StatefulSets in the CPOv2 shard component and select on that.
This requires a change in the shard component code but is more future-proof.

##### e. Status update (`handleJobStatus`)

After successful upload, parse per-shard snapshot URLs from the upload container's
termination message (JSON array instead of single string) and populate both
`SnapshotURL` (default shard, backward compat) and `ShardSnapshots`.

#### 3. Upload command (`control-plane-operator/etcd-upload`)

Extend the `etcd-upload` subcommand:

- Add `--snapshot-dir` flag (mutually exclusive with existing `--snapshot-path`)
- When `--snapshot-dir` is set, iterate over `*.db` files in the directory
- Upload each with key `<prefix>/<timestamp>/<shard-name>.db`
- Write JSON to termination log: `[{"name":"etcd","url":"s3://..."},{"name":"etcd-events","url":"s3://..."}]`

The existing `--snapshot-path` continues to work for the single-shard case so
older CPO images remain compatible.

#### 4. Documentation and API warnings

- Remove the `WARNING` from `ManagedEtcdShardSpec.Shards` godoc
- Remove the `TODO(etcd-sharding)` from `shard.go`
- Update `docs/content/how-to/disaster-recovery/etcd-snapshot-backup/` to cover
  multi-shard backup behavior

### Restore: per-shard snapshot restore

Restore (via `restoreSnapshotURL`) is currently single-shard only. The default
etcd StatefulSet's `adaptStatefulSet` injects an `etcd-init` container that
curls the snapshot URL and runs `etcdutl snapshot restore`, gated by the
`EtcdSnapshotRestored` condition. The shard component (`adaptStatefulSetForShard`)
has **no restore logic at all**.

Backup without restore is incomplete DR — the backed-up shard snapshots would be
useless without a restore path. Both must ship together.

#### Restore approach

The existing restore mechanism is a one-shot, creation-time bootstrap: the user
sets an immutable URL on the HostedCluster, the etcd init container downloads and
restores it on first boot (when `/var/lib/data` is empty), and the
`EtcdSnapshotRestored` condition gates removal of the init container.

Multi-shard restore follows the same pattern with minimal new machinery:

##### a. API: add `restoreSnapshotURL` to `ManagedEtcdShardSpec`

```go
type ManagedEtcdShardSpec struct {
    // ... existing fields ...

    // restoreSnapshotURL allows an optional URL to be provided where an etcd
    // snapshot for this shard can be downloaded. This snapshot will be restored
    // on initial startup, only when the shard's PV is empty.
    // Only meaningful for PersistentVolume-backed shards; EmptyDir shards are
    // ephemeral and restoring into them would be lost on pod restart.
    // +optional
    // +immutable
    // +kubebuilder:validation:MaxLength=1024
    // +kubebuilder:validation:XValidation:rule="self == oldSelf",message="restoreSnapshotURL is immutable"
    // +kubebuilder:validation:XValidation:rule="self == '' || self.matches('^(https|s3)://.*')",message="restoreSnapshotURL must be a valid URL with scheme https or s3"
    RestoreSnapshotURL string `json:"restoreSnapshotURL,omitempty"`
}
```

Using a single `string` rather than `[]string` (like the parent spec) avoids the
legacy MaxItems=1 list pattern. The field is immutable via CEL and only valid for
PersistentVolume-backed shards — an additional CEL rule on `ManagedEtcdShardSpec`
rejects `restoreSnapshotURL` when `storage.type` is `EmptyDir`:

```go
// +kubebuilder:validation:XValidation:rule="self.storage.type != 'EmptyDir' || !has(self.restoreSnapshotURL) || self.restoreSnapshotURL == ''",message="restoreSnapshotURL is not supported for EmptyDir shards"
```

##### b. Shard component: inject `etcd-init` container

`adaptStatefulSetForShard` gains the same pattern as `adaptStatefulSet`:

```go
if shard.RestoreSnapshotURL != "" && !snapshotRestored {
    sts.Spec.Template.Spec.InitContainers = append(sts.Spec.Template.Spec.InitContainers,
        buildEtcdInitContainer(shard.RestoreSnapshotURL),
    )
}
```

The existing `buildEtcdInitContainer` and `etcd-init.sh` script work unchanged —
the script is shard-agnostic (it just curls a URL and restores into
`/var/lib/data`, which is the volume mount point for every shard's PVC).

##### c. Restore condition: extend `etcdRestoredCondition`

The HCP controller's `reconcileEtcdStatus` currently checks only the default
etcd StatefulSet for restore completion. It must be extended to check **all**
shard StatefulSets that have a `restoreSnapshotURL` configured:

```go
// For the default etcd, check RestoreSnapshotURL from managed storage spec.
// For each shard, check RestoreSnapshotURL from the shard spec.
// EtcdSnapshotRestored is set to True only when ALL shards with restore URLs
// have completed their etcd-init containers successfully.
```

The `EtcdSnapshotRestored` condition remains a single boolean on the HCP — it's
not set to True until **all** shards (default + named) with restore URLs report
their init containers ready. This preserves the existing gate: the init container
is removed from all StatefulSets only once the aggregate condition is True.

##### d. EmptyDir shards: no restore

EmptyDir-backed shards (e.g., events, leases) are ephemeral by design — data is
lost on pod restart. Restoring a snapshot into tmpfs is pointless and wasteful.
The API validation rejects `restoreSnapshotURL` on EmptyDir shards, and the
backup controller skips EmptyDir shards when building snapshot init containers
(their data is expendable).

##### e. Backup: skip EmptyDir shards

For consistency, the backup Job should also skip EmptyDir-backed shards. These
hold ephemeral data (events, leases) that regenerates naturally and doesn't need
to survive a disaster recovery scenario. The `etcdShards()` discovery helper
should filter based on storage type:

```go
func (r *HCPEtcdBackupReconciler) etcdShards(hcp *hyperv1.HostedControlPlane) []etcdShardInfo {
    shards := []etcdShardInfo{{...}} // default shard always included
    if hcp.Spec.Etcd.Managed != nil {
        for _, s := range hcp.Spec.Etcd.Managed.Shards {
            if s.Storage.Type == hyperv1.EmptyDirEtcdShardStorage {
                continue // skip ephemeral shards
            }
            // ...
        }
    }
    return shards
}
```

## Test plan

1. **Unit tests** (`reconciler_test.go`):
   - Health check fails if any shard StatefulSet is unhealthy
   - Job contains correct number of snapshot init containers for N shards
   - EmptyDir shards are excluded from snapshot init containers
   - Status correctly populates `ShardSnapshots` from JSON termination message
   - Backward compat: single-shard HCP still produces single `SnapshotURL`

2. **Unit tests** (`shard_test.go`):
   - `adaptStatefulSetForShard` injects `etcd-init` when `RestoreSnapshotURL` is set and `EtcdSnapshotRestored` is not True
   - `adaptStatefulSetForShard` omits `etcd-init` when `EtcdSnapshotRestored` is True
   - `adaptStatefulSetForShard` omits `etcd-init` when `RestoreSnapshotURL` is empty

3. **Unit tests** (`hostedcontrolplane_controller_test.go`):
   - `etcdRestoredCondition` requires all shards with restore URLs to complete
   - `etcdRestoredCondition` ignores shards without restore URLs
   - `EtcdSnapshotRestored` not set True until all shard init containers succeed

4. **Envtest** (CEL validation):
   - `HCPEtcdBackupStatus.ShardSnapshots` validates correctly
   - `SnapshotURL` and `ShardSnapshots` can coexist
   - `ManagedEtcdShardSpec.RestoreSnapshotURL` is immutable
   - `RestoreSnapshotURL` rejected on EmptyDir shards
   - `RestoreSnapshotURL` accepted on PersistentVolume shards

5. **E2E** (`test/e2e/`):
   - Existing backup e2e test continues to pass (no sharding configured)
   - When sharding e2e is added: verify backup includes shard snapshots
   - When sharding e2e is added: verify restore with per-shard URLs

## Task breakdown

| # | Task | Scope |
|---|------|-------|
| 1 | Add `ShardSnapshots` to `HCPEtcdBackupStatus` API, regenerate | API |
| 2 | Add `RestoreSnapshotURL` to `ManagedEtcdShardSpec` + CEL rules, regenerate | API |
| 3 | Add `etcdShards()` discovery helper (skip EmptyDir shards) | Controller |
| 4 | Update `checkEtcdHealth` for multi-shard | Controller |
| 5 | Update `createBackupJob` to emit per-shard snapshot init containers | Controller |
| 6 | Extend `etcd-upload` to support `--snapshot-dir` | CPO CLI |
| 7 | Update `handleJobStatus` to parse JSON termination message | Controller |
| 8 | Verify NetworkPolicy pod selector covers shard pods | Controller |
| 9 | Inject `etcd-init` in `adaptStatefulSetForShard` for PV-backed shards | CPO |
| 10 | Extend `reconcileEtcdStatus` / `etcdRestoredCondition` for shard StatefulSets | CPO |
| 11 | Unit tests for all above | Tests |
| 12 | Envtest CEL tests for new API fields | Tests |
| 13 | Remove WARNING/TODO from sharding code | Cleanup |
| 14 | Update docs | Docs |
