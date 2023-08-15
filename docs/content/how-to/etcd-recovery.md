# Etcd Recovery

### Overview

Etcd pods for hosted clusters run as part of a statefulset (etcd). The statefulset relies on persistent storage to store etcd data per member. In the case of a HighlyAvailable control plane, the size of the statefulset is 3 and each member (etcd-N) has its own PersistentVolumeClaim (etcd-data-N).

### Checking cluster health

Execute into a running etcd pod:

```
$ oc rsh etcd-0
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
$ oc get pods -l app=etcd -n $CONTROL_PLANE_NAMESPACE
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