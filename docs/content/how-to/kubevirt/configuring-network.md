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
--attach-default-network=false \
--additional-network name:my-namespace/network1 \
```

## Avoiding Network CIDR Collisions between Hosting and Guest Clusters.

When creating a guest cluster, it's crucial to ensure that the hosting and guest clusters do not share the same subnet (cidr) for the cluster network.
This prevents DNS-related conflicts and avoids issues during the deployment of guest clusters.


### Guidelines to Avoid CIDR Collisions:


1. Detecting Hosting Cluster Network CIDR Range.

Determine the cluster network CIDR range from the underlying Hosting cluster using the command:

```
oc --kubeconfig <hosting cluster kubeconfig> get network cluster -o=jsonpath='{.spec.clusterNetwork}'
```


2. Identifying CIDR Conflicts with HCP Default Range

Compare the default CIDR range used by HCP, which is typically set to 10.132.0.0/14,
with the CIDR range obtained in Step 1 to detect any conflicts with the hosting CIDR range.


3. Setting Custom Cluster CIDR Range:

If a conflict is detected, use the HCP CLI to specify a custom CIDR range for the guest cluster using
the --cluster-cidr flag. The default CIDR range used by the HCP CLI is designed to be offset from the
typical OCP deployment range, so under normal operation, this offset should prevent collisions.

For example, if the hosting cluster's clusterNetwork CIDR is set to 10.132.0.0/14, it's imperative to
select a different CIDR range for the guest cluster to prevent conflicts.

For instance, consider specifying a different CIDR range, such as 10.128.0.0/14 (this range is just an example),
for the guest cluster.

```
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
--cluster-cidr 10.128.0.0/14
```

By following these guidelines, you can ensure successful deployment of guest clusters, avoid network CIDR collisions,
and prevent DNS-related issues between hosting and guest clusters.


## Avoiding Join Subnet Collisions When Using UDNs in Guest Clusters

By default, guest clusters use the join subnet 100.65.0.0/16 for pod networking to avoid collisions with the
infrastructure cluster join subnet 100.64.0.0/16. However, ovn-kubernetes Layer2 and Layer3 primary UDNs also use 100.65.0.0/16 by default,
which creates a collision with the default pod network.

To resolve this issue, users who want to use Layer2 or Layer3 primary UDNs must override the `joinSubnets` field in the
`UserDefinedNetwork` or `ClusterUserDefinedNetwork` to specify a subnet different from 100.65.0.0/16.


This is a primary UDN layer3 example:
```yaml
 apiVersion: k8s.ovn.org/v1
  kind: UserDefinedNetwork
  metadata:
    name: my-layer3-net
    namespace: default
  spec:
    topology: Layer3
    layer3:
      role: Primary
      subnets:
      - cidr: 192.168.100.0/16
        hostSubnet: 24
      joinSubnets:
      - "100.66.0.0/16"
      mtu: 1500
```

And a layer2:
```yaml
apiVersion: k8s.ovn.org/v1
  kind: UserDefinedNetwork
  metadata:
    name: my-layer2-net
    namespace: default
  spec:
    topology: Layer2
    layer2:
      role: Primary
      subnets:
      - "192.168.100.0/16"
      joinSubnets:
      - "100.66.0.0/24"
      mtu: 1500
      ipam:
        lifecycle: Persistent
```


