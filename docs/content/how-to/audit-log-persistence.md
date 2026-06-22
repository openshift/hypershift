# Audit Log Persistence

The Audit Log Persistence feature provides persistent storage for kube-apiserver audit logs in hosted clusters, along with automatic snapshot creation for crash recovery. This feature enables management cluster administrators to ensure audit logs survive pod restarts and crashes, making them available for compliance, security auditing, and troubleshooting purposes.

## Overview

When enabled, the audit log persistence feature:

- Creates PersistentVolumeClaims (PVCs) for each kube-apiserver pod to store audit logs
- Configures kube-apiserver to write audit logs to persistent storage
- Automatically creates VolumeSnapshots when kube-apiserver pods crash or restart
- Manages snapshot retention based on configurable policies

## Enabling the Feature

The audit log persistence feature must be enabled at two levels:

### 1. Operator-Level Enablement

Enable the feature during HyperShift operator installation using the `--enable-audit-log-persistence` flag:

```bash
hypershift install --enable-audit-log-persistence
```

This flag:

- Installs the `AuditLogPersistenceConfig` CRD
- Sets the `ENABLE_AUDIT_LOG_PERSISTENCE=true` environment variable on the HyperShift operator deployment
- Registers mutating webhooks for Pods and ConfigMaps (requires webhook certificates)
- Enables the snapshot controller that watches for pod crashes

### 2. Configuration-Level Enablement

After the operator is installed, create an `AuditLogPersistenceConfig` resource to enable and configure the feature:

```yaml
apiVersion: auditlogpersistence.hypershift.openshift.io/v1alpha1
kind: AuditLogPersistenceConfig
metadata:
  name: cluster
spec:
  enabled: true
```

**Important**: The `AuditLogPersistenceConfig` resource must be named `cluster` and is cluster-scoped. Only one instance can exist in the management cluster.

## Configuration Options

The `AuditLogPersistenceConfig` resource provides comprehensive configuration options organized into three main sections:

### Global Enablement

- **`spec.enabled`** (boolean, default: `false`)
  - Enables or disables the audit log persistence feature globally
  - When disabled, no PVCs will be created and no snapshots will be taken
  - Must be set to `true` to activate the feature

### Storage Configuration (`spec.storage`)

Controls how PersistentVolumeClaims are created for audit log storage:

- **`spec.storage.storageClassName`** (string, optional)
  - The name of the StorageClass to use for PVCs
  - If not specified, the default storage class will be used
  - Example: `"gp3-csi"`

- **`spec.storage.size`** (string, default: `"5Gi"`)
  - The size of each PVC created for kube-apiserver pods
  - Must be a valid Kubernetes quantity (e.g., `"5Gi"`, `"10Gi"`, `"100G"`)
  - Each kube-apiserver pod gets its own PVC

### Audit Log Configuration (`spec.auditLog`)

Controls kube-apiserver audit log settings:

- **`spec.auditLog.maxSize`** (integer, default: `200`)
  - Maximum size in megabytes of the audit log file before rotation
  - Corresponds to the `--audit-log-maxsize` kube-apiserver argument
  - Minimum value: `1`
  - Example: `200` (200 MB)

- **`spec.auditLog.maxBackup`** (integer, default: `10`)
  - Maximum number of old audit log files to retain
  - Corresponds to the `--audit-log-maxbackup` kube-apiserver argument
  - Minimum value: `1`
  - Example: `10` (keeps 10 rotated log files)

### Snapshot Configuration (`spec.snapshots`)

Controls automatic snapshot creation and retention:

- **`spec.snapshots.enabled`** (boolean, default: `true`)
  - Enables or disables automatic snapshot creation on pod crashes
  - When enabled, snapshots are created when kube-apiserver pod restart count increases
  - Can be disabled independently of the main feature

- **`spec.snapshots.minInterval`** (string, default: `"1h"`)
  - Minimum time interval between snapshots for the same pod
  - Prevents creating too many snapshots in rapid succession
  - Must be a valid duration string (e.g., `"1h"`, `"30m"`, `"2h30m"`)
  - Format: `^([0-9]+(ns|us|µs|ms|s|m|h))+$`
  - Example: `"1h"` (one hour minimum between snapshots)

- **`spec.snapshots.perPodRetentionCount`** (integer, default: `10`)
  - Maximum number of snapshots to retain per PVC
  - When this limit is reached, the oldest snapshot for that PVC is deleted
  - Minimum value: `1`
  - Example: `10` (keeps 10 snapshots per pod)

- **`spec.snapshots.namespaceRetentionCount`** (integer, default: `50`)
  - Maximum total number of snapshots to retain per namespace (hosted control plane)
  - When this limit is reached, the oldest snapshot in the namespace is deleted
  - Minimum value: `1`
  - Example: `50` (keeps 50 snapshots total per hosted cluster)

- **`spec.snapshots.volumeSnapshotClassName`** (string, optional)
  - The name of the VolumeSnapshotClass to use for creating snapshots
  - If not specified, the system attempts to match the PVC's StorageClass provisioner to an appropriate VolumeSnapshotClass
  - Example: `"csi-aws-vsc"`

## Complete Configuration Example

Here's a complete example configuration with all options specified:

```yaml
apiVersion: auditlogpersistence.hypershift.openshift.io/v1alpha1
kind: AuditLogPersistenceConfig
metadata:
  name: cluster
spec:
  enabled: true
  storage:
    storageClassName: "gp3-csi"
    size: "10Gi"
  auditLog:
    maxSize: 500      # 500 MB per log file
    maxBackup: 20     # Keep 20 rotated log files
  snapshots:
    enabled: true
    minInterval: "2h"                    # Minimum 2 hours between snapshots
    perPodRetentionCount: 15             # Keep 15 snapshots per pod
    namespaceRetentionCount: 100         # Keep 100 snapshots per namespace
    volumeSnapshotClassName: "csi-aws-vsc"
```

## How It Works

### Pod Mutation

When a kube-apiserver pod is created in a hosted control plane namespace:

1. The pod mutating webhook intercepts the pod creation
2. Checks if the feature is enabled via `AuditLogPersistenceConfig`
3. Creates a PVC named `kas-audit-logs-<pod-name>` if it doesn't exist
4. Replaces the ephemeral `logs` volume with a PVC-backed volume
5. Sets the PVC owner reference to the pod's ReplicaSet for proper cleanup

### ConfigMap Mutation

When the `kas-config` ConfigMap is created or updated:

1. The ConfigMap mutating webhook intercepts the change
2. Checks if the feature is enabled
3. Updates the kube-apiserver configuration JSON to include:
   - `--audit-log-maxsize` based on `spec.auditLog.maxSize`
   - `--audit-log-maxbackup` based on `spec.auditLog.maxBackup`

### Snapshot Creation

The snapshot controller watches kube-apiserver pods and:

1. Detects when a pod's restart count increases (indicating a crash)
2. Checks if snapshots are enabled and minimum interval has passed
3. Creates a VolumeSnapshot with a timestamped name: `<pvc-name>-snapshot-<timestamp>`
4. Labels the snapshot with:
   - `hypershift.openshift.io/audit-logs-pvc`: PVC name
   - `hypershift.openshift.io/audit-logs-pod`: Pod name
   - `hypershift.openshift.io/hosted-control-plane-namespace`: Namespace
5. Updates pod annotations with the last snapshotted restart count and timestamp
6. Manages retention by deleting oldest snapshots when limits are exceeded

## Default Values Summary

| Configuration | Default Value |
|---------------|---------------|
| `spec.enabled` | `false` |
| `spec.storage.size` | `"5Gi"` |
| `spec.storage.storageClassName` | (uses default StorageClass) |
| `spec.auditLog.maxSize` | `200` (MB) |
| `spec.auditLog.maxBackup` | `10` |
| `spec.snapshots.enabled` | `true` |
| `spec.snapshots.minInterval` | `"1h"` |
| `spec.snapshots.perPodRetentionCount` | `10` |
| `spec.snapshots.namespaceRetentionCount` | `50` |
| `spec.snapshots.volumeSnapshotClassName` | (auto-detected) |

## Requirements

- HyperShift operator with `--enable-audit-log-persistence` flag
- Webhook certificates configured (for mutating webhooks)
- StorageClass available in hosted control plane namespaces
- VolumeSnapshotClass available (for snapshot functionality)
- CSI snapshotter installed (for VolumeSnapshot support)

## Troubleshooting

### Feature Not Working

1. Verify the operator has `ENABLE_AUDIT_LOG_PERSISTENCE=true` environment variable:
   ```bash
   kubectl get deployment -n hypershift hypershift-operator -o jsonpath='{.spec.template.spec.containers[0].env}' | grep ENABLE_AUDIT_LOG_PERSISTENCE
   ```

2. Check that `AuditLogPersistenceConfig` exists and is enabled:
   ```bash
   kubectl get auditlogpersistenceconfig cluster -o yaml
   ```

3. Verify webhooks are registered (check operator logs):
   ```bash
   kubectl logs -n hypershift deployment/hypershift-operator | grep "Audit log persistence"
   ```

### PVCs Not Created

- Ensure the feature is enabled at both operator and configuration levels
- Check webhook logs for errors
- Verify StorageClass exists and is accessible in hosted control plane namespaces

### Snapshots Not Created

- Verify `spec.snapshots.enabled` is `true`
- Check that VolumeSnapshotClass is available
- Review snapshot controller logs for errors
- Ensure minimum interval has passed since last snapshot

### Storage Issues

- Verify StorageClass has sufficient capacity
- Check PVC status: `kubectl get pvc -n <hosted-control-plane-namespace>`
- Review storage provider logs for provisioning errors

## Disabling the Feature

To disable the feature:

1. Set `spec.enabled: false` in the `AuditLogPersistenceConfig`:
   ```bash
   kubectl patch auditlogpersistenceconfig cluster --type=merge -p '{"spec":{"enabled":false}}'
   ```

2. Existing PVCs will remain but new ones won't be created
3. To fully remove, delete the `AuditLogPersistenceConfig` resource (after disabling)

**Note**: Disabling at the operator level (removing `--enable-audit-log-persistence`) requires operator redeployment and will prevent the CRD and webhooks from functioning.

