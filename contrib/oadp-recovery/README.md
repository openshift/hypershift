# OADP Recovery Script

A simple bash script that periodically checks for HostedClusters paused by OADP (OpenShift API for Data Protection) and automatically resumes them when their associated Velero backups reach a terminal state.

## Overview

This script provides automated recovery for HyperShift clusters that may get stuck in a paused state due to OADP backup operations. It runs as a simple Kubernetes CronJob using a bash script instead of a complex Go application.

## Features

- **Automatic Detection**: Identifies clusters paused by the HyperShift OADP plugin
- **Backup Status Monitoring**: Monitors Velero backup states in real-time
- **Intelligent Recovery**: Resumes clusters when backups complete, fail, or are deleted
- **NodePool Management**: Automatically resumes associated NodePools
- **Dry-run Mode**: Supports testing without making actual changes
- **Structured Logging**: Comprehensive logging with cluster context
- **Simple Deployment**: Single manifest file with minimal dependencies

## Recovery Logic

1. **Detection**: Checks for clusters with OADP pause annotations:
   - `oadp.openshift.io/paused-by: hypershift-oadp-plugin`
   - `oadp.openshift.io/paused-at: <timestamp>`

2. **Backup Analysis**: Searches for related Velero backups using:
   - Backup name patterns containing cluster name
   - Included namespaces matching cluster namespace

3. **Terminal State Check**: Monitors for backup states:
   - `Completed`: Backup finished successfully
   - `Failed`: Backup encountered errors
   - `PartiallyFailed`: Backup completed with some failures
   - `Deleted`: Backup was manually deleted

4. **Recovery**: When terminal state detected or no backups found:
   - Removes OADP pause annotations
   - Clears `pausedUntil` field
   - Resumes all associated NodePools

## Quick Start

### Deploy CronJob

```bash
# Deploy OADP Recovery CronJob
make deploy
```

### Test Locally

```bash
# Test the script in dry-run mode (safe)
./oadp-recovery.sh --dry-run --log-level verbose

# Run complete integration test (real changes)
make test

# Individual test targets
make test-clusters    # Create test clusters only
make test-recovery    # Test recovery against existing clusters
make test-cleanup     # Clean up test resources
```

### View Status

```bash
# Check CronJob status
make status

# View recent logs
make logs
```

### Remove

```bash
# Clean up deployment
make clean
```

## Configuration

The script supports both command-line arguments and environment variables.

### Command-Line Arguments

```bash
./oadp-recovery.sh [options]

Options:
  --dry-run                   Enable dry-run mode (no changes made)
  --oadp-namespace NAMESPACE  OADP/Velero namespace (default: openshift-adp)
  --log-level LEVEL           Log verbosity: info, verbose, debug (default: info)
  --help, -h                  Show help message
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DRY_RUN` | `false` | Enable dry-run mode for testing |
| `OADP_NAMESPACE` | `openshift-adp` | Namespace where Velero backups are stored |
| `LOG_LEVEL` | `info` | Logging verbosity (`info`, `verbose`, `debug`) |

### CronJob Configuration

The CronJob is configured in `manifests/rbac-cronjob.yaml` with:

- **Schedule**: Every 15 minutes (`*/15 * * * *`)
- **Namespace**: `hypershift`
- **Image**: `registry.redhat.io/ubi9/ubi-minimal:latest`
- **Resources**: Minimal CPU/memory limits

To customize, edit the manifest before deploying:

```yaml
spec:
  schedule: "*/5 * * * *"  # Run every 5 minutes instead
  jobTemplate:
    spec:
      template:
        spec:
          containers:
          - name: oadp-recovery
            env:
            - name: LOG_LEVEL
              value: "verbose"  # More detailed logging
```

## Deployment Details

### Prerequisites

- HyperShift cluster with RBAC permissions
- OADP/Velero installed and configured
- Access to the `hypershift` namespace

### RBAC Permissions

The script requires minimal permissions:

```yaml
rules:
- apiGroups: ["hypershift.openshift.io"]
  resources: ["hostedclusters", "nodepools"]
  verbs: ["get", "list", "patch", "update"]
- apiGroups: ["velero.io"]
  resources: ["backups"]
  verbs: ["get", "list"]
- apiGroups: [""]
  resources: ["namespaces"]
  verbs: ["get"]
```

### Container Image

The CronJob uses the standard UBI minimal image and mounts the script from a ConfigMap that is generated dynamically from the standalone script file, eliminating the need for custom container images and avoiding code duplication.

## Monitoring

### Viewing Logs

```bash
# Real-time logs
kubectl logs -n hypershift -l job-name --follow

# Recent executions
kubectl get jobs -n hypershift -l job-name --sort-by=.metadata.creationTimestamp
```

### Log Output

The script provides structured logging:

```text
[INFO] 2023-12-15 10:30:00 Starting OADP recovery check (oadp-namespace: openshift-adp, dry-run: false)
[INFO] 2023-12-15 10:30:01 Cluster my-cluster needs to be unpaused
[INFO] 2023-12-15 10:30:01 Resuming cluster my-cluster from OADP pause
[INFO] 2023-12-15 10:30:02 Successfully resumed HostedCluster my-cluster
[INFO] 2023-12-15 10:30:02 Resuming NodePool my-pool for cluster my-cluster
[INFO] 2023-12-15 10:30:02 Successfully resumed NodePool my-pool
[INFO] 2023-12-15 10:30:03 OADP recovery completed: total=5, processed=5, recovered=1, errors=0
[INFO] 2023-12-15 10:30:03 Recovered clusters: my-cluster
```

## Troubleshooting

### Common Issues

1. **Permission Denied**
   ```bash
   # Check RBAC configuration
   kubectl get clusterrolebinding oadp-recovery
   kubectl get serviceaccount oadp-recovery -n hypershift
   ```

2. **No Backups Found**
   ```bash
   # Verify OADP namespace and backups exist
   kubectl get backups -n openshift-adp
   ```

3. **Script Not Running**
   ```bash
   # Check CronJob status
   kubectl get cronjob oadp-recovery -n hypershift
   kubectl describe cronjob oadp-recovery -n hypershift
   ```

4. **Test Environment Cleanup**
   ```bash
   # Manual cleanup if integration tests fail
   make test-cleanup

   # Or manual cleanup with proper order
   kubectl delete np -n test-oadp-recovery --all
   kubectl delete hc -n test-oadp-recovery --all
   kubectl delete namespace test-oadp-recovery
   ```

### Debug Mode

Enable verbose logging by updating the CronJob:

```yaml
env:
- name: LOG_LEVEL
  value: "debug"
```

### Dry-run Testing

Test without making changes:

```yaml
env:
- name: DRY_RUN
  value: "true"
```

## Contributing

When making changes:

1. **Test locally**: Use `./oadp-recovery.sh --dry-run` first
2. **Run integration tests**: Verify with `make test` (includes real recovery testing)
3. **Check permissions**: Ensure RBAC is sufficient for changes
4. **Document changes**: Update this README for new features

### Integration Test Details

The `make test` target executes the `test/test-integration.sh` bash script that runs a comprehensive integration test:

1. **Isolation**: Scales HyperShift Operator to 0 for test isolation
2. **Cleanup**: Cleans any existing test resources first
3. **Setup**: Creates 5 test clusters (some paused by OADP, some not)
4. **Recovery**: Executes the script without dry-run to make real changes
5. **Verification**: Confirms all clusters are unpaused and OADP annotations removed
6. **Final Cleanup**: Ensures complete cleanup before scaling operator back up
7. **Restoration**: Scales HyperShift Operator back to 1

The test creates clusters in `test-oadp-recovery` namespace and includes built-in error handling with automatic cleanup on failure.

**Individual test targets:**
- `make test-clusters`: Create test clusters only
- `make test-recovery`: Run recovery against existing clusters
- `make test-cleanup`: Clean up test resources manually

**Warning**: The integration test makes real changes to clusters. It's designed to be safe but only run it in development environments.

For questions or issues, please file a GitHub issue or contact the HyperShift team.