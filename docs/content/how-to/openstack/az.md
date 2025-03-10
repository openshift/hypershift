# Availability Zones

This example will create a Nodepool that will spawn 2 additional machines in a given Nova availability zone.
Availability Zones do not necessarily correspond to fault domains and do not inherently provide high availability benefits.

```shell
export NODEPOOL_NAME=$CLUSTER_NAME-az1
export WORKER_COUNT="2"
export FLAVOR="m1.xlarge"
export AZ="az1"

hcp create nodepool openstack \
  --cluster-name $CLUSTER_NAME \
  --name $NODEPOOL_NAME \
  --node-count $WORKER_COUNT \
  --openstack-node-flavor $FLAVOR \
  --openstack-node-availability-zone $AZ \
```

Check the status of the NodePool by listing `nodepool` resources in the `clusters`
namespace:

```shell
oc get nodepools --namespace clusters

NAME                      CLUSTER         DESIRED NODES   CURRENT NODES   AUTOSCALING   AUTOREPAIR   VERSION   UPDATINGVERSION   UPDATINGCONFIG   MESSAGE
example                   example         5               5               False         False        4.17.0
example-az1               example         2                               False         False                  True              True             Minimum availability requires 2 replicas, current 0 available
```

After a while, in our hosted cluster this is what we will see:

```shell
oc --kubeconfig $CLUSTER_NAME-kubeconfig get nodes

NAME                      STATUS   ROLES    AGE     VERSION
(..)
example-extra-az-zh9l5    Ready    worker   2m6s    v1.27.4+18eadca
example-extra-az-zr8mj    Ready    worker   102s    v1.27.4+18eadca
...
```

And the nodepool will be in the desired state:

```shell
oc get nodepools --namespace clusters

NAME                      CLUSTER         DESIRED NODES   CURRENT NODES   AUTOSCALING   AUTOREPAIR   VERSION   UPDATINGVERSION   UPDATINGCONFIG   MESSAGE
example                   example         5               5               False         False        4.17.0
example-az1               example         2               2               False         False        4.17.0
```
