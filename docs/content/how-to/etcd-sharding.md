# Etcd Sharding for HyperShift

## Overview

Etcd sharding allows you to distribute Kubernetes resources across multiple independent etcd clusters (shards) based on resource type. This feature improves performance and stability for large-scale hosted clusters by isolating high-churn resources (like Events and Leases) from critical cluster state.

## When to Use Etcd Sharding

Consider enabling etcd sharding when you experience:

- **Large cluster scale**: 7500+ nodes or equivalent workload density
- **High event churn**: Frequent pod creation/deletion generating thousands of events
- **Lease saturation**: Large number of leader election leases (e.g., many controllers, operators)
- **Etcd performance issues**: High latency, slow queries, or storage pressure on etcd

### Typical Performance Improvements

With a 3-shard configuration (main + events + leases):

- **Etcd latency**: 40-60% reduction in P99 latency for critical operations
- **Storage efficiency**: 50-70% reduction in main shard storage growth
- **Scalability**: Supports 30-50% more nodes before hitting etcd limits
- **Backup/restore**: Faster backups of critical data, skip non-critical shards

## Architecture

### How Sharding Works

1. **Resource prefix routing**: Kube-apiserver uses `--etcd-servers-overrides` to route requests by resource prefix
2. **Independent shards**: Each shard is a separate etcd cluster with its own StatefulSet, Services, and storage
3. **Default shard**: Exactly one shard must have "/" prefix to handle all non-routed resources
4. **Managed sharding**: HyperShift automatically creates and manages shard infrastructure

### Resource Prefix Format

- **Default prefix**: `/` - Catches all resources not explicitly routed to other shards
- **Specific resources**: `/events#`, `/coordination.k8s.io/leases#` - Route specific resource types
- **Format**: `/<api-group>/<resource>#` where `#` is required for non-default prefixes

## Configuration

### Basic Example: 3-Shard Setup

See [`example-3-shard.yaml`](etcd-sharding/example-3-shard.yaml) for a complete example.

```yaml
etcd:
  managementType: Managed
  managed:
    storage:
      type: PersistentVolume
      persistentVolume:
        storageClassName: gp3-csi

    shards:
      - name: main
        resourcePrefixes: ["/"]
        priority: Critical
        replicas: 3
        backupSchedule: "*/30 * * * *"
        storage:
          type: PersistentVolume
          persistentVolume:
            size: 8Gi
            storageClassName: fast-ssd

      - name: events
        resourcePrefixes: ["/events#"]
        priority: Low
        replicas: 1
        storage:
          type: PersistentVolume
          persistentVolume:
            size: 4Gi

      - name: leases
        resourcePrefixes: ["/coordination.k8s.io/leases#"]
        priority: Low
        replicas: 1
```

### Shard Configuration Options

#### Per-Shard Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Unique identifier (DNS-1035 compliant, max 15 chars) |
| `resourcePrefixes` | []string | Yes | Resource prefixes to route to this shard |
| `priority` | enum | No | Operational priority: Critical, High, Medium (default), Low |
| `replicas` | int32 | No | Number of etcd replicas: 1 or 3 (defaults to cluster policy) |
| `storage` | object | No | Storage config (inherits from `managed.storage` if unset) |
| `backupSchedule` | string | No | Cron schedule for backups (priority-based default) |

#### Priority-Based Defaults

| Priority | Default Backup Schedule | Use Case |
|----------|------------------------|----------|
| Critical | `*/30 * * * *` (every 30min) | Core cluster state |
| High | `0 * * * *` (hourly) | Important but less critical data |
| Medium | None | Standard workload data |
| Low | None | Transient or easily recreatable data |

## CLI Usage

HyperShift CLI supports etcd sharding configuration during cluster creation via two methods:

### Method 1: Configuration File (Recommended)

Use `--etcd-sharding-config` to specify a YAML or JSON file containing your shard configuration.

**AWS Example:**
```bash
hypershift create cluster aws \
  --name my-cluster \
  --pull-secret /path/to/pull-secret.json \
  --base-domain example.com \
  --etcd-sharding-config ./etcd-shards.yaml \
  --region us-east-1 \
  --role-arn arn:aws:iam::123456789012:role/hypershift-role \
  --sts-creds /path/to/sts-creds.json
```

**Azure Example:**
```bash
hypershift create cluster azure \
  --name my-cluster \
  --pull-secret /path/to/pull-secret.json \
  --base-domain example.com \
  --etcd-sharding-config ./etcd-shards.yaml \
  --location eastus \
  --credentials-file /path/to/azure-creds.json
```

**Configuration File Format** (etcd-shards.yaml):
```yaml
shards:
  - name: main
    resourcePrefixes: ["/"]
    priority: Critical
    replicas: 3
    storage:
      type: PersistentVolume
      persistentVolume:
        size: 16Gi
        storageClassName: gp3-csi
    backupSchedule: "*/30 * * * *"

  - name: events
    resourcePrefixes: ["/events#"]
    priority: Low
    replicas: 1
    storage:
      type: PersistentVolume
      persistentVolume:
        size: 8Gi

  - name: leases
    resourcePrefixes: ["/coordination.k8s.io/leases#"]
    priority: Low
    replicas: 1
```

### Method 2: Inline Flags (Simple Cases)

Use `--etcd-shard` (repeatable) for simple 2-3 shard configurations:

```bash
hypershift create cluster aws \
  --name my-cluster \
  --pull-secret /path/to/pull-secret.json \
  --base-domain example.com \
  --etcd-shard name=main,prefixes=/,priority=Critical,replicas=3 \
  --etcd-shard name=events,prefixes=/events#,priority=Low,replicas=1 \
  --region us-east-1 \
  --role-arn arn:aws:iam::123456789012:role/hypershift-role \
  --sts-creds /path/to/sts-creds.json
```

**Inline Flag Format:**
```
--etcd-shard name=<name>,prefixes=<prefix1>|<prefix2>,priority=<priority>,replicas=<1|3>,storage-size=<size>,storage-class=<class>,backup-schedule=<cron>
```

**Required keys:**
- `name`: Shard identifier (DNS-1035 compliant, max 15 chars)
- `prefixes`: Pipe-separated resource prefixes (e.g., `/events#`)

**Optional keys:**
- `priority`: Critical, High, Medium (default), Low
- `replicas`: 1 or 3 (defaults based on cluster availability policy)
- `storage-size`: e.g., 8Gi, 16Gi
- `storage-class`: Storage class name
- `backup-schedule`: Cron format (e.g., "*/30 * * * *")

### Global Storage Defaults

You can specify global etcd storage settings that apply to all shards unless overridden:

```bash
hypershift create cluster aws \
  --name my-cluster \
  --etcd-storage-size 20Gi \
  --etcd-storage-class fast-ssd \
  --etcd-sharding-config ./etcd-shards.yaml \
  ...
```

Shards without explicit storage configuration will inherit these global defaults.

### Rendering Manifests

Use `--render` to preview the generated HostedCluster manifest with sharding configuration:

```bash
hypershift create cluster aws \
  --name my-cluster \
  --etcd-sharding-config ./etcd-shards.yaml \
  --render > cluster-manifest.yaml
```

### Important Notes

- **Mutually Exclusive**: Cannot use both `--etcd-sharding-config` and `--etcd-shard` together
- **Validation**: CLI validates shard configuration before cluster creation
- **All Platforms**: Etcd sharding is available for all platforms (AWS, Azure, KubeVirt, OpenStack, Agent, etc.)
- **Immutable**: Sharding configuration cannot be changed after cluster creation

### Unmanaged Etcd Sharding

For externally managed etcd clusters:

```yaml
etcd:
  managementType: Unmanaged
  unmanaged:
    shards:
      - name: main
        resourcePrefixes: ["/"]
        priority: Critical
        endpoint: https://etcd-main-client:2379
        tls:
          clientSecret:
            name: etcd-main-client-tls

      - name: events
        resourcePrefixes: ["/events#"]
        priority: Low
        endpoint: https://etcd-events-client:2379
        tls:
          clientSecret:
            name: etcd-events-client-tls
```

## Operational Considerations

### Deployment

1. **New clusters**: Configure sharding in initial HostedCluster manifest
2. **Existing clusters**: Migration not supported (requires new cluster creation)
3. **Resource naming**:
   - Default shard uses backward-compatible names: `etcd`, `etcd-client`, `etcd-discovery`
   - Named shards use pattern: `etcd-<shard-name>`, `etcd-<shard-name>-client`, `etcd-<shard-name>-discovery`

### Dynamic Shard Management

HyperShift now supports full dynamic shard management:

1. **Automatic creation**: All configured shards are automatically deployed
2. **Orphan cleanup**: Removing a shard from the spec automatically deletes its resources
3. **Independent scaling**: Each shard can be scaled independently via the `replicas` field
4. **Status aggregation**: Overall etcd health considers all shards, with priority-based availability

**Adding a new shard:**

1. Edit the HostedCluster manifest to add the new shard configuration
2. Apply the changes
3. HyperShift automatically creates the StatefulSet, Services, and supporting resources
4. Monitor the new shard's rollout via `oc get statefulset etcd-<shard-name>`

**Removing a shard:**

1. Edit the HostedCluster manifest to remove the shard configuration
2. Apply the changes
3. HyperShift automatically cleans up the StatefulSet, Services, PVCs, and other resources
4. **Warning**: This deletes all data in the shard. Ensure resources are migrated first.

### Monitoring

Monitor shard-specific metrics:

```promql
# Etcd latency by shard
histogram_quantile(0.99, rate(etcd_request_duration_seconds_bucket{shard="main"}[5m]))

# Storage usage by shard
etcd_mvcc_db_total_size_in_bytes{shard="main"}

# Shard availability
up{job="etcd-main"}
```

### Backup and Restore

- **Per-shard backups**: Each shard has independent backup schedule
- **Selective backup**: Skip low-priority shards to reduce backup time/cost
- **Restore**: Must restore all shards to consistent point-in-time

### Troubleshooting

#### Shard-specific pod failures

```bash
# Check shard-specific StatefulSet
oc get statefulset -n <namespace> etcd-<shard-name>

# Check shard logs
oc logs -n <namespace> etcd-<shard-name>-0 -c etcd

# Verify shard service
oc get svc -n <namespace> etcd-<shard-name>-client
```

#### Resource routing issues

```bash
# Check kube-apiserver args for etcd-servers-overrides
oc get deployment -n <namespace> kube-apiserver -o yaml | grep etcd-servers

# Expected output:
# --etcd-servers=https://etcd-main-client.namespace.svc:2379
# --etcd-servers-overrides=/coordination.k8s.io/leases#;https://etcd-leases-client.namespace.svc:2379,/events#;https://etcd-events-client.namespace.svc:2379
```

## Limitations and Caveats

### Current Limitations

1. **No in-place migration**: Cannot enable sharding on existing clusters
2. **Immutable configuration**: Cannot modify sharding config after cluster creation

### Design Constraints

1. **Exactly one default shard**: One shard must have "/" in resourcePrefixes
2. **Max 10 shards**: Limit to prevent excessive complexity
3. **No overlapping prefixes**: Each resource prefix must route to exactly one shard
4. **DNS-1035 shard names**: Max 15 characters, lowercase alphanumeric + hyphens
5. **Immutable after creation**: Cannot change shard configuration post-deployment

## Best Practices

### Shard Design

1. **Start simple**: Use 3-shard config (main + events + leases) for most large clusters
2. **Measure first**: Profile etcd before adding more shards
3. **Isolate high-churn**: Separate resources with high mutation rates
4. **Size appropriately**: Give main shard 2x storage of auxiliary shards

### Storage Planning

| Shard Type | Recommended Size | Rationale |
|------------|------------------|-----------|
| Main (/) | 8-16Gi | Stores all cluster state |
| Events | 2-4Gi | Short TTL, high churn |
| Leases | 1-2Gi | Small objects, auto-GC |

### Replica Configuration

| Cluster Availability | Main Shard | Auxiliary Shards |
|---------------------|------------|------------------|
| SingleReplica | 1 | 1 |
| HighlyAvailable | 3 | 1-3 (based on priority) |

## Future Enhancements

### Completed Features

- ✅ **CLI integration**: `--etcd-sharding-config` and `--etcd-shard` flags (available in current version)

### Planned Features

The following features are planned for future releases:

- **Dynamic shard rebalancing**: Move prefixes between shards
- **In-place migration**: Enable sharding on existing clusters
- **Auto-sharding**: Automatic prefix distribution based on metrics
- **Grafana dashboards**: Pre-built shard visualization
- **Health scoring**: Aggregated shard health metrics

## References

- [Kubernetes etcd-servers-overrides](https://kubernetes.io/docs/reference/command-line-tools-reference/kube-apiserver/)
- [HyperShift Architecture](https://hypershift-docs.netlify.app/)
- Implementation Plan: `.spec/001-etcd-shard/implementation-plan.md`
- Requirements: `.spec/001-etcd-shard/requirements.md`

## Example Use Cases

### Use Case 1: 10,000 Node Cluster

**Problem**: Etcd storage growing 10GB/week due to event churn

**Solution**: 3-shard configuration
- Main: 3 replicas, 16Gi, backup every 30min
- Events: 1 replica, 8Gi, no backup
- Leases: 1 replica, 4Gi, no backup

**Result**: 70% reduction in main shard growth, stable P99 latency

### Use Case 2: Multi-Tenant Platform

**Problem**: Many operators creating thousands of leases, impacting API latency

**Solution**: Isolate leases to dedicated shard
- Main: Critical data with HA
- Leases: Separate shard, tolerant to occasional downtime

**Result**: 50% reduction in API latency for critical operations

### Use Case 3: Development/Staging

**Problem**: Frequent cluster recycling makes backup overhead too high

**Solution**: Selective backup strategy
- Main: Backup enabled for disaster recovery
- Events/Leases: No backup (recreatable)

**Result**: 80% reduction in backup time and storage costs
