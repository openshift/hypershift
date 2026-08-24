---
title: Same-Cluster Restore
---

# Restoring a HostedCluster on the Same Management Cluster

This guide covers restoring a HostedCluster on the **same** Management cluster where the backup was originally taken. This is the simplest and most common disaster recovery scenario.

## When to Use This Procedure

This procedure is appropriate when:

- The HostedCluster control plane is down (KAS, etcd, or other critical components).
- Etcd data is corrupted or lost.
- A HostedCluster was accidentally deleted.
- The HyperShift Operator experienced a failure that corrupted HostedCluster state.

This procedure is **not appropriate** when:

- The Management cluster itself is unrecoverable — use [Cross-cluster Migration](cross-cluster-migration.md) instead.
- Only a single etcd member is lost — use [Etcd Recovery](../etcd-recovery.md) instead (automatic recovery may handle this).
- Worker nodes are frozen or unresponsive — this is a node-level issue, not a control plane DR scenario.

## Prerequisites

Review the [Disaster Recovery Prerequisites](../prerequisites.md) before proceeding. For same-cluster restore, the key requirements are:

- Cluster-admin access to the Management cluster.
- A valid backup exists (created via OADP volume snapshot, etcd snapshot, or manual etcd backup).
- OADP Operator installed and DataProtectionApplication (DPA) configured.
- A valid StorageClass configured in the Management cluster.

!!! note "This page covers the OADP volume-snapshot restore flow"

    The procedure below uses the default OADP volume-snapshot restore method. For other backup methods, see:

    - **Etcd Snapshot restore**: use `hypershift create oadp-restore --from-backup <BACKUP_NAME> --use-etcd-snapshot` or see the [Etcd Snapshot Restore Flow](../methods/etcd-snapshot/restore-flow.md).
    - **Manual etcd restore**: see [Manual Etcd Backup and Restore](../methods/manual-etcd-backup.md#restoring-an-etcd-snapshot) (restore is done at cluster creation time via `restoreSnapshotURL`).

!!! note "Fixed hostnames are NOT required for same-cluster restore"

    Unlike cross-cluster migration, same-cluster restore does not require fixed hostnames in the `servicePublishingStrategy`. The infrastructure endpoints (Load Balancers, Routes) are recreated on the same cluster and will be reassigned.

## What Happens During Restore

When restoring on the same Management cluster:

1. **Infrastructure endpoints are preserved**: Load Balancers and Routes are recreated in the same cloud environment, so DNS records and existing node configurations remain valid.
2. **Worker nodes are reprovisioned**: Existing worker nodes are terminated and new ones are created by the NodePool controller. See the [Supportability Matrix](../index.md#supportability-matrix) for node readoption support per platform.
3. **Etcd data is restored**: From the backup (volume snapshot, etcd snapshot, or manual backup depending on the method used).
4. **Control plane pods restart**: The HostedControlPlane components are recreated from the restored state.

## Procedure

### Step 1: Verify the Backup Exists

Confirm your backup is available and in a `Completed` state:

```bash
oc get backup -n openshift-adp
```

Verify the backup details:

```bash
alias velero='oc -n openshift-adp exec deployment/velero -c velero -it -- ./velero'
velero backup describe <BACKUP_NAME> --details
```

### Step 2: Clean Up the Existing HostedCluster

Before restoring, remove the existing HostedCluster objects to avoid conflicts with immutable fields:

```bash
# Delete the HostedCluster (this cascades to the control plane namespace)
oc delete hostedcluster -n <HC_NAMESPACE> <HC_NAME>

# Delete NodePools
oc delete nodepool -n <HC_NAMESPACE> --all

# Wait for the control plane namespace to be fully terminated
oc wait --for=delete namespace/<HC_NAMESPACE>-<HC_NAME> --timeout=300s
```

!!! important "Platform-Specific Cleanup"

    - **Agent / Bare Metal**: Do **not** delete the InfraEnv object or its namespace. The InfraEnv has a separate lifecycle and is required for node reprovisioning. See the [Agent Platform Guide](../platform-guides/agent.md).
    - **KubeVirt**: KubeVirt VMs will be automatically recreated after restore. No special cleanup is needed for VMs.

### Step 3: Create the Restore

Apply the restore manifest:

```yaml
apiVersion: velero.io/v1
kind: Restore
metadata:
  name: <HC_NAME>-restore
  namespace: openshift-adp
spec:
  backupName: <BACKUP_NAME>
  restorePVs: true
  existingResourcePolicy: update
  excludedResources:
  - nodes
  - events
  - events.events.k8s.io
  - backups.velero.io
  - restores.velero.io
  - resticrepositories.velero.io
  - csinodes.storage.k8s.io
  - volumeattachments.storage.k8s.io
  - backuprepositories.velero.io
```

Key fields:

- `restorePVs: true` — Initiates recovery of persistent volumes (etcd data).
- `existingResourcePolicy: update` — Overwrites any existing objects with backup content.

### Step 4: Monitor the Restore

Watch the restore progress:

```bash
watch "oc get restore -n openshift-adp <HC_NAME>-restore -o jsonpath='{.status}' | jq"
```

Follow Velero logs for detailed progress:

```bash
oc logs -n openshift-adp -ldeploy=velero -f
```

The restore is complete when `status.phase` is `Completed`.

### Step 5: Verify the Restored Cluster

```bash
# Check HostedCluster status
oc get hostedcluster -n <HC_NAMESPACE> <HC_NAME>

# Check control plane pods
oc get pods -n <HC_NAMESPACE>-<HC_NAME>

# Check NodePool status
oc get nodepool -n <HC_NAMESPACE>

# Verify the HostedCluster is accessible
oc get nodes --kubeconfig=<HC_KUBECONFIG>
oc get clusterversion --kubeconfig=<HC_KUBECONFIG>
```

### Step 6: Platform-Specific Post-Restore Actions

Depending on your platform, additional steps may be required after restore:

| Platform | Post-Restore Action | Details |
| ---------- | --------------------- | --------- |
| **AWS** | Fix OIDC Identity Provider | Run `hypershift fix dr-oidc-iam`. See [AWS Platform Guide](../platform-guides/aws.md#fixing-oidc-after-restore). |
| **Azure** | No additional steps | — |
| **Agent / Bare Metal** | Verify InfraEnv and agents | Ensure agents can reach the InfraEnv Discovery ISO. See [Agent Platform Guide](../platform-guides/agent.md). |
| **KubeVirt** | Wait for VM recreation | VMs are automatically recreated. See [KubeVirt Platform Guide](../platform-guides/kubevirt.md). |
| **OpenStack** | No additional steps | — |

## Using the HyperShift CLI

Instead of manually creating backup and restore manifests, you can use the HyperShift CLI:

```bash
# Create a named backup
hypershift create oadp-backup \
  --hc-name <HC_NAME> \
  --hc-namespace <HC_NAMESPACE> \
  --name <BACKUP_NAME>

# Create a restore from that backup (after cleanup)
hypershift create oadp-restore \
  --hc-name <HC_NAME> \
  --hc-namespace <HC_NAMESPACE> \
  --from-backup <BACKUP_NAME>
```

See the [DR CLI reference](../dr-cli.md) for all available options.

## Troubleshooting

If issues arise during or after restore, see the [Troubleshooting Guide](../troubleshooting.md).
