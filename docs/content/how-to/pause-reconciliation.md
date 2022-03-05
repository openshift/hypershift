# Pause Reconciliation

The `pausedUntil` field allows a hypershift administrator to pause reconciliation of a HostedCluster/HostedControlPlane pair. This is useful in operational scenarios like backing up and restoring an etcd database. It is also useful for debugging problems with HostedClusters/HostedControlPlanes.


Pause reconciliation for a given `HostedCluster/HostedControlPlane` pair by populating the pausedUntil field of the resource:

```
#Can be boolean or RFC339 timestamp
# PAUSED_UNTIL="true"
PAUSED_UNTIL="2022-03-03T03:28:48Z"
kubectl patch -n HOSTED_CLUSTERS_NAMESPACE hostedclusters/HOSTED_CLUSTER_NAME -p '{"spec":{"pausedUntil":"'${PAUSEDUNTIL}'"}}' --type=merge
```

Two formats are accepted:
1) If a [RFC3339](https://datatracker.ietf.org/doc/html/rfc3339) formatted date is specified: reconciliation will be paused until that date.
2) If the boolean value of true is passed: reconciliation will be paused until the field is removed from the HostedCluster.

All other values will not pause reconciliation.

The associated downstream HostedControlPlane resource will have the pause reconciliation field added to it automatically. When the user removes the field from the HostedCluster or the specified date is passed: reconciliation will continue on the object

The field can be removed with the following patch command:
```
kubectl patch -n HOSTED_CLUSTERS_NAMESPACE hostedclusters/HOSTED_CLUSTER_NAME -p '{"spec":{"pausedUntil":null}}' --type=merge
```