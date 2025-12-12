# OADP Recovery

A standalone script that periodically checks for HostedClusters paused by OADP (OpenShift API for Data Protection) and automatically resumes them when their associated Velero backups reach a terminal state.

## Overview

This script was extracted from the HyperShift controller to run as an independent CronJob. It provides automated recovery for HyperShift clusters that may get stuck due to OADP backup operations.

## Features

- **Automatic Detection**: Identifies clusters paused by the HyperShift OADP plugin
- **Backup Status Monitoring**: Monitors Velero backup states in real-time
- **Intelligent Recovery**: Resumes clusters when backups complete, fail, or are deleted
- **NodePool Management**: Automatically resumes associated NodePools
- **Dry-run Mode**: Supports testing without making actual changes
- **Structured Logging**: Comprehensive logging with cluster context

## Architecture

### Components

- **Main Script** (`main.go`): Core recovery logic
- **RBAC Configuration**: Minimal permissions for cluster operations
- **CronJob**: Kubernetes-native scheduling
- **Docker Image**: Distroless container for security

### Recovery Logic

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

4. **Recovery**: When terminal state detected:
   - Removes OADP pause annotations
   - Clears `pausedUntil` field
   - Resumes all associated NodePools

## Deployment

### Prerequisites

- HyperShift cluster with RBAC permissions
- OADP/Velero installed and configured
- Access to `openshift-adp` namespace

### Installation

#### Quick Installation

```bash
# Deploy OADP Recovery CronJob
make deploy
```

#### Manual Installation

1. **Apply RBAC configuration**:
   ```bash
   kubectl apply -f manifests/01-serviceaccount.yaml
   kubectl apply -f manifests/02-clusterrole.yaml
   kubectl apply -f manifests/03-clusterrolebinding.yaml
   ```

2. **Deploy the CronJob**:
   ```bash
   kubectl apply -f manifests/04-cronjob.yaml
   ```

#### Removal

```bash
# Remove OADP Recovery CronJob
make remove
```

### Configuration

The CLI supports both command-line flags and environment variables:

#### Command-Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--oadp-namespace` | `openshift-adp` | Namespace where OADP/Velero backups are stored |
| `--dry-run` | `false` | Enable dry-run mode (no actual changes made) |
| `--log-dev` | `false` | Enable development logging (human-friendly) |
| `--log-level` | `0` | Log verbosity level (0=info, 1=verbose, 2=debug) |

#### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `OADP_NAMESPACE` | `openshift-adp` | Namespace where Velero backups are stored |
| `DRY_RUN` | `false` | Enable dry-run mode for testing |
| `LOG_LEVEL` | `info` | Logging verbosity (`debug`, `verbose`, `info`) |

Environment variables override defaults but are overridden by command-line flags.

### Schedule Configuration

The default schedule runs every 5 minutes (`*/5 * * * *`). Adjust based on your requirements:

- **Development**: `*/1 * * * *` (every minute for faster testing)
- **Production**: `*/5 * * * *` (every 5 minutes for balance)
- **Conservative**: `*/15 * * * *` (every 15 minutes for minimal load)

## Testing

### Unit Tests

The project includes comprehensive unit tests organized under the `test/` directory:

```bash
# Run all tests
make test

# Run tests with verbose output
go test ./test/unit/... -v

# Run tests with coverage
go test ./test/unit/... -cover
```

#### Test Structure

- `test/unit/run_test.go`: Core recovery logic tests
- `test/unit/integration_scenarios_test.go`: Integration scenario tests

#### Test Scenarios Covered

- **OADP Detection**: Clusters with/without OADP annotations
- **Backup Status Handling**: Terminal vs in-progress backup states
- **Recovery Logic**: Cluster and NodePool recovery operations
- **Dry-run Mode**: Testing without actual changes
- **Integration Scenarios**: Multi-cluster environments with mixed states

### Integration Tests

For integration testing with real clusters, use the test helper scripts:

```bash
# Create test clusters for integration testing
cd test
NUM_CLUSTERS=3 ./create-test-clusters.sh
```

## Building

### Local Development

```bash
cd contrib/oadp-recovery
make build
```

### CLI Usage

The tool provides a comprehensive CLI interface:

#### Help and Documentation

```bash
# Show general help
./bin/oadp-recovery --help

# Show help for the run command
./bin/oadp-recovery run --help
```

#### Running OADP Recovery

```bash
# Basic execution
./bin/oadp-recovery run

# Dry-run mode (recommended for testing)
./bin/oadp-recovery run --dry-run

# Debug logging
./bin/oadp-recovery run --log-level=2 --log-dev

# Custom OADP namespace
./bin/oadp-recovery run --oadp-namespace=my-oadp-ns

# Combined flags
./bin/oadp-recovery run --dry-run --log-level=1 --oadp-namespace=openshift-adp
```

### Container Image

#### Quick Build and Push

```bash
# Build and push with defaults (quay.io/hypershift/oadp-recovery:latest)
make docker-push

# Build only
make docker-build
```

#### Custom Registry/Tag

```bash
# Custom registry and tag
make docker-build REGISTRY=my-registry.com/myorg IMAGE_TAG=v1.0.0
make docker-push REGISTRY=my-registry.com/myorg IMAGE_TAG=v1.0.0
```

#### Manual Build

```bash
docker build -t quay.io/hypershift/oadp-recovery:latest .
docker push quay.io/hypershift/oadp-recovery:latest
```

## Monitoring

### Logs

View CronJob logs:
```bash
kubectl logs -l app=oadp-recovery -n hypershift --follow
```

### Metrics

The script logs structured information including:
- Total clusters processed
- Clusters recovered
- Processing errors
- Execution time

### Alerts

Monitor for:
- Consecutive CronJob failures
- High error rates
- Clusters stuck in paused state

## Troubleshooting

### Common Issues

1. **Permission Denied**
   - Verify RBAC configuration is applied
   - Check ServiceAccount exists in correct namespace

2. **No Backups Found**
   - Confirm OADP_NAMESPACE is correct
   - Verify Velero is installed and running
   - Check backup naming patterns

3. **Clusters Not Resuming**
   - Enable debug logging (`LOG_LEVEL=debug`)
   - Check for backup terminal states
   - Verify annotation matching logic

### Debug Mode

Enable verbose logging:
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

## Security

### RBAC

The script uses minimal required permissions:
- Read access to HostedClusters and NodePools
- Update access for removing pause annotations
- Read access to Velero backups

### Container Security

- Distroless base image
- Non-root user (65532)
- Read-only filesystem
- Security context constraints

## Migration from Controller

This script replaces OADP recovery functionality previously embedded in the HyperShift controller:

### Benefits

- **Decoupling**: Independent from controller reconciliation
- **Simplicity**: No complex cache management needed
- **Observability**: Dedicated logging and metrics
- **Scalability**: Configurable execution frequency

### Compatibility

- Zero downtime migration possible
- Can run alongside controller during transition
- Rollback available by re-enabling controller code

## Contributing

When making changes:

1. **Run tests**: Ensure all unit tests pass with `make test`
2. **Test with dry-run mode**: Validate changes with `--dry-run` flag first
3. **Add test coverage**: Include unit tests for new functionality
4. **Verify RBAC permissions**: Ensure sufficient permissions for new features
5. **Update documentation**: Document new features and configuration options
6. **Follow Go conventions**: Adhere to Go best practices and error handling patterns

### Development Workflow

```bash
# Make changes to code
vim cmd/run.go

# Run unit tests
make test

# Test locally with dry-run
go run main.go run --dry-run --log-level=2

# Build and test binary
make build
./bin/oadp-recovery run --dry-run
```

## Related Documentation

- [HyperShift OADP Integration](../../docs/oadp.md)
- [Velero Backup API](https://velero.io/docs/v1.12/api-types/backup/)
- [Kubernetes CronJob](https://kubernetes.io/docs/concepts/workloads/controllers/cron-jobs/)