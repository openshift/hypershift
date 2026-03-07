# HyperShift Backup and Restore E2E Tests

This directory contains end-to-end tests for HyperShift control plane backup and restore functionality using OADP (OpenShift API for Data Protection) and Velero.

## Overview

The backup and restore tests validate the ability to:
- Create backups of HyperShift hosted cluster control planes
- Create scheduled backups using Velero schedules
- Restore control planes from backups after catastrophic failures
- Verify control plane health after backup and restore operations

## Prerequisites

### Required Components

1. **OADP Operator**: Must be installed and running in the `openshift-adp` namespace on the management cluster
2. **Storage for OADP data**: Might be S3 bucket or other storage
3. **Secret for accessing storage**
4. **Backup Storage Location**: Configured storage location for backups (e.g., S3 bucket for AWS)
5. **Volume Snapshot Location**: Configured snapshot location
6. **DataProtectionApplication**: This resource brings Velero pod that handles the backups
7. **HyperShift CLI**: The `hypershift` binary must be available in your PATH
8. **Platform**: Currently supports AWS platform only

#### Platform-specific Prerequisites

This section includes platform-specific configurations for some of the prerequisites above.

##### AWS

* S3 bucket

```bash
region="<aws_region>"
bucket_name="<s3_bucket_name>"

aws s3api create-bucket --bucket $bucket_name --region $region
```

* Secret:

```bash
secret_data="$(base64 -w 0 ~/.aws/credentials)"
cluster_name="<cluster name>"

cat <<EOF | oc apply -f -
apiVersion: v1
data:
 credentials: $secret_data
kind: Secret
metadata:
 name: $cluster_name
 namespace: openshift-adp
type: Opaque
```


* DataProtectionApplication

```bash
cat <<EOF | oc apply -f -
apiVersion: oadp.openshift.io/v1alpha1
kind: DataProtectionApplication
metadata:
  name: dpa-instance
  namespace: openshift-adp
spec:
  backupImages: false
  configuration:
    nodeAgent:
      enable: true
      uploaderType: kopia
    velero:
      customPlugins:
        - name: hypershift-oadp-plugin
          image: quay.io/redhat-user-workloads/ocp-art-tenant/oadp-hypershift-oadp-plugin-main:main
      defaultPlugins:
        - openshift
        - aws
        - csi
        - kubevirt
      disableFsBackup: false
      resourceTimeout: 2h
      noDefaultBackupLocation: true
      logLevel: debug
EOF
```

* BackupStorageLocation

```bash
bucket_name="<s3_bucket_name>"
cluster_name="<cluster_name>"
region="<aws_region>"
profile="<profile in AWS credentials file to be used>"

cat <<EOF | oc apply -f -
apiVersion: velero.io/v1
kind: BackupStorageLocation
metadata:
 name: $cluster_name
 namespace: openshift-adp
spec:
 provider: aws
 objectStorage:
   bucket: $bucket_name
   prefix: backup-objects
 credential:
   name: $cluster_name
   key: credentials
 config:
   region: $region
   profile: $profile
EOF
```

* VolumeSnapshotLocation

```bash
cluster_name="<your_cluster_name>"
region="<aws_region>"

cat <<EOF | oc apply -f -
apiVersion: velero.io/v1
kind: VolumeSnapshotLocation
metadata:
  name: $cluster_name
  namespace: openshift-adp
spec:
  provider: aws
  credential:
    name: $cluster_name
    key: credentials
  config:
    region:  $region
    profile: "default"
EOF
```

## Test Structure

The tests follow an ordered, serial execution flow:

### Test Phases

1. **PreBackupGuest**: Validates guest cluster health before backup
2. **SetupContinual**: Sets up continual operations to verify control plane or data plane availability
3. **Backup**: Creates both a one-time backup and a scheduled backup
4. **VerifyContinual**: Verifies continual operations completed successfully
5. **PostBackupControlPlane**: Validates control plane health after backup
6. **PostBackupGuest**: Validates guest cluster health after backup
7. **BreakControlPlane**: Simulates a catastrophic failure by deleting control plane resources
8. **Restore**: Restores the control plane from the backup
9. **PostRestoreControlPlane**: Validates control plane health after restore
10. **PostRestoreGuest**: Validates guest cluster health after restore

### Key Test Operations

#### Catastrophic Failure Simulation

The `BreakControlPlane` phase simulates a disaster by:
1. Deleting the HostedCluster resource
2. Deleting the control plane namespace
3. Deleting the HostedControlPlane resource
4. Forcing deletion by removing finalizers if needed

## Running the Tests

* Build the test binary and run tests in one go

```bash
make test-backup-restore
```

* Build the test binary separately and run tests manually

```bash
# Build the test binary
make backuprestore-e2e

# Run tests
./bin/test-backuprestore \
    --ginkgo.v \
    --ginkgo.junit-report="/tmp/junit.xml" \
    --ginkgo.label-filter="backup-restore" \
    --ginkgo.fail-fast=true \
    --ginkgo.timeout=2h
```

* Run tests instantly using ginkgo without building the test binary

```bash
go run github.com/onsi/ginkgo/v2/ginkgo \
    --vv --tags=e2ev2,backuprestore \
    --label-filter="backup-restore" \
    --junit-report="/tmp/junit.xml" \
    --fail-fast=true \
    --timeout=2h \
    ./test/e2e/v2/tests
```

### Required Environment Variables

The tests rely on the standard HyperShift E2E test context, which requires:

- `E2E_HOSTED_CLUSTER_NAME`: Name of the HostedCluster to test
- `E2E_HOSTED_CLUSTER_NAMESPACE`: Namespace containing the HostedCluster
- `ARTIFACT_DIR`: Directory to store test artifacts and logs (or default value is used)

## Artifacts and Logging

Test artifacts are stored in the `ARTIFACT_DIR`:

- `oadp-backup-{namespace}-{name}.log`: Backup creation logs
- `oadp-restore-{namespace}-{name}.log`: Restore creation logs
- `oadp-schedule-{namespace}-{name}.log`: Schedule creation logs

## Known Limitations

1. **Platform Support**: Currently only supports AWS platform
2. **Guest Cluster Validation**: Guest cluster health checks are skipped due to OCPBUGS-59876
3. **Continual Operations**: Continual operation verification is skipped until CNTRLPLANE-2676 is implemented
4. **Serial Execution**: Tests must run serially as it breaks the cluster
