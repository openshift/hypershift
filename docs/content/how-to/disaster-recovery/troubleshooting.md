---
title: Disaster Recovery Troubleshooting
---

# Disaster Recovery Troubleshooting

This page consolidates common issues encountered during HostedCluster backup, restore, and cross-cluster migration, along with their solutions.

## New Workloads Do Not Get Scheduled After Migration

**Symptoms**: Everything looks normal but new workloads do not schedule in the migrated HostedCluster. Eventually pods fail and the cluster becomes degraded.

**Diagnosis**:

1. Check cluster operators:

    ```bash
    oc get co --kubeconfig=<HC_KUBECONFIG>
    ```

2. If operators are degraded, check logs for OVN-related errors:

    ```bash
    oc get co <OPERATOR_NAME> -o yaml --kubeconfig=<HC_KUBECONFIG>
    ```

**Fix**: Ensure the old HostedCluster is paused or deleted, then restart OVN pods:

```bash
oc delete pod -n openshift-ovn-kubernetes --all --kubeconfig=<HC_KUBECONFIG>
```

The HostedCluster will self-heal and ClusterOperators will recover.

**Cause**: After migration, the KAS uses the same DNS name but points to a different Load Balancer. OVN sometimes does not handle this transition correctly.

## Etcd Recovery Gets Blocked

**Symptoms**: You edited the HostedCluster to add `ETCDSnapshotURL` but the modification disappears and does not continue. The HyperShift Operator pod is in `CrashLoopBackOff`.

**Fix**:

1. Kill the HyperShift Operator pod:

    ```bash
    oc delete pod -n hypershift -lapp=operator
    ```

2. Re-edit the HostedCluster to add the `ETCDSnapshotURL`.
3. The etcd pod will start using the snapshot from the S3 bucket.

**Cause**: The HyperShift Operator was down and the HostedCluster controller could not process modifications to HostedCluster objects.

## Nodes Cannot Join the New Cluster (AWS)

### If the HostedCluster Is Paused

Verify the HostedCluster is paused:

```bash
oc get hostedcluster -n <HC_NAMESPACE> <HC_NAME> -o jsonpath='{.spec.pausedUntil}'
```

If this returns no output, the pause was not applied correctly. Re-apply it following the [Cross-cluster Migration](scenarios/cross-cluster-migration.md) procedure.

### If the Old Cluster Is Still Running

The only way to resolve this is to complete the teardown of the old HostedCluster before the full restoration on the new Management cluster. Ensure you have all manifests and etcd backed up first.

Once you follow the teardown procedure, the migrated HostedCluster will begin to self-recover.

**Cause** (AWS-specific): The old HostedCluster has a conflict with the AWSPrivateLink object. The old cluster is still running and the `hypershift.local` internal DNS entry still points to the old LoadBalancer.

## Dependent Resources Block Teardown

**Symptoms**: The old HostedCluster cannot be deleted because dependent resources have active finalizers.

**Fix**: Check all objects in the HostedControlPlane namespace and identify which ones are stuck terminating:

```bash
# List all objects in the namespace to find stuck resources
kubectl get all -n <NAMESPACE>

# Identify objects with active finalizers
kubectl get all -n <NAMESPACE> -o json | jq -r '.items[] | select(.metadata.finalizers != null) | "\(.kind)/\(.metadata.name): \(.metadata.finalizers)"'
```

!!! danger "Last Resort Only"

    The following script removes **all** finalizers from every object in the namespace. This bypasses controller cleanup and can orphan cloud resources. Only use this if you do not care about the stability of the source Management cluster (e.g., it is being decommissioned).

```bash
#!/bin/bash
NAMESPACE=$1

if [[ -z $1 ]]; then
    echo "Specify the Namespace!"
    exit 1
fi

# Using ketall (https://github.com/corneliusweig/ketall)
for object in $(ketall -n $NAMESPACE -o name | grep -v packa); do
    oc -n $NAMESPACE patch $object -p '{"metadata":{"finalizers":null}}' --type merge
done
```

Eventually the namespace and HostedCluster will terminate.

**Cause**: A resource has dependent objects with active finalizers that cannot progress.

## Storage ClusterOperator Reports "Waiting for Deployment" (AWS)

**Symptoms**: After migration, the Storage ClusterOperator keeps reporting it is waiting for a deployment.

**Fix**: Delete the AWS EBS CSI Driver deployments in the HCP namespace on the destination Management cluster:

```bash
oc delete deployment aws-ebs-csi-driver-controller aws-ebs-csi-driver-operator \
  -n <HC_NAMESPACE>-<HC_NAME>
```

The operator will recreate them from scratch.

**Cause**: Objects deployed by the `cluster-storage-operator` are not reconciled correctly after migration.

## Image-Registry ClusterOperator Reports Degraded Status

**Symptoms**: After migration, the image-registry ClusterOperator is degraded with message: `ImagePrunerDegraded: Job has reached the specified backoff limit`.

**Diagnosis**:

1. Look for failed pods in the HostedControlPlane namespace.
2. Check other ClusterOperators in the HostedCluster.
3. Verify nodes are ready.

**Fix**: If all components look healthy, manually trigger a new image pruner job:

```bash
oc create job -n openshift-image-registry --from=cronjob/image-pruner image-pruner-recover \
  --kubeconfig=<HC_KUBECONFIG>
```

Alternatively, wait 24 hours for the CronJob to create a new job automatically.

## OADP Backup/Restore General Issues

### Backup Stuck or Failing

- Review Velero logs: `oc logs -n openshift-adp -ldeploy=velero -f`
- Use `velero backup describe <NAME> --details` for detailed status.
- Check backup/restore status in the object: `oc get backup -n openshift-adp <NAME> -o yaml`
- Check events in the affected namespaces.
- Verify all OADP objects: see the [monitoring commands](methods/oadp.md#watching-backuprestore-progress).

### BackupRepository Issues

If you manually modified the folder structure in the remote storage, `backuprepositories.velero.io` may become inconsistent. In this case, you will need to recreate all associated objects (DPAs, backups, restores).

**Prevention**: Always manage backups through the Velero CLI, not by directly modifying storage.

## AWS-Specific: OIDC Identity Provider Missing After Restore

See [AWS Platform Guide — Fixing OIDC After Restore](platform-guides/aws.md#fixing-oidc-after-restore).
