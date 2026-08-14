# Performance Tuning

By default, KubeVirt VMs use best effort resource requests for CPU. This means
 it’s possible for a VM to have overcommitted CPU which are in competition
with other resources on the host machine. While best effort CPU will provide
the best utilization of resources on a node, it could impact performance of
the VM in high density applications.

## Guaranteed CPU QOS

To avoid overcommitment of CPU, the KubeVirt NodePool can request guaranteed
CPU qos, which will guarantee unshared CPU resources to the KubeVirt VMs. Below
is an example of using the hcp command line tool to request a KubeVirt platform
guest cluster with guaranteed CPU by using the --qos-class argument.

```shell linenums="1"
export CLUSTER_NAME=example
export PULL_SECRET="$HOME/pull-secret"
export MEM="6Gi"
export CPU="2"
export WORKER_COUNT="2"

hcp create cluster kubevirt \
--name $CLUSTER_NAME \
--node-pool-replicas $WORKER_COUNT \
--pull-secret $PULL_SECRET \
--memory $MEM \
--cores $CPU \
--qos-class Guaranteed
```
