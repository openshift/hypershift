# Etcd Recovery

### Overview

Etcd pods for hosted clusters run as part of a statefulset (etcd). The statefulset relies on persistent storage to store etcd data per member. In the case of a HighlyAvailable control plane, the size of the statefulset is 3 and each member (etcd-N) has its own PersistentVolumeClaim (data-etcd-N).

### Automatic Recovery of Removed Members

In certain circumstances, an etcd member is removed from the cluster. This could be due to networking issues (sdn or dns of management cluster). The hypershift operator can automatically recover from this situation by enabling automatic etcd recovery (--enable-etcd-recovery), which is set to true by default.

If this is enabled, then the HyperShift operator will attempt to recover the health of an etcd cluster if the following conditions are met:

* The hosted cluster is configured to run HighlyAvailable (`spec.controllerAvailabilityPolicy = HighlyAvailable`)
* Etcd is managed by HyperShift (`spec.etcd.managementType = Managed`)
* Only one member of the etcd cluster is failing (quorum is not lost)

The recovery procedure consists of the following:
* If a member has been removed from the etcd cluster, re-add the missing member by executing the `member add` command
* The administrator should [delete the etcd member's pod and pvc](#single-node-recovery), after which the HyperShift operator will automatically provision a replacement etcd member Pod and PersistentVolume.

If the management-cluster node hosting an etcd pod becomes unreachable, the recovery controller automatically detects this and force-deletes the stuck pod and PVC. Recovery begins approximately 5 minutes after the node goes down, which avoids reacting to transient disruptions.

Once this is done, the `reset-member` init container of the removed pod should be able to complete the recovery.

To disable this default behavior, install HyperShift with `--enable-etcd-recovery=false`

### Checking cluster health

Execute into a running etcd pod:

```
$ oc rsh -n ${CONTROL_PLANE_NAMESPACE} -c etcd etcd-0
```

Setup the etcdctl environment:

```
export ETCDCTL_API=3
export ETCDCTL_CACERT=/etc/etcd/tls/etcd-ca/ca.crt
export ETCDCTL_CERT=/etc/etcd/tls/client/etcd-client.crt
export ETCDCTL_KEY=/etc/etcd/tls/client/etcd-client.key
export ETCDCTL_ENDPOINTS=https://etcd-client:2379
```

Print out endpoint health for each cluster member:
```
etcdctl endpoint health --cluster -w table
```

### Single Node Recovery

If a single etcd member of a 3-node cluster has corrupted data, it will most likely start crash looping, as in:

```
$ oc get pods -l app=etcd -n ${CONTROL_PLANE_NAMESPACE}
NAME     READY   STATUS             RESTARTS     AGE
etcd-0   2/2     Running            0            64m
etcd-1   2/2     Running            0            45m
etcd-2   1/2     CrashLoopBackOff   1 (5s ago)   64m
```

To recover the etcd member, delete its persistent volume claim (data-etcd-N) as well as the pod (etcd-N):

```
oc delete pvc/data-etcd-2 pod/etcd-2 --wait=false
```

When the pod restarts, the member should get re-added to the etcd cluster and become healthy again:

```
$ oc get pods -l app=etcd -n $CONTROL_PLANE_NAMESPACE
NAME     READY   STATUS    RESTARTS   AGE
etcd-0   2/2     Running   0          67m
etcd-1   2/2     Running   0          48m
etcd-2   2/2     Running   0          2m2s
```

### Recovery from Quorum Loss

If multiple members of the etcd cluster have lost data or are in a crashloop state, then etcd must be restored from a snapshot. The following procedure requires down time for the control plane as the etcd database is restored.

This procedure applies to an existing HostedCluster with `spec.controllerAvailabilityPolicy: HighlyAvailable`, managed etcd, and accessible persistent volumes for all three members.

!!! warning "Restore every member from the same snapshot"

    Restore the same snapshot into **all three** member PVCs before starting any etcd member, as described in [etcd disaster recovery](https://etcd.io/docs/v3.6/op-guide/recovery/#restoring-from-snapshot). Restoring only `data-etcd-0` and starting the other two members with empty volumes can leave the members serving different data, even when all pods are Ready and endpoint health checks pass. An empty member can become leader, causing loss of the restored data.

NOTE: The following instructions require the `oc` and `jq` binaries.

0. Setup environment variables that point to your hosted cluster:

```
CLUSTER_NAME=my-cluster
CLUSTER_NAMESPACE=clusters
CONTROL_PLANE_NAMESPACE="${CLUSTER_NAMESPACE}-${CLUSTER_NAME}"
```

1. Pause reconciliation on the HostedCluster (setting CLUSTER_NAME and CLUSTER_NAMESPACE to values that correspond to your hosted cluster):

```
oc patch -n ${CLUSTER_NAMESPACE} hostedclusters/${CLUSTER_NAME} -p '{"spec":{"pausedUntil":"true"}}' --type=merge
oc wait -n ${CONTROL_PLANE_NAMESPACE} hostedcontrolplane/${CLUSTER_NAME} \
  --for=jsonpath='{.spec.pausedUntil}'=true --timeout=5m
oc wait -n ${CONTROL_PLANE_NAMESPACE} hostedcontrolplane/${CLUSTER_NAME} \
  --for=condition=ReconciliationActive=False --timeout=5m
```

Wait for both commands to succeed before changing control plane workloads. Setting the HostedCluster field alone does not mean the control-plane-operator has observed the pause.

Stop the API servers so that they cannot write to etcd during recovery or the subsequent data consistency checks:

```bash
oc scale deployment -n ${CONTROL_PLANE_NAMESPACE} --replicas=0 \
  kube-apiserver openshift-apiserver openshift-oauth-apiserver
oc wait -n ${CONTROL_PLANE_NAMESPACE} --for=delete pod \
  -l 'app in (kube-apiserver,openshift-apiserver,openshift-oauth-apiserver)' --timeout=5m
```

2. Take a snapshot of etcd data using one of the following methods:

    a. Download or copy a previously backed up snapshot to `/tmp/etcd.snapshot.db` on the machine where you run `oc`. For example, to use a local backup:

        ```bash
        cp /path/to/backup/snapshot.db /tmp/etcd.snapshot.db
        ```

    b. Take a snapshot from a running etcd pod (PREFERRED but requires available etcd pod):

        ```
        # List etcd pods
        oc get -n ${CONTROL_PLANE_NAMESPACE} pods -l app=etcd

        # If a pod is available:

        # 1. take a snapshot of its database and save it locally
        # Set ETCD_POD to the name of the pod that is available
        ETCD_POD=etcd-0
        oc exec -n ${CONTROL_PLANE_NAMESPACE} -c etcd -t ${ETCD_POD} -- env ETCDCTL_API=3 /usr/bin/etcdctl \
        --cacert /etc/etcd/tls/etcd-ca/ca.crt \
        --cert /etc/etcd/tls/client/etcd-client.crt \
        --key /etc/etcd/tls/client/etcd-client.key \
        --endpoints=https://localhost:2379 \
        snapshot save /var/lib/snapshot.db

        # 2. Verify that the snapshot is good
        oc exec -n ${CONTROL_PLANE_NAMESPACE} -c etcd ${ETCD_POD} -- env ETCDCTL_API=3 /usr/bin/bash -ec '
          if [ -x /usr/bin/etcdutl ]; then
            exec /usr/bin/etcdutl snapshot status /var/lib/snapshot.db -w table
          else
            exec /usr/bin/etcdctl snapshot status /var/lib/snapshot.db -w table
          fi'

        # 3. Make a local copy of the snapshot
        oc cp -c etcd ${CONTROL_PLANE_NAMESPACE}/${ETCD_POD}:/var/lib/snapshot.db /tmp/etcd.snapshot.db
        ```

    c. Make a copy of the snapshot db from etcd persistent storage:

       ```
       # List etcd pods
       oc get -n ${CONTROL_PLANE_NAMESPACE} pods -l app=etcd

       # Find a pod that is running and set its name as the value of ETCD_POD
       ETCD_POD=etcd-0

       # Copy the snapshot db from it
       oc cp -c etcd ${CONTROL_PLANE_NAMESPACE}/${ETCD_POD}:/var/lib/data/member/snap/db /tmp/etcd.snapshot.db
       ```

3. Scale down the etcd statefulset:

```
oc scale -n ${CONTROL_PLANE_NAMESPACE} statefulset/etcd --replicas=0
oc wait -n ${CONTROL_PLANE_NAMESPACE} --for=delete pod -l app=etcd --timeout=5m
```

Wait until all etcd pods are deleted before mounting their PVCs. Keep the statefulset at zero replicas and reconciliation paused until all three PVCs have been restored.

4. Create a temporary pod for each member's PVC:

```bash
# Save etcd image
ETCD_IMAGE=$(oc get -n ${CONTROL_PLANE_NAMESPACE} statefulset/etcd -o jsonpath='{.spec.template.spec.containers[?(@.name=="etcd")].image}')

# Create pods that allow access to all three PVCs
for MEMBER in 0 1 2; do
cat << EOF | oc apply -n ${CONTROL_PLANE_NAMESPACE} -f -
apiVersion: v1
kind: Pod
metadata:
  name: etcd-data-${MEMBER}
  labels:
    app: etcd-data
spec:
  restartPolicy: Never
  containers:
  - name: access
    image: ${ETCD_IMAGE}
    volumeMounts:
    - name: data
      mountPath: /var/lib
    command:
    - /usr/bin/bash
    args:
    - -c
    - sleep infinity
  volumes:
  - name: data
    persistentVolumeClaim:
      claimName: data-etcd-${MEMBER}
EOF
done

oc wait -n ${CONTROL_PLANE_NAMESPACE} --for=condition=Ready \
  pod/etcd-data-0 pod/etcd-data-1 pod/etcd-data-2 --timeout=5m
```

5. Clear previous data and restore the same snapshot into every PVC. Use the same initial cluster membership and token for all restores, with each member's own name and peer URL. For offline snapshot status and restore, select `etcdutl` when available, falling back to `etcdctl` for older payloads.

If the snapshot was copied directly from `member/snap/db` (method 2c), it has no integrity hash. Set `RESTORE_OPTIONS=(--skip-hash-check)` before running the block. For snapshots created with `snapshot save` (methods 2a and 2b), leave `RESTORE_OPTIONS` empty so the integrity check stays enabled. The status query only inspects the database contents, so it succeeds in both cases.

```bash
# Run in a subshell that stops on the first failed command
(
set -e
INITIAL_CLUSTER="etcd-0=https://etcd-0.etcd-discovery.${CONTROL_PLANE_NAMESPACE}.svc:2380,etcd-1=https://etcd-1.etcd-discovery.${CONTROL_PLANE_NAMESPACE}.svc:2380,etcd-2=https://etcd-2.etcd-discovery.${CONTROL_PLANE_NAMESPACE}.svc:2380"
RESTORE_TOKEN="etcd-cluster-$(date +%s)"
# Use (--skip-hash-check) only for a snapshot copied from member/snap/db (method 2c).
RESTORE_OPTIONS=()

for MEMBER in 0 1 2; do
  DATA_POD="etcd-data-${MEMBER}"
  SNAPSHOT_TOOL=$(oc exec -n ${CONTROL_PLANE_NAMESPACE} ${DATA_POD} -- /usr/bin/bash -ec '
    if [ -x /usr/bin/etcdutl ]; then
      echo /usr/bin/etcdutl
    else
      echo /usr/bin/etcdctl
    fi')
  oc cp /tmp/etcd.snapshot.db ${CONTROL_PLANE_NAMESPACE}/${DATA_POD}:/var/lib/restored.snap.db
  oc exec -n ${CONTROL_PLANE_NAMESPACE} ${DATA_POD} -- env ETCDCTL_API=3 "${SNAPSHOT_TOOL}" \
    snapshot status /var/lib/restored.snap.db -w table

  oc exec -n ${CONTROL_PLANE_NAMESPACE} ${DATA_POD} -- rm -rf /var/lib/data
  oc exec -n ${CONTROL_PLANE_NAMESPACE} ${DATA_POD} -- env ETCDCTL_API=3 "${SNAPSHOT_TOOL}" \
    snapshot restore /var/lib/restored.snap.db \
    "${RESTORE_OPTIONS[@]}" \
    --data-dir=/var/lib/data \
    --name etcd-${MEMBER} \
    --initial-cluster-token="${RESTORE_TOKEN}" \
    --initial-cluster="${INITIAL_CLUSTER}" \
    --initial-advertise-peer-urls=https://etcd-${MEMBER}.etcd-discovery.${CONTROL_PLANE_NAMESPACE}.svc:2380

  oc exec -n ${CONTROL_PLANE_NAMESPACE} ${DATA_POD} -- rm /var/lib/restored.snap.db
done
)
```

Do not continue unless all three restores succeed. If a restore fails, correct the error and repeat this step for all three members using the same snapshot and a single shared token.

6. Delete the temporary data access pods and wait for their termination so that the PVCs can be mounted by etcd:

```bash
oc delete -n ${CONTROL_PLANE_NAMESPACE} pod/etcd-data-0 pod/etcd-data-1 pod/etcd-data-2 --wait=true
```

7. Scale up the etcd cluster:
```
oc scale -n ${CONTROL_PLANE_NAMESPACE} statefulset/etcd --replicas=3
oc rollout status -n ${CONTROL_PLANE_NAMESPACE} statefulset/etcd --timeout=5m
```

8. Verify the restored data on each member's own endpoint while reconciliation remains paused and the API servers remain stopped:

These online health, status, and key-value queries use `etcdctl` on all supported payloads. The `etcdutl` selection above applies only to offline snapshot operations.

```bash
for MEMBER in 0 1 2; do
  oc exec -n ${CONTROL_PLANE_NAMESPACE} -c etcd etcd-${MEMBER} -- env \
    ETCDCTL_API=3 \
    ETCDCTL_CACERT=/etc/etcd/tls/etcd-ca/ca.crt \
    ETCDCTL_CERT=/etc/etcd/tls/client/etcd-client.crt \
    ETCDCTL_KEY=/etc/etcd/tls/client/etcd-client.key \
    ETCDCTL_ENDPOINTS=https://etcd-${MEMBER}.etcd-discovery.${CONTROL_PLANE_NAMESPACE}.svc:2379 \
    /usr/bin/bash -ec '
      etcdctl endpoint health
      etcdctl endpoint status -w table
      etcdctl endpoint hashkv -w table
      etcdctl get /kubernetes.io/ --prefix --count-only --consistency=s -w fields
    '
done
```

Check that all three endpoints are healthy, share a cluster ID (`ClusterID`) and leader, and have distinct member IDs. The key-value hashes, `Revision` values, and `Count` values must match across all three members. The revision must correspond to the restored snapshot, and the key count must be consistent with the expected hosted cluster data. Allow members to finish applying updates and repeat the checks if necessary. Pod readiness, matching Raft indexes, and endpoint health alone do not establish data consistency.

If these checks fail, keep reconciliation paused and the API servers stopped. Repeat the restore from the same known-good snapshot on all three PVCs; do not try to repair a partially restored cluster by deleting members with unknown data.

9. After data consistency is confirmed, request a control plane restart to refresh caches, then remove the HostedCluster pause. Reconciliation restores the API server replica counts:

```
oc annotate -n ${CLUSTER_NAMESPACE} hostedcluster/${CLUSTER_NAME} \
  hypershift.openshift.io/restart-date="$(date -u +%Y-%m-%dT%H:%M:%SZ)" --overwrite
oc patch -n ${CLUSTER_NAMESPACE} hostedclusters/${CLUSTER_NAME} -p '{"spec":{"pausedUntil":null}}' --type=merge
```
