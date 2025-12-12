# OADP Recovery Tests

This directory contains both unit tests and integration test utilities for the OADP recovery functionality in HyperShift.

## Overview

The OADP recovery tool automatically detects and unpauses HyperShift clusters that were paused due to OADP (OpenShift API for Data Protection) backup operations. These integration tests create test scenarios with multiple HostedClusters and NodePools to validate the recovery functionality.

## Test Structure

### Unit Tests
- `unit/run_test.go`: Core recovery logic unit tests
- `unit/integration_scenarios_test.go`: Integration scenario unit tests

### Integration Utilities
- `create-test-clusters.sh`: Script to create test HostedClusters and NodePools directly in Kubernetes
- `README.md`: This documentation

## Unit Tests

### Running Unit Tests

```bash
# Run all unit tests from project root
make test

# Run unit tests with verbose output
go test ./test/unit/... -v

# Run unit tests with coverage
go test ./test/unit/... -cover
```

### Test Coverage

The unit tests provide comprehensive coverage of:

- **OADP Detection Logic**: Testing cluster annotation matching
- **Backup Status Evaluation**: Terminal vs in-progress backup states
- **Recovery Operations**: Cluster and NodePool pause/unpause logic
- **Dry-run Mode**: Testing without actual cluster modifications
- **Integration Scenarios**: Multi-cluster environments with mixed states
- **Error Handling**: Various failure scenarios and edge cases

## Test Data Generation

The script creates:
- **Configurable number of HostedClusters** (default: 2)
- **1 NodePool per HostedCluster**
- **Required secrets** (pull-secret, ssh-key, etcd-encryption-key)
- **Random pause states** using OADP annotations

### Pause Logic

Clusters are paused based on their index:
- **Odd-numbered clusters** (1, 3, 5...) → Paused with OADP annotations
- **Even-numbered clusters** (2, 4, 6...) → Active (no pause)

## Integration Testing

### 1. Create Test Clusters

```bash
# Create 2 test clusters (default)
./create-test-clusters.sh

# Create 5 test clusters
NUM_CLUSTERS=5 ./create-test-clusters.sh

# Use different base name
BASE_NAME=my-test NUM_CLUSTERS=3 ./create-test-clusters.sh
```

### 2. Verify Creation

```bash
kubectl get hostedcluster -n test-oadp-recovery
kubectl get nodepool -n test-oadp-recovery
kubectl get secrets -n test-oadp-recovery
```

### 3. Test OADP Recovery

```bash
# Create a manual job from the OADP recovery CronJob
kubectl create job --from=cronjob/oadp-recovery test-recovery -n openshift-adp

# Watch the logs to see recovery in action
kubectl logs -f job/test-recovery -n openshift-adp
```

### 4. Clean Up

```bash
kubectl delete namespace test-oadp-recovery
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NUM_CLUSTERS` | `2` | Number of HostedClusters to create |
| `BASE_NAME` | `test-cluster` | Base name for cluster naming |
| `NAMESPACE` | `test-oadp-recovery` | Kubernetes namespace for resources |

## Expected Behavior

### Test Resources Created
- HostedClusters with realistic AWS configurations
- NodePools with proper owner references
- Required secrets for each cluster
- Random infraIDs, networking CIDRs, and resource names

### OADP Annotations (Paused Clusters)
```yaml
metadata:
  annotations:
    oadp.openshift.io/paused-by: "oadp-test-backup-abc123"
    oadp.openshift.io/paused-at: "2025-12-12T08:51:01Z"
spec:
  pausedUntil: "2099-01-01T00:00:00Z"
```

### OADP Recovery Expected Results

When running the OADP recovery tool on test clusters:

```json
{
  "msg": "OADP recovery completed",
  "totalClusters": 2,
  "clustersRecovered": 0,
  "nodePoolsRecovered": 0,
  "errors": 0
}
```

**Why 0 clusters recovered?**
- The recovery tool checks for related Velero backups
- Since test clusters have no real backup operations, they remain paused
- This is the correct behavior - clusters should only be unpaused when their backups are in terminal states

## Integration with OADP Recovery Tool

The test clusters integrate perfectly with the OADP recovery tool:

1. **Detection**: Tool finds HostedClusters with OADP pause annotations
2. **Processing**: Checks each cluster's backup status (none found for test clusters)
3. **Decision**: Keeps clusters paused since no terminal backups exist
4. **Logging**: Provides detailed logs for SRE monitoring

## Troubleshooting

### Permission Issues
Ensure you have proper RBAC permissions:
```bash
kubectl auth can-i create hostedcluster -n test-oadp-recovery
kubectl auth can-i create nodepool -n test-oadp-recovery
kubectl auth can-i create secret -n test-oadp-recovery
```

### Resource Creation Failures
Check the API server logs if resources fail to create:
```bash
kubectl describe hostedcluster test-cluster-01 -n test-oadp-recovery
kubectl describe nodepool test-cluster-01-workers-1 -n test-oadp-recovery
```

### Missing OADP Recovery CronJob
Ensure the OADP recovery CronJob is deployed:
```bash
kubectl get cronjob -n openshift-adp
```

## Example Output

```bash
$ NUM_CLUSTERS=3 ./create-test-clusters.sh

🚀 Creating 3 test clusters directly
namespace/test-oadp-recovery created
🏗️  Creating clusters...
  📦 Creating test-cluster-01 (paused: true)
    ✅ test-cluster-01 created successfully

  📦 Creating test-cluster-02 (paused: false)
    ✅ test-cluster-02 created successfully

  📦 Creating test-cluster-03 (paused: true)
    ✅ test-cluster-03 created successfully

🎉 Cluster creation completed!
   Created: 3/3 HostedClusters
   Paused: 2 (with OADP annotations)
   Active: 1
   NodePools: 3

🔍 Verification:
NAME              VERSION   KUBECONFIG   PROGRESS   AVAILABLE   PROGRESSING   MESSAGE
test-cluster-01
test-cluster-02
test-cluster-03

🧪 To test OADP recovery:
   kubectl create job --from=cronjob/oadp-recovery test-recovery -n openshift-adp
   kubectl logs job/test-recovery -n openshift-adp

🧹 To cleanup:
   kubectl delete namespace test-oadp-recovery
```

This provides comprehensive testing of the OADP recovery functionality with realistic cluster configurations.