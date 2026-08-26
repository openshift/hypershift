---
title: Cross-Cluster Migration
---

# Migrating a HostedCluster to a Different Management Cluster

!!! danger "Not Yet Supported"

    This procedure is documented for reference and disaster recovery planning, but it is **not yet officially supported**. Use at your own risk, preferably in non-production environments or as a last-resort measure. See the [Supportability Matrix](../index.md#supportability-matrix) for per-platform status.

This guide covers migrating a HostedCluster from one Management cluster to another. This is the most complex disaster recovery scenario and has strict prerequisites that **must** be in place before the original cluster fails.

## When to Use This Procedure

This procedure is appropriate when:

- The source Management cluster is completely unrecoverable.
- You need to relocate a HostedCluster to a different Management cluster as a planned DR exercise.
- The HyperShift Operator on the source cluster cannot be recovered.

This procedure is **not appropriate** when:

- The Management cluster is still functional — use [Same-cluster Restore](same-cluster-restore.md) instead, it is simpler and supported.
- Only the HostedCluster control plane is down but the Management cluster is healthy — same-cluster restore is sufficient.

## Hard Prerequisites

!!! warning "These must be configured BEFORE the disaster occurs"

    Cross-cluster migration requires advance preparation. If these prerequisites are not in place when the source cluster fails, migration is not possible.

### 1. Fixed Hostnames in Service Publishing Strategy

**All** services in the HostedCluster **must** have fixed hostnames configured. At a minimum, the APIServer requires a fixed hostname. Without it, worker nodes will be unable to rejoin the cluster after migration.

```yaml
spec:
  services:
  - service: APIServer
    servicePublishingStrategy:
      type: LoadBalancer
      loadBalancer:
        hostname: api.example.com
  - service: OAuthServer
    servicePublishingStrategy:
      type: Route
      route:
        hostname: oauth.example.com
  - service: OIDC
    servicePublishingStrategy:
      type: Route
      route:
        hostname: oidc.example.com
  - service: Konnectivity
    servicePublishingStrategy:
      type: Route
      route:
        hostname: konnectivity.example.com
  - service: Ignition
    servicePublishingStrategy:
      type: Route
      route:
        hostname: ignition.example.com
```

See the [Prerequisites](../prerequisites.md#hostedcluster-service-publishing-strategy-requirements) page for a detailed explanation of why this is required.

### 2. DNS Control

You must have the ability to update DNS records to point fixed hostnames to the new Management cluster's endpoints (Load Balancers, Routes).

### 3. Accessible Backup Storage

The backup storage (S3, Azure Blob, MinIO) must be accessible from **both** the source and destination Management clusters.

### 4. OADP Installed on Destination Cluster

The destination Management cluster must have:

- OADP Operator installed.
- A DataProtectionApplication (DPA) configured pointing to the same backup storage location.
- The HyperShift Operator installed and running.

### 5. ExternalDNS Operator (Public / PublicAndPrivate clusters)

If the HostedCluster uses `Public` or `PublicAndPrivate` endpoint access, the destination Management cluster must have the ExternalDNS Operator configured with the same domain.

## What Happens During Cross-Cluster Migration

Understanding what changes and what is preserved helps set expectations:

| Aspect | What Happens |
| -------- | ------------- |
| **Infrastructure endpoints** | New Load Balancers / Routes are created on the destination cluster. DNS must be updated to point to them. |
| **Worker nodes** | Reprovisioned on most platforms. See [Supportability Matrix](../index.md#supportability-matrix) for node readoption support. |
| **Etcd data** | Restored from backup (volume snapshot or etcd snapshot). |
| **TLS certificates** | Fixed hostnames ensure SANs remain valid. Ephemeral endpoints would cause certificate validation failures. |
| **Data Plane workloads** | Preserved in etcd. Running workloads on existing nodes continue until nodes are drained/replaced. |
| **Control plane pods** | Recreated on the destination Management cluster. |

## Procedure

!!! tip "Proactive Backup Recommended"

    If the source Management cluster is **already unavailable**, skip Phase 1 and go directly to [Phase 2: Restore](#phase-2-restore-on-destination-management-cluster). You must have a pre-existing backup available in the shared storage location.

    For disaster preparedness, create periodic backups **before** a failure occurs using `hypershift create oadp-schedule --hc-name <HC_NAME> --hc-namespace <HC_NAMESPACE> --schedule "0 */6 * * *" --ttl 720h`. The default TTL is 2 hours, which is too short for DR scenarios — set a TTL that covers your disaster recovery window.

### Phase 1: Backup (on Source Management Cluster)

#### Step 1: Create the Backup

**Using the HyperShift CLI (recommended):**

```bash
hypershift create oadp-backup \
  --hc-name <HC_NAME> \
  --hc-namespace <HC_NAMESPACE>
```

**Or using a manual Velero Backup manifest** — see the [OADP method reference](../methods/oadp.md) for platform-specific backup manifests.

#### Step 2: Verify Backup Completion

```bash
watch "oc get backup -n openshift-adp <BACKUP_NAME> -o jsonpath='{.status.phase}'"
```

Wait until the phase is `Completed`.

#### Step 3: Clean Up Routes (Public/PublicAndPrivate only)

For clusters with `Public` or `PublicAndPrivate` endpoint access, delete the control plane routes so the ExternalDNS Operator removes the DNS records from the source cluster:

```bash
oc delete routes -n <HC_NAMESPACE>-<HC_NAME> --all
```

Wait for DNS records to be cleaned up before proceeding. You can verify with:

```bash
# For AWS
aws route53 list-resource-record-sets --hosted-zone-id <ZONE_ID> \
  --output json | grep -c <EXTERNAL_DNS_DOMAIN>
```

The count should drop to the baseline (typically 2 SOA/NS records).

### Phase 2: Restore (on Destination Management Cluster)

#### Step 1: Prepare the Destination Cluster

```bash
export KUBECONFIG=<DEST_MGMT_KUBECONFIG>

# Ensure OADP is installed and DPA is configured
oc get dpa -n openshift-adp

# Verify the backup is accessible from the destination cluster
oc get backup -n openshift-adp
```

!!! note

    If the backup does not appear, verify that the DPA on the destination cluster points to the same BackupStorageLocation as the source cluster.

#### Step 2: Create the Restore

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

#### Step 3: Monitor the Restore

```bash
watch "oc get restore -n openshift-adp <HC_NAME>-restore -o jsonpath='{.status}' | jq"
oc logs -n openshift-adp -ldeploy=velero -f
```

#### Step 4: Update DNS Records

After the restore creates new infrastructure endpoints on the destination cluster, update your DNS records to point the fixed hostnames to the new endpoints:

1. Get the new Load Balancer / Route addresses from the destination cluster.
2. Update DNS records for each fixed hostname (APIServer, OAuthServer, OIDC, Konnectivity, Ignition).

#### Step 5: Platform-Specific Post-Restore Actions

| Platform | Action Required |
| ---------- | ---------------- |
| **AWS** | Run `hypershift fix dr-oidc-iam` to fix OIDC Identity Provider. See [AWS Platform Guide](../platform-guides/aws.md#fixing-oidc-after-restore). |
| **Azure** | Verify Workload Identity configuration. See [Azure Platform Guide](../platform-guides/azure.md). |
| **Agent / Bare Metal** | Ensure InfraEnv and Assisted Installer DB are restored. Configure node migration strategy beforehand. See [Agent Platform Guide](../platform-guides/agent.md#cross-cluster-considerations). |
| **KubeVirt** | See [KubeVirt Platform Guide](../platform-guides/kubevirt.md). |
| **OpenStack** | See [OpenStack Platform Guide](../platform-guides/openstack.md). |

### Phase 3: Teardown (on Source Management Cluster)

!!! important

    Only perform teardown after verifying the HostedCluster is fully operational on the destination cluster. If the source cluster is already unavailable, skip this phase.

#### Step 1: Pause the HostedCluster on the Source Cluster

Pause the HostedCluster and NodePools to prevent the source and destination control planes from competing for the same resources:

```bash
export KUBECONFIG=<SOURCE_MGMT_KUBECONFIG>

# Pause the HostedCluster
oc patch -n <HC_NAMESPACE> hostedclusters/<HC_NAME> \
  -p '{"spec":{"pausedUntil":"true"}}' --type=merge

# Pause all NodePools
for np in $(oc get nodepools -n <HC_NAMESPACE> -o jsonpath='{.items[?(@.spec.clusterName=="<HC_NAME>")].metadata.name}'); do
    oc patch -n <HC_NAMESPACE> nodepools/${np} \
      -p '{"spec":{"pausedUntil":"true"}}' --type=merge
done
```

#### Step 2: Scale Down and Delete Resources

```bash
# Scale down everything in the control plane namespace
oc scale deployment -n <HC_NAMESPACE>-<HC_NAME> --replicas=0 --all
oc scale statefulset -n <HC_NAMESPACE>-<HC_NAME> --replicas=0 --all
sleep 15

# Remove finalizers and delete NodePools
NODEPOOLS=$(oc get nodepools -n <HC_NAMESPACE> -o jsonpath='{.items[?(@.spec.clusterName=="<HC_NAME>")].metadata.name}')
for np in ${NODEPOOLS}; do
    oc patch -n <HC_NAMESPACE> nodepool ${np} \
      --type=json --patch='[{"op":"remove","path":"/metadata/finalizers"}]' || true
    oc delete nodepool -n <HC_NAMESPACE> ${np} || true
done

# Remove finalizers and delete Machines
for m in $(oc get machines -n <HC_NAMESPACE>-<HC_NAME> -o name); do
    oc patch -n <HC_NAMESPACE>-<HC_NAME> ${m} \
      --type=json --patch='[{"op":"remove","path":"/metadata/finalizers"}]' || true
    oc delete -n <HC_NAMESPACE>-<HC_NAME> ${m} || true
done

# Delete the HostedControlPlane
oc patch -n <HC_NAMESPACE>-<HC_NAME> hostedcontrolplane <HC_NAME> \
  --type=json --patch='[{"op":"remove","path":"/metadata/finalizers"}]'
oc delete hostedcontrolplane -n <HC_NAMESPACE>-<HC_NAME> --all

# Delete the HostedCluster
oc -n <HC_NAMESPACE> patch hostedclusters <HC_NAME> \
  -p '{"metadata":{"finalizers":null}}' --type merge || true
oc delete hostedcluster -n <HC_NAMESPACE> <HC_NAME> || true

# Clean up namespaces
oc delete ns <HC_NAMESPACE>-<HC_NAME> || true
```

### Phase 4: Verification

On the destination Management cluster:

```bash
export KUBECONFIG=<DEST_MGMT_KUBECONFIG>

# Verify HostedCluster
oc get hostedcluster -n <HC_NAMESPACE>
oc get nodepool -n <HC_NAMESPACE>
oc get pods -n <HC_NAMESPACE>-<HC_NAME>

# Verify the HostedCluster is accessible
oc get clusterversion --kubeconfig=<HC_KUBECONFIG>
oc get nodes --kubeconfig=<HC_KUBECONFIG>
oc get co --kubeconfig=<HC_KUBECONFIG>
```

For Public/PublicAndPrivate clusters, you may need to restart OVN pods after teardown of the source cluster:

```bash
oc delete pod -n openshift-ovn-kubernetes --all --kubeconfig=<HC_KUBECONFIG>
```

See the [Troubleshooting Guide](../troubleshooting.md) for common issues.

## Troubleshooting

See the [Troubleshooting Guide](../troubleshooting.md) for common cross-cluster migration issues including:

- OVN connectivity issues after migration
- Etcd recovery getting blocked
- Nodes unable to join the new cluster
- Dependent resources blocking teardown
- Storage ClusterOperator reporting issues
