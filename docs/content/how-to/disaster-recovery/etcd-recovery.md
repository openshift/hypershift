# Etcd Recovery

### Overview

Etcd pods for hosted clusters run as part of a statefulset (etcd). The statefulset relies on persistent storage to store etcd data per member. In the case of a HighlyAvailable control plane, the size of the statefulset is 3 and each member (etcd-N) has its own PersistentVolumeClaim (etcd-data-N).

### Automatic Recovery of Removed Members

In certain circumstances, an etcd member is removed from the cluster. This could be due to networking issues (sdn or dns of management cluster). The hypershift operator can automatically recover from this situation by enabling automatic etcd recovery (--enable-etcd-recovery), which is set to true by default.

If this is enabled, then the HyperShift operator will attempt to recover the health of an etcd cluster if the following conditions are met:

* The hosted cluster is configured to run HighlyAvailable (`spec.controllerAvailabilityPolicy = HighlyAvailable`)
* Etcd is managed by HyperShift (`spec.etcd.managementType = Managed`)
* Only one member of the etcd cluster is failing (quorum is not lost)

The recovery procedure consists of the following:
* If a member has been removed from the etcd cluster, re-add the missing member by executing the `member add` command
* The administrator should [delete the etcd member's pod and pvc](#single-node-recovery), after which the HyperShift operator will automatically provision a replacement etcd member Pod and PersistentVolume.

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
```

2. Take a snapshot of etcd data using one of the following methods:

    a. Use a previously backed up snapshot

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
        oc exec -n ${CONTROL_PLANE_NAMESPACE} -c etcd -t ${ETCD_POD} -- env ETCDCTL_API=3 /usr/bin/etcdctl -w table snapshot status /var/lib/snapshot.db

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
```

4. Delete volumes for 2nd and 3rd members:
```
oc delete -n ${CONTROL_PLANE_NAMESPACE} pvc/data-etcd-1 pvc/data-etcd-2
```

5. Create pod to access the first etcd member's data:

```
# Save etcd image
ETCD_IMAGE=$(oc get -n ${CONTROL_PLANE_NAMESPACE} statefulset/etcd -o jsonpath='{ .spec.template.spec.containers[0].image }')

# Create pod that will allow access to etcd data:
cat << EOF | oc apply -n ${CONTROL_PLANE_NAMESPACE} -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: etcd-data
spec:
  replicas: 1
  selector:
    matchLabels:
      app: etcd-data
  template:
    metadata:
      labels:
        app: etcd-data
    spec:
      containers:
      - name: access
        image: $ETCD_IMAGE
        volumeMounts:
        - name: data
          mountPath: /var/lib
        command:
        - /usr/bin/bash
        args:
        - -c
        - |-
          while true; do
            sleep 1000
          done
      volumes:
      - name: data
        persistentVolumeClaim:
          claimName: data-etcd-0
EOF

```

6. Clear previous data and restore snapshot

```
# Wait for the etcd-data pod to start running
oc get -n ${CONTROL_PLANE_NAMESPACE} pods -l app=etcd-data

# Get the name of the etcd-data pod
DATA_POD=$(oc get -n ${CONTROL_PLANE_NAMESPACE} pods --no-headers -l app=etcd-data -o name | cut -d/ -f2)

# Copy local snapshot into the pod
oc cp /tmp/etcd.snapshot.db ${CONTROL_PLANE_NAMESPACE}/${DATA_POD}:/var/lib/restored.snap.db

# Remove old data
oc exec -n ${CONTROL_PLANE_NAMESPACE} ${DATA_POD} -- rm -rf /var/lib/data
oc exec -n ${CONTROL_PLANE_NAMESPACE} ${DATA_POD} -- mkdir -p /var/lib/data

# Restore snapshot
oc exec -n ${CONTROL_PLANE_NAMESPACE} ${DATA_POD} -- etcdutl snapshot restore /var/lib/restored.snap.db \
     --data-dir=/var/lib/data --skip-hash-check \
     --name etcd-0 \
     --initial-cluster-token=etcd-cluster \
     --initial-cluster etcd-0=https://etcd-0.etcd-discovery.${CONTROL_PLANE_NAMESPACE}.svc:2380,etcd-1=https://etcd-1.etcd-discovery.${CONTROL_PLANE_NAMESPACE}.svc:2380,etcd-2=https://etcd-2.etcd-discovery.${CONTROL_PLANE_NAMESPACE}.svc:2380 \
     --initial-advertise-peer-urls https://etcd-0.etcd-discovery.${CONTROL_PLANE_NAMESPACE}.svc:2380

# Remove snapshot from etcd-0 data directory
oc exec -n ${CONTROL_PLANE_NAMESPACE} ${DATA_POD} -- rm /var/lib/restored.snap.db
```

7. Delete data access deployment:

```
oc delete -n ${CONTROL_PLANE_NAMESPACE} deployment/etcd-data
```

8. Scale up etcd cluster:
```
oc scale -n ${CONTROL_PLANE_NAMESPACE} statefulset/etcd --replicas=3
```

Wait for the all etcd member pods to come up and report available:
```
oc get -n ${CONTROL_PLANE_NAMESPACE} pods -l app=etcd -w
```

9. Remove hosted cluster pause:

```
oc patch -n ${CLUSTER_NAMESPACE} hostedclusters/${CLUSTER_NAME} -p '{"spec":{"pausedUntil":null}}' --type=merge
```
