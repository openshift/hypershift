# Configuring Network

By default, the NodePools for the KubeVirt platform generate VMs which are
attached to the default pod network. It is possible to attach additional
networks to the NodePools VMs as well though.

## Attaching Additional Networks to KubeVirt VMs

Attaching additional networks to a KubeVirt NodePool is accomplished through
the usage of multus and [NetworkAttachmentDefinitions](https://docs.openshift.com/container-platform/latest/rest_api/network_apis/networkattachmentdefinition-k8s-cni-cncf-io-v1.html). More information about
how to configure NetworkAttachementDefinitions can be found [here](https://70729--ocpdocs-pr.netlify.app/openshift-enterprise/latest/networking/multiple_networks/configuring-additional-network). Below is an example
of adding multiple additional networks to a guest cluster at creation time
using the hcp command line tool with the --additional-network argument.

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
–additional-network name:my-namespace/network1 \
–additional-network name:my-namespace/network2
```

In this example, the KubeVirt VMs will have interfaces attached to the networks
for the NetworkAttachmentDefinitions network1 and network2 which reside in
namespace my-namespace.

## Using Secondary Network as Default

Users managing a network (DHCP, routing, etc...) can use that network 
as the default one for the kubevirt hosted clusters, to do so
disable pod default network and attach an additional one that connects to it 
with the hcp command line tool arguments `--attach-default-network=false` and 
`--additional-network`.

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
--attach-default-network false \
--additional-network name:my-namespace/network1 \
```

