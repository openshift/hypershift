# Disaster Recovery CLI

The HyperShift CLI provides disaster recovery capabilities through integrated commands.

## Backup

The `hypershift create oadp-backup` command creates backups of hosted clusters using OADP (OpenShift API for Data Protection).

### Prerequisites

Before creating backups, ensure that:

1. **OADP Operator is installed**: The OADP operator must be installed and running in your management cluster
2. **DataProtectionApplication (DPA) exists**: A DPA custom resource must be configured and ready
3. **Storage location configured**: A backup storage location must be available (e.g., S3, GCS, Azure Blob)

### Basic Usage

```bash
hypershift create oadp-backup \
  --hc-name my-hosted-cluster \
  --hc-namespace my-hosted-cluster-namespace
```

### Command Options

| Flag | Type | Default | Required | Description |
|------|------|---------|----------|-------------|
| `--hc-name` | string | - | ✅ | Name of the hosted cluster to backup |
| `--hc-namespace` | string | - | ✅ | Namespace of the hosted cluster to backup |
| `--oadp-namespace` | string | `openshift-adp` | ❌ | Namespace where OADP operator is installed |
| `--storage-location` | string | `default` | ❌ | Storage location for the backup (must exist in DPA config) |
| `--ttl` | duration | `2h0m0s` | ❌ | Time to live for the backup before automatic deletion |
| `--snapshot-move-data` | bool | `true` | ❌ | Enable snapshot move data feature for CSI volumes |
| `--default-volumes-to-fs-backup` | bool | `false` | ❌ | Use filesystem backup for volumes by default instead of snapshots |
| `--render` | bool | `false` | ❌ | Render the backup object to STDOUT instead of creating it |
| `--included-resources` | []string | (see below) | ❌ | Comma-separated list of resources to include (overrides defaults) |

#### Flag Details

**`--hc-name` and `--hc-namespace`**
These identify the hosted cluster to backup. The namespace typically follows the pattern where control plane resources are in `{hc-namespace}-{hc-name}`.

**`--storage-location`**
Must match a storage location configured in your DataProtectionApplication. Common values:
- `default` - Default storage location
- `aws-s3` - AWS S3 bucket
- `gcp-gcs` - Google Cloud Storage
- `azure-blob` - Azure Blob Storage

**`--ttl`**
Supports Kubernetes duration format:
- `30m` - 30 minutes
- `2h` - 2 hours
- `24h` - 24 hours
- `7d` - 7 days

**`--snapshot-move-data`**
When enabled, moves snapshot data to object storage for long-term retention. Useful for cross-region/cross-cloud disaster recovery.

**`--included-resources`**
Accepts comma-separated resource types. See [Resource Types](#resource-types) section for complete list.

### Example Commands

#### Basic backup with defaults
```bash
hypershift create oadp-backup \
  --hc-name prod01 \
  --hc-namespace hcp01
```

#### Backup with custom settings
```bash
hypershift create oadp-backup \
  --hc-name prod01 \
  --hc-namespace hcp01 \
  --oadp-namespace custom-oadp \
  --storage-location s3-backup \
  --ttl 24h0m0s \
  --snapshot-move-data=false
```

#### Render backup object without creating it
```bash
hypershift create oadp-backup \
  --hc-name prod01 \
  --hc-namespace hcp01 \
  --render
```

This will output the backup YAML to STDOUT, allowing you to inspect or pipe it to other commands:
```bash
# Save to file
hypershift create oadp-backup --hc-name prod --hc-namespace hcp01 --render > backup.yaml

# Apply with kubectl
hypershift create oadp-backup --hc-name prod --hc-namespace hcp01 --render | kubectl apply -f -
```

#### Backup with custom resource selection
```bash
hypershift create oadp-backup \
  --hc-name prod01 \
  --hc-namespace hcp01 \
  --included-resources configmaps,secrets,hostedclusters.hypershift.openshift.io,nodepools.hypershift.openshift.io
```

This will only backup the specified resources instead of the default comprehensive list.

### What Gets Backed Up

By default, the backup includes the following resources. The exact set of resources depends on the platform type of your HostedCluster, which is automatically detected.

**Core Kubernetes Resources (always included):**
- ServiceAccounts (`serviceaccounts`), Roles (`roles`), RoleBindings (`rolebindings`)
- Pods (`pods`), PVCs (`persistentvolumeclaims`), PVs (`persistentvolumes`), ConfigMaps (`configmaps`)
- Secrets (`secrets`), Services (`services`), Deployments (`deployments`), StatefulSets (`statefulsets`)
- PriorityClasses (`priorityclasses`), PodDisruptionBudgets (`poddisruptionbudgets`)

**HyperShift Specific resources (always included):**
- HostedCluster (`hostedclusters.hypershift.openshift.io`)
- HostedControlPlane (`hostedcontrolplanes.hypershift.openshift.io`)
- NodePool (`nodepools.hypershift.openshift.io`)

**Cluster API Resources (always included):**
- MachineDeployment (`machinedeployments.cluster.x-k8s.io`), MachineSet (`machinesets.cluster.x-k8s.io`), Machine (`machines.cluster.x-k8s.io`)
- Generic cluster resources (`clusters.cluster.x-k8s.io`)

**Additional Resources (always included):**
- Routes (`routes.route.openshift.io`), ClusterDeployments (`clusterdeployments.hive.openshift.io`)

**Platform-Specific Resources (automatically detected):**

The backup command automatically detects your HostedCluster's platform and includes the appropriate platform-specific resources:

- **AWS Platform**: AWSCluster (`awsclusters.infrastructure.cluster.x-k8s.io`), AWSMachineTemplate (`awsmachinetemplates.infrastructure.cluster.x-k8s.io`), AWSMachine (`awsmachines.infrastructure.cluster.x-k8s.io`)
- **Agent Platform**: AgentCluster (`agentclusters.infrastructure.cluster.x-k8s.io`), AgentMachineTemplate (`agentmachinetemplates.infrastructure.cluster.x-k8s.io`), AgentMachine (`agentmachines.infrastructure.cluster.x-k8s.io`)
- **KubeVirt Platform**: KubevirtCluster (`kubevirtclusters.infrastructure.cluster.x-k8s.io`), KubevirtMachineTemplate (`kubevirtmachinetemplates.infrastructure.cluster.x-k8s.io`)
- **OpenStack Platform**: OpenStackClusters (`openstackclusters.infrastructure.cluster.x-k8s.io`), OpenStackMachineTemplates (`openstackmachinetemplates.infrastructure.cluster.x-k8s.io`), OpenStackMachine (`openstackmachines.infrastructure.cluster.x-k8s.io`)
- **Azure Platform**: AzureClusters (`azureclusters.infrastructure.cluster.x-k8s.io`), AzureMachineTemplates (`azuremachinetemplates.infrastructure.cluster.x-k8s.io`), AzureMachine (`azuremachines.infrastructure.cluster.x-k8s.io`)

> **Note**: If the HostedCluster cannot be accessed or the platform type cannot be determined, the command defaults to AWS platform resources.

#### Custom Resource Selection

You can override the default resource list using the `--included-resources` flag:

```bash
# Backup only specific resources
hypershift create oadp-backup \
  --hc-name my-cluster \
  --hc-namespace hcp01 \
  --included-resources hostedclusters.hypershift.openshift.io,nodepools.hypershift.openshift.io,secrets,configmaps
```

When using `--included-resources`, only the specified resources will be included in the backup, completely replacing the default list.

### Resource Types

The following table lists all available resource types for the `--included-resources` flag:

| Category | Resource Type | Description |
|----------|---------------|-------------|
| **Core K8s** | `serviceaccounts` | ServiceAccounts |
| | `roles` | Roles |
| | `rolebindings` | RoleBindings |
| | `pods` | Pods |
| | `persistentvolumeclaims` | PersistentVolumeClaims |
| | `persistentvolumes` | PersistentVolumes |
| | `configmaps` | ConfigMaps |
| | `secrets` | Secrets |
| | `services` | Services |
| | `deployments` | Deployments |
| | `statefulsets` | StatefulSets |
| | `priorityclasses` | PriorityClasses |
| | `poddisruptionbudgets` | PodDisruptionBudgets |
| **HyperShift** | `hostedclusters.hypershift.openshift.io` | HostedCluster resources |
| | `hostedcontrolplanes.hypershift.openshift.io` | HostedControlPlane resources |
| | `nodepools.hypershift.openshift.io` | NodePool resources |
| | `clusters.cluster.x-k8s.io` | Generic cluster resources |
| **AWS Platform** | `awsclusters.infrastructure.cluster.x-k8s.io` | AWSCluster resources |
| | `awsmachinetemplates.infrastructure.cluster.x-k8s.io` | AWSMachineTemplate resources |
| | `awsmachines.infrastructure.cluster.x-k8s.io` | AWSMachine resources |
| **Agent Platform** | `agentclusters.infrastructure.cluster.x-k8s.io` | AgentCluster resources |
| | `agentmachinetemplates.infrastructure.cluster.x-k8s.io` | AgentMachineTemplate resources |
| | `agentmachines.infrastructure.cluster.x-k8s.io` | AgentMachine resources |
| **KubeVirt Platform** | `kubevirtclusters.infrastructure.cluster.x-k8s.io` | KubevirtCluster resources |
| | `kubevirtmachinetemplates.infrastructure.cluster.x-k8s.io` | KubevirtMachineTemplate resources |
| **OpenStack Platform** | `openstackclusters.infrastructure.cluster.x-k8s.io` | OpenStackClusters resources |
| | `openstackmachinetemplates.infrastructure.cluster.x-k8s.io` | OpenStackMachineTemplates resources |
| | `openstackmachines.infrastructure.cluster.x-k8s.io` | OpenStackMachine resources |
| **Azure Platform** | `azureclusters.infrastructure.cluster.x-k8s.io` | AzureClusters resources |
| | `azuremachinetemplates.infrastructure.cluster.x-k8s.io` | AzureMachineTemplates resources |
| | `azuremachines.infrastructure.cluster.x-k8s.io` | AzureMachine resources |
| **Cluster API** | `machinedeployments.cluster.x-k8s.io` | MachineDeployment resources |
| | `machinesets.cluster.x-k8s.io` | MachineSet resources |
| | `machines.cluster.x-k8s.io` | Machine resources |
| **OpenShift** | `routes.route.openshift.io` | OpenShift Routes |
| | `clusterdeployments.hive.openshift.io` | ClusterDeployment resources |

> **Platform Detection**: When using default resources (no `--included-resources` flag), only the platform-specific resources matching your HostedCluster's platform will be included automatically.

### Backup Naming

Backup names are automatically generated using the pattern:
```
{hc-name}-{hc-namespace}-{random-hash}
```

Example: `prod01-hcp01-abc123`

### Validation Process

The command performs the following validations before creating a backup:

1. **HostedCluster Validation**: Verifies that the specified HostedCluster exists and extracts its platform type for platform-specific resource selection
2. **OADP Operator Check**: Verifies that the OADP operator deployment is running
3. **Velero Check**: Confirms that Velero deployment is ready
4. **DPA Status Check**: Ensures at least one DataProtectionApplication is in "Reconciled" state
5. **HyperShift Plugin Check**: Verifies that the 'hypershift' plugin is included in the DPA's defaultPlugins list (shows warning if missing, but doesn't fail)

#### Render Mode Validation

When using the `--render` flag:
- If the cluster is accessible, all validations run normally but failures become warnings
- If the cluster is not accessible, validations are skipped and the backup YAML is still rendered
- Platform detection works in render mode: if the HostedCluster can be accessed, the correct platform-specific resources are included; otherwise, it defaults to AWS platform resources
- This allows you to generate backup manifests without requiring cluster connectivity

### Common Scenarios

#### Scenario 1: Full Cluster Backup
For complete disaster recovery, use the default settings:
```bash
hypershift create oadp-backup \
  --hc-name production \
  --hc-namespace hcp01 \
  --ttl 168h  # 7 days retention
```

#### Scenario 2: Configuration-Only Backup
For quick configuration backups (faster, smaller):
```bash
hypershift create oadp-backup \
  --hc-name production \
  --hc-namespace hcp01 \
  --included-resources hostedclusters.hypershift.openshift.io,hostedcontrolplanes.hypershift.openshift.io,nodepools.hypershift.openshift.io,secrets,configmaps \
  --ttl 72h  # 3 days retention
```

#### Scenario 3: Pre-Upgrade Backup
Before cluster upgrades, create a comprehensive backup:
```bash
hypershift create oadp-backup \
  --hc-name production \
  --hc-namespace hcp01 \
  --storage-location long-term-storage \
  --ttl 720h  # 30 days retention
```

#### Scenario 4: Cross-Region Backup
For cross-region disaster recovery:
```bash
hypershift create oadp-backup \
  --hc-name production \
  --hc-namespace hcp01 \
  --storage-location cross-region-s3 \
  --snapshot-move-data=true \
  --ttl 2160h  # 90 days retention
```

#### Scenario 5: Development Environment
For development hcp01 (minimal backup):
```bash
hypershift create oadp-backup \
  --hc-name dev-cluster \
  --hc-namespace dev \
  --included-resources hostedclusters.hypershift.openshift.io,nodepools.hypershift.openshift.io \
  --ttl 24h  # 1 day retention
```

#### Scenario 6: Platform-Specific Backups
The backup command automatically detects your platform and includes appropriate resources. Here are examples for different platforms:

**AWS HostedCluster:**
```bash
# Automatically includes: awsclusters.infrastructure.cluster.x-k8s.io, awsmachinetemplates.infrastructure.cluster.x-k8s.io, awsmachines.infrastructure.cluster.x-k8s.io
hypershift create oadp-backup \
  --hc-name aws-prod-cluster \
  --hc-namespace hcp01
```

**Agent HostedCluster:**
```bash
# Automatically includes: agentclusters.infrastructure.cluster.x-k8s.io, agentmachinetemplates.infrastructure.cluster.x-k8s.io, agentmachines.infrastructure.cluster.x-k8s.io
hypershift create oadp-backup \
  --hc-name agent-cluster \
  --hc-namespace hcp01
```

**KubeVirt HostedCluster:**
```bash
# Automatically includes: kubevirtclusters.infrastructure.cluster.x-k8s.io, kubevirtmachinetemplates.infrastructure.cluster.x-k8s.io
hypershift create oadp-backup \
  --hc-name kubevirt-cluster \
  --hc-namespace hcp01
```

**Custom platform-specific resource selection:**
```bash
# Force specific platform resources regardless of auto-detection
hypershift create oadp-backup \
  --hc-name my-cluster \
  --hc-namespace hcp01 \
  --included-resources hostedclusters.hypershift.openshift.io,nodepools.hypershift.openshift.io,awsclusters.infrastructure.cluster.x-k8s.io,awsmachines.infrastructure.cluster.x-k8s.io,secrets,configmaps
```

### Best Practices

#### Resource Selection Guidelines

**Minimal Backup (fastest, smallest):**
```bash
--included-resources hostedclusters.hypershift.openshift.io,nodepools.hypershift.openshift.io
```

**Configuration Backup (recommended for most cases):**
```bash
--included-resources hostedclusters.hypershift.openshift.io,hostedcontrolplanes.hypershift.openshift.io,nodepools.hypershift.openshift.io,secrets,configmaps
```

**Full Infrastructure Backup (complete recovery):**
Use default resources (no `--included-resources` flag). This automatically includes platform-specific resources based on your HostedCluster's platform type.

#### TTL Recommendations

| Environment | Recommended TTL | Use Case |
|-------------|-----------------|----------|
| Development | `24h` - `72h` | Quick recovery, frequent changes |
| Staging | `168h` (7 days) | Pre-production testing |
| Production | `720h` (30 days) | Long-term retention |
| Compliance | `2160h` (90 days) | Regulatory requirements |

#### Storage Location Strategy

- **Local backups**: Use `default` for same-region recovery
- **DR backups**: Use separate region/cloud storage locations
- **Compliance**: Use immutable storage locations
- **Development**: Use cost-optimized storage classes

#### Backup Frequency

- **Production**: Daily backups with weekly retention
- **Development**: Weekly or on-demand backups
- **Pre-change**: Always backup before major changes

### Troubleshooting

#### HostedCluster Not Found
```
Error: HostedCluster validation failed: HostedCluster 'my-cluster' not found in namespace 'hcp01'
```
**Solution**: Verify the HostedCluster name and namespace are correct:
```bash
kubectl get hostedcluster -n hcp01
```

#### HostedCluster Platform Detection Failed
```
Error: HostedCluster validation failed: platform type not found in HostedCluster 'my-cluster' spec
```
**Solution**: Check if the HostedCluster has a valid platform configuration:
```bash
kubectl get hostedcluster my-cluster -n hcp01 -o jsonpath='{.spec.platform}'
```

#### Platform Detection in Render Mode
```
Warning: HostedCluster validation failed, using default platform (AWS)
```
**Note**: This is normal behavior in render mode when the HostedCluster cannot be accessed. The backup will be generated with AWS platform resources by default.

#### HyperShift Plugin Missing
```
Warning: HyperShift plugin validation: HyperShift plugin not found in any DataProtectionApplication. Please add 'hypershift' to the defaultPlugins list in your DPA configuration
```
**Solution**: Add the 'hypershift' plugin to your DataProtectionApplication configuration:
```bash
kubectl patch dataprotectionapplication <dpa-name> -n openshift-adp --type='json' \
  -p='[{"op": "add", "path": "/spec/configuration/velero/defaultPlugins/-", "value": "hypershift"}]'
```

Or edit your DPA YAML to include 'hypershift' in the defaultPlugins list:
```yaml
spec:
  configuration:
    velero:
      defaultPlugins:
      - openshift
      - aws
      - csi
      - hypershift  # Add this line
```

#### OADP Operator Not Found
```
Error: OADP validation failed: OADP operator deployment not found in namespace openshift-adp
```
**Solution**: Install the OADP operator or check the correct namespace with `--oadp-namespace`. The command looks for the deployment named `openshift-adp-controller-manager`.

#### No DataProtectionApplication Found
```
Error: DPA verification failed: no DataProtectionApplication resources found in namespace openshift-adp
```
**Solution**: Create a DataProtectionApplication custom resource

#### Velero Not Ready
```
Error: OADP validation failed: Velero deployment is not ready in namespace openshift-adp
```
**Solution**: Check Velero deployment status and logs for issues

#### Invalid Storage Location
```
Error: failed to create backup resource: admission webhook "vbackup.kb.io" denied the request:
backup storage location 'invalid-location' not found
```
**Solution**: Verify storage location exists in your DPA configuration:
```bash
kubectl get backupstoragelocations -n openshift-adp
```

#### Permission Denied
```
Error: failed to create backup resource: admission webhook denied the request:
insufficient permissions to create backup
```
**Solution**: Ensure your user has proper RBAC permissions:
```bash
kubectl auth can-i create backups.velero.io -n openshift-adp
```

#### Invalid Resource Type
```
Error: backup generation failed: invalid resource type 'invalidresource'
```
**Solution**: Check the [Resource Types](#resource-types) table for valid resource names

#### Backup Stuck in Progress
If a backup remains in "InProgress" status:
1. Check backup logs:
   ```bash
   kubectl logs -n openshift-adp deployment/velero
   ```
2. Check backup status:
   ```bash
   kubectl describe backup <backup-name> -n openshift-adp
   ```
3. Common causes:
   - Storage location unreachable
   - Large volume snapshots taking time
   - Network connectivity issues

#### Render Mode Warnings
When using `--render`, you may see warnings like:
```
Warning: Cannot connect to cluster for validation, skipping OADP/DPA checks
```
This is normal behavior when cluster connectivity is not available.

### Monitoring and Verification

#### Check Backup Status
```bash
# List all backups
kubectl get backups -n openshift-adp

# Check specific backup details
kubectl describe backup <backup-name> -n openshift-adp

# View backup logs
kubectl logs -n openshift-adp deployment/velero -f
```

#### Verify Backup Contents
```bash
# Check what was backed up
kubectl get backup <backup-name> -n openshift-adp -o jsonpath='{.status}'
```

#### Monitor Storage Usage
```bash
# Check storage location status
kubectl get backupstoragelocations -n openshift-adp

# View storage usage (if supported by storage provider)
kubectl describe backupstoragelocation default -n openshift-adp
```

### Automation and Scheduling

#### Using CronJobs for Automated Backups

Create a CronJob to automate regular backups:

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: hypershift-backup
  namespace: openshift-adp
spec:
  schedule: "0 2 * * *"  # Daily at 2 AM
  jobTemplate:
    spec:
      template:
        spec:
          serviceAccountName: backup-operator
          containers:
          - name: hypershift-backup
            image: registry.redhat.io/openshift4/ose-cli:latest
            command:
            - /bin/bash
            - -c
            - |
              hypershift create oadp-backup \
                --hc-name production \
                --hc-namespace hcp01 \
                --ttl 168h
          restartPolicy: OnFailure
```

#### GitOps Integration

Use the `--render` flag in GitOps workflows:

```yaml
# .github/workflows/backup.yml
name: Generate Backup Manifests
on:
  schedule:
    - cron: '0 1 * * *'
jobs:
  backup:
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v2
    - name: Generate backup manifest
      run: |
        hypershift create oadp-backup \
          --hc-name ${{ env.CLUSTER_NAME }} \
          --hc-namespace hcp01 \
          --render > manifests/backup-$(date +%Y%m%d).yaml
    - name: Commit manifests
      run: |
        git add manifests/
        git commit -m "Generated backup manifest for $(date)"
        git push
```

### Security Considerations

#### RBAC Requirements

Ensure your service account has the necessary permissions:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: hypershift-backup
rules:
- apiGroups: ["velero.io"]
  resources: ["backups"]
  verbs: ["create", "get", "list", "delete"]
- apiGroups: ["oadp.openshift.io"]
  resources: ["dataprotectionapplications"]
  verbs: ["get", "list"]
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["get", "list"]
```

#### Sensitive Data Handling

- **Secrets**: Backed up by default, ensure your storage is encrypted
- **Certificates**: Included in backup, verify rotation policies
- **Keys**: Consider using external secret management

### Performance Considerations

#### Backup Size Optimization

| Resource Selection | Typical Size | Backup Time | Use Case |
|-------------------|--------------|-------------|-----------|
| Minimal (`hostedcluster,nodepool`) | < 1 MB | < 30s | Quick config backup |
| Configuration | < 10 MB | < 2 min | Standard backup |
| Full (default) | 100 MB - 1 GB | 5-15 min | Complete DR backup |

#### Network and Storage Impact

- **Bandwidth**: Consider backup timing during low-traffic periods
- **Storage costs**: Use appropriate storage classes for retention policies
- **Cross-region**: Factor in data transfer costs and time

### Integration Examples

#### Slack Notifications

```bash
#!/bin/bash
BACKUP_NAME=$(hypershift create oadp-backup --hc-name prod --hc-namespace hcp01 2>&1 | grep "created successfully" | awk '{print $6}')
if [ $? -eq 0 ]; then
  curl -X POST -H 'Content-type: application/json' \
    --data '{"text":"✅ Backup successful: '$BACKUP_NAME'"}' \
    $SLACK_WEBHOOK_URL
else
  curl -X POST -H 'Content-type: application/json' \
    --data '{"text":"❌ Backup failed for prod cluster"}' \
    $SLACK_WEBHOOK_URL
fi
```

#### Prometheus Metrics

Monitor backup success/failure with custom metrics:

```bash
# Success metric
echo "hypershift_backup_success{cluster=\"prod\"} 1" | curl -X POST --data-binary @- http://pushgateway:9091/metrics/job/hypershift_backup

# Failure metric
echo "hypershift_backup_success{cluster=\"prod\"} 0" | curl -X POST --data-binary @- http://pushgateway:9091/metrics/job/hypershift_backup
```

## Restore

The `hypershift create oadp-restore` command creates restore operations from hosted cluster backups using OADP (OpenShift API for Data Protection).

### Prerequisites

Before restoring from backups, ensure that:

1. **OADP Operator is installed**: The OADP operator must be installed and running in your management cluster
2. **DataProtectionApplication (DPA) exists**: A DPA custom resource must be configured and ready
3. **Backup or Schedule exists**: Either a completed backup or a backup schedule must be available
4. **Storage location configured**: The backup storage location must be accessible

### Basic Usage

#### Restore from a specific backup
```bash
hypershift create oadp-restore \
  --hc-name my-hosted-cluster \
  --hc-namespace my-hosted-cluster-namespace \
  --from-backup my-backup-name
```

#### Restore from a schedule (uses latest backup)
```bash
hypershift create oadp-restore \
  --hc-name my-hosted-cluster \
  --hc-namespace my-hosted-cluster-namespace \
  --from-schedule daily-backup-schedule
```

### Command Options

| Flag | Type | Default | Required | Description |
|------|------|---------|----------|-------------|
| `--hc-name` | string | - | ✅ | Name of the hosted cluster to restore |
| `--hc-namespace` | string | - | ✅ | Namespace of the hosted cluster to restore |
| `--from-backup` | string | - | ⚠️ | Name of the backup to restore from (mutually exclusive with `--from-schedule`) |
| `--from-schedule` | string | - | ⚠️ | Name of the schedule to restore from (uses latest backup, mutually exclusive with `--from-backup`) |
| `--name` | string | - | ❌ | Custom name for the restore (auto-generated if not specified) |
| `--oadp-namespace` | string | `openshift-adp` | ❌ | Namespace where OADP operator is installed |
| `--existing-resource-policy` | string | `update` | ❌ | Policy for handling existing resources (`none`, `update`) |
| `--include-namespaces` | []string | (see below) | ❌ | Override included namespaces (replaces defaults completely) |
| `--render` | bool | `false` | ❌ | Render the restore object to STDOUT instead of creating it |
| `--restore-pvs` | bool | `true` | ❌ | Restore persistent volumes |
| `--preserve-node-ports` | bool | `true` | ❌ | Preserve NodePort assignments during restore |

> **Note**: Either `--from-backup` OR `--from-schedule` must be specified, but not both.

#### Flag Details

**`--hc-name` and `--hc-namespace`**
These identify the hosted cluster to restore. These should match the original cluster that was backed up.

**`--from-backup` vs `--from-schedule`**
- `--from-backup`: Restores from a specific backup by name. The backup must exist and be in "Completed" status.
- `--from-schedule`: Restores from the latest successful backup created by the specified schedule. Velero automatically selects the most recent backup.

**`--existing-resource-policy`**
Controls how to handle resources that already exist in the target cluster:
- `update` (default): Update existing resources with backup data
- `none`: Skip existing resources, only restore missing ones

**`--include-namespaces`**
By default, restores include:
- `{hc-namespace}` - The hosted cluster's namespace
- `{hc-namespace}-{hc-name}` - The control plane namespace

When specified, this flag **completely overrides** the default namespaces (it doesn't add to them).

**`--name`**
Specifies a custom name for the restore resource. If not provided, a name is automatically generated using the pattern `{source-name}-{hc-name}-restore-{random-suffix}`.

**`--render`**
Outputs the restore YAML to STDOUT instead of creating the resource. Useful for inspection or GitOps workflows.

### Example Commands

#### Basic restore from backup
```bash
hypershift create oadp-restore \
  --hc-name prod01 \
  --hc-namespace hcp01 \
  --from-backup prod01-hcp01-abc123
```

#### Restore from schedule (latest backup)
```bash
hypershift create oadp-restore \
  --hc-name prod01 \
  --hc-namespace hcp01 \
  --from-schedule daily-prod-backup
```

#### Restore with custom settings
```bash
hypershift create oadp-restore \
  --hc-name prod01 \
  --hc-namespace hcp01 \
  --from-backup prod01-hcp01-abc123 \
  --existing-resource-policy none \
  --restore-pvs=false \
  --preserve-node-ports=false
```

#### Restore to custom namespaces
```bash
# Override default namespaces completely
hypershift create oadp-restore \
  --hc-name prod01 \
  --hc-namespace hcp01 \
  --from-backup prod01-hcp01-abc123 \
  --include-namespaces hcp01,hcp01-prod01,custom-namespace
```

#### Restore with custom name
```bash
hypershift create oadp-restore \
  --hc-name prod01 \
  --hc-namespace hcp01 \
  --from-backup prod01-hcp01-abc123 \
  --name my-custom-restore-name
```

#### Render restore object without creating it
```bash
hypershift create oadp-restore \
  --hc-name prod01 \
  --hc-namespace hcp01 \
  --from-backup prod01-hcp01-abc123 \
  --render
```

This outputs the restore YAML to STDOUT:
```bash
# Save to file
hypershift create oadp-restore --hc-name prod01 --hc-namespace hcp01 --from-backup backup-123 --render > restore.yaml

# Apply with kubectl
hypershift create oadp-restore --hc-name prod01 --hc-namespace hcp01 --from-backup backup-123 --render | kubectl apply -f -
```

### What Gets Restored

The restore process includes the following by default:

**Included Namespaces:**
- `{hc-namespace}` - Contains the HostedCluster resource and related objects
- `{hc-namespace}-{hc-name}` - Contains the HostedControlPlane and control plane components

**Excluded Resources (automatically):**
For safety and compatibility, these resources are always excluded from restore:
- `nodes` - Cluster nodes (managed by the platform)
- `events` - Kubernetes events (ephemeral)
- `events.events.k8s.io` - Extended events
- `backups.velero.io` - Velero backup resources
- `restores.velero.io` - Velero restore resources
- `resticrepositories.velero.io` - Velero repository data
- `csinodes.storage.k8s.io` - CSI node information
- `volumeattachments.storage.k8s.io` - Volume attachment state
- `backuprepositories.velero.io` - Backup repository information

### Restore Naming

#### Automatic Name Generation

When `--name` is not specified, restore names are automatically generated using the pattern:
```
{source-name}-{hc-name}-restore-{random-hash}
```

Examples:
- From backup: `prod01-hcp01-abc123-production-restore-def456`
- From schedule: `daily-backup-production-restore-ghi789`

#### Custom Naming

When using the `--name` flag, the specified name is used exactly as provided:
```bash
# Results in restore named "my-custom-restore"
hypershift create oadp-restore \
  --hc-name prod01 \
  --hc-namespace hcp01 \
  --from-backup backup-123 \
  --name my-custom-restore
```

### Validation Process

The command performs the following validations before creating a restore:

1. **Backup/Schedule Validation**:
   - **From backup**: Verifies the backup exists and is in "Completed" status
   - **From schedule**: Verifies the schedule exists and is not paused
2. **OADP Operator Check**: Verifies that the OADP operator deployment is running
3. **Velero Check**: Confirms that Velero deployment is ready

#### Render Mode Validation

When using the `--render` flag:
- If the cluster is accessible, all validations run normally but failures become warnings
- If the cluster is not accessible, validations are skipped and the restore YAML is still rendered
- This allows you to generate restore manifests without requiring cluster connectivity

### Restore Scenarios

#### Scenario 1: Full Disaster Recovery
Complete restore from backup after cluster loss:
```bash
hypershift create oadp-restore \
  --hc-name production \
  --hc-namespace hcp01 \
  --from-backup production-hcp01-20241022-abc123
```

#### Scenario 2: Schedule-Based Recovery
Restore using the latest backup from a schedule:
```bash
hypershift create oadp-restore \
  --hc-name production \
  --hc-namespace hcp01 \
  --from-schedule daily-production-backup
```

#### Scenario 3: Selective Namespace Restore
Restore only specific namespaces:
```bash
hypershift create oadp-restore \
  --hc-name production \
  --hc-namespace hcp01 \
  --from-backup production-backup-abc123 \
  --include-namespaces hcp01,app-namespace-1,app-namespace-2
```

#### Scenario 4: Configuration-Only Restore
Restore without persistent volumes (faster):
```bash
hypershift create oadp-restore \
  --hc-name production \
  --hc-namespace hcp01 \
  --from-backup config-backup-abc123 \
  --restore-pvs=false
```

#### Scenario 5: Non-Destructive Restore
Restore only missing resources, don't update existing ones:
```bash
hypershift create oadp-restore \
  --hc-name production \
  --hc-namespace hcp01 \
  --from-backup production-backup-abc123 \
  --existing-resource-policy none
```

### Best Practices

#### Pre-Restore Checklist

1. **Verify backup status**:
   ```bash
   kubectl get backup <backup-name> -n openshift-adp -o jsonpath='{.status.phase}'
   ```

2. **Check target namespace availability**:
   ```bash
   kubectl get namespace <hc-namespace>
   ```

3. **Ensure sufficient resources**: Verify the target cluster has adequate CPU, memory, and storage

4. **Review existing resources**: Decide on `--existing-resource-policy` based on what's already present

#### Resource Policy Guidelines

| Scenario | Recommended Policy | Result |
|----------|-------------------|--------|
| **Clean cluster** | `update` (default) | All resources restored |
| **Partial restore** | `none` | Only missing resources restored |
| **Force overwrite** | `update` | Existing resources updated |
| **Incremental restore** | `none` | Preserves local changes |

#### Namespace Strategy

**Default behavior** (recommended for most cases):
```bash
# Restores to: hcp01, hcp01-production
hypershift create oadp-restore --hc-name production --hc-namespace hcp01 --from-backup backup-123
```

**Custom namespace mapping**:
```bash
# Restores to: new-hcp, new-hcp-prod, additional-ns
hypershift create oadp-restore \
  --hc-name production \
  --hc-namespace hcp01 \
  --from-backup backup-123 \
  --include-namespaces new-hcp,new-hcp-prod,additional-ns
```

#### Schedule vs Backup Selection

| Use Case | Recommendation | Command |
|----------|----------------|---------|
| **Latest data** | Use schedule | `--from-schedule daily-backup` |
| **Specific point-in-time** | Use backup | `--from-backup backup-20241022-123` |
| **Automated recovery** | Use schedule | `--from-schedule <schedule-name>` |
| **Manual/precise recovery** | Use backup | `--from-backup <specific-backup>` |

#### Naming Strategy

**Automatic naming** (recommended for most cases):
```bash
# Results in: backup-name-cluster-name-restore-abc123
hypershift create oadp-restore --hc-name cluster-name --hc-namespace hcp01 --from-backup backup-name
```

**Custom naming** (useful for tracking or automation):
```bash
# For DR scenarios
--name "prod-cluster-dr-$(date +%Y%m%d)"

# For testing
--name "test-restore-$(git rev-parse --short HEAD)"

# For scheduled restores
--name "weekly-restore-week-$(date +%U)"
```

### Troubleshooting

#### Backup Not Found
```
Error: backup validation failed: backup 'my-backup' not found in namespace 'openshift-adp'
```
**Solution**: Verify backup exists:
```bash
kubectl get backups -n openshift-adp | grep my-backup
```

#### Backup Not Completed
```
Error: backup validation failed: backup 'my-backup' is not completed, current phase: InProgress
```
**Solution**: Wait for backup to complete or use a different backup:
```bash
kubectl get backup my-backup -n openshift-adp -o jsonpath='{.status.phase}'
```

#### Schedule Not Found
```
Error: schedule validation failed: schedule 'daily-backup' not found in namespace 'openshift-adp'
```
**Solution**: Verify schedule exists:
```bash
kubectl get schedules -n openshift-adp | grep daily-backup
```

#### Schedule Paused
```
Error: schedule validation failed: schedule 'daily-backup' is paused
```
**Solution**: Resume the schedule or use a different one:
```bash
kubectl patch schedule daily-backup -n openshift-adp --type='json' -p='[{"op": "replace", "path": "/spec/paused", "value": false}]'
```

#### Mutually Exclusive Flags
```
Error: --from-backup and --from-schedule are mutually exclusive, specify only one
```
**Solution**: Use only one source flag:
```bash
# Either this:
hypershift create oadp-restore --hc-name prod --hc-namespace hcp01 --from-backup backup-123

# Or this:
hypershift create oadp-restore --hc-name prod --hc-namespace hcp01 --from-schedule daily-backup
```

#### Missing Source
```
Error: either --from-backup or --from-schedule must be specified
```
**Solution**: Specify exactly one source:
```bash
hypershift create oadp-restore --hc-name prod --hc-namespace hcp01 --from-backup backup-123
```

#### Invalid Resource Policy
```
Error: invalid existing-resource-policy 'invalid'. Valid values are: none, update
```
**Solution**: Use a valid policy:
```bash
hypershift create oadp-restore \
  --hc-name prod \
  --hc-namespace hcp01 \
  --from-backup backup-123 \
  --existing-resource-policy update  # or 'none'
```

#### Insufficient Permissions
```
Error: failed to create restore resource: admission webhook denied the request
```
**Solution**: Ensure proper RBAC permissions:
```bash
kubectl auth can-i create restores.velero.io -n openshift-adp
```

#### Restore Stuck in Progress
If a restore remains in "InProgress" status:
1. Check restore logs:
   ```bash
   kubectl logs -n openshift-adp deployment/velero
   ```
2. Check restore status:
   ```bash
   kubectl describe restore <restore-name> -n openshift-adp
   ```

### Monitoring and Verification

#### Check Restore Status
```bash
# List all restores
kubectl get restores -n openshift-adp

# Check specific restore details
kubectl describe restore <restore-name> -n openshift-adp

# View restore logs
kubectl logs -n openshift-adp deployment/velero -f
```

#### Verify Restored Resources
```bash
# Check HostedCluster
kubectl get hostedcluster -n <hc-namespace>

# Check HostedControlPlane
kubectl get hostedcontrolplane -n <hc-namespace>-<hc-name>

# Check NodePools
kubectl get nodepool -n <hc-namespace>
```

#### Monitor Restore Progress
```bash
# Watch restore status
kubectl get restore <restore-name> -n openshift-adp -w

# Check restored namespace
kubectl get all -n <hc-namespace>
```

### Automation Examples

#### GitOps Restore Workflow
```yaml
# .github/workflows/restore.yml
name: Disaster Recovery Restore
on:
  workflow_dispatch:
    inputs:
      backup_name:
        description: 'Backup name to restore from'
        required: true
      cluster_name:
        description: 'Cluster name to restore'
        required: true
      cluster_namespace:
        description: 'Cluster namespace'
        required: true
        default: 'hcp01'

jobs:
  restore:
    runs-on: ubuntu-latest
    steps:
    - name: Generate restore manifest
      run: |
        hypershift create oadp-restore \
          --hc-name ${{ github.event.inputs.cluster_name }} \
          --hc-namespace ${{ github.event.inputs.cluster_namespace }} \
          --from-backup ${{ github.event.inputs.backup_name }} \
          --render > restore-manifest.yaml

    - name: Apply restore
      run: |
        kubectl apply -f restore-manifest.yaml
```

#### Automated Schedule-Based Recovery
```bash
#!/bin/bash
# restore-latest.sh - Restore from the latest backup in a schedule

CLUSTER_NAME="$1"
CLUSTER_NAMESPACE="$2"
SCHEDULE_NAME="$3"

if [[ -z "$CLUSTER_NAME" || -z "$CLUSTER_NAMESPACE" || -z "$SCHEDULE_NAME" ]]; then
    echo "Usage: $0 <cluster-name> <cluster-namespace> <schedule-name>"
    exit 1
fi

echo "Starting restore for cluster $CLUSTER_NAME from schedule $SCHEDULE_NAME..."

hypershift create oadp-restore \
  --hc-name "$CLUSTER_NAME" \
  --hc-namespace "$CLUSTER_NAMESPACE" \
  --from-schedule "$SCHEDULE_NAME"

if [[ $? -eq 0 ]]; then
    echo "✅ Restore initiated successfully"
    echo "Monitor progress with: kubectl get restores -n openshift-adp"
else
    echo "❌ Restore failed"
    exit 1
fi
```

### Security Considerations

#### RBAC Requirements
```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: hypershift-restore
rules:
- apiGroups: ["velero.io"]
  resources: ["restores", "backups", "schedules"]
  verbs: ["create", "get", "list"]
- apiGroups: ["oadp.openshift.io"]
  resources: ["dataprotectionapplications"]
  verbs: ["get", "list"]
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["get", "list"]
```

#### Data Security
- **Encryption**: Ensure restored data maintains encryption at rest
- **Access Control**: Verify restored resources have appropriate RBAC
- **Secrets**: Validate that restored secrets are current and secure
- **Certificates**: Check certificate validity after restore

### Performance Considerations

#### Restore Duration Estimates

| Restore Type | Typical Duration | Factors |
|--------------|-----------------|---------|
| **Configuration only** | 1-5 minutes | Cluster size, network |
| **With PVs (small)** | 5-15 minutes | Volume size, storage speed |
| **With PVs (large)** | 15-60 minutes | Volume size, storage speed |
| **Full cluster** | 30-120 minutes | Complete cluster complexity |

#### Optimization Tips
- Use `--restore-pvs=false` for faster configuration-only restores
- Consider `--existing-resource-policy=none` for incremental restores
- Use `--include-namespaces` to restore only required namespaces
- Ensure adequate network bandwidth for cross-region restores

### Testing and Validation

#### Integration Tests

The HyperShift CLI includes comprehensive integration tests for both backup and restore functionality:

```bash
# Run all OADP integration tests
go test -tags integration ./test/integration/oadp/... -v

# Run only backup tests
go test -tags integration ./test/integration/oadp/ -run "TestBackup" -v

# Run only restore tests
go test -tags integration ./test/integration/oadp/ -run "TestRestore" -v
```

The tests validate:
- Manifest generation against Velero CRDs
- CLI configuration and defaults
- Business rules and validation logic
- Platform-specific resource selection
- Namespace configuration

> **Note**: Tests require internet connectivity to download Velero CRDs from GitHub for validation.

### Related Documentation

- [OADP Installation Guide](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/installing/installing-oadp-ocs.html)
- [HyperShift OADP Plugin](https://github.com/openshift/hypershift-oadp-plugin)
- [Velero Documentation](https://velero.io/docs/)
- [HyperShift Documentation](https://hypershift.pages.dev/)
- [OpenShift Backup Best Practices](https://docs.openshift.com/container-platform/latest/backup_and_restore/application_backup_and_restore/backing_up_and_restoring/backing-up-applications.html)