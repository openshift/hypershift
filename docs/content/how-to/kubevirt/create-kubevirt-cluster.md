# Create a Kubevirt cluster

Install an OCP cluster running on VMs within a management OCP cluster

## Prerequisites

* Admin access to an OpenShift cluster (version 4.14+) specified by the `KUBECONFIG` environment variable.
* The management OCP cluster must have wildcard dns routes enabled. `oc patch ingresscontroller -n openshift-ingress-operator default --type=json -p '[{ "op": "add", "path": "/spec/routeAdmission", "value": {wildcardPolicy: "WildcardsAllowed"}}]'`
* The management OCP cluster must have Openshift Virtualization (4.14+) installed on it. [Instructions for installing Openshift Virtualization](https://docs.openshift.com/container-platform/4.12/virt/install/installing-virt-web.html)
* The Management OCP cluster must be configured with OVNKubernetes as the default pod network CNI.
* The Management OCP cluster must have LoadBalancer service support. [Instructions for installing MetalLB](https://docs.openshift.com/container-platform/4.12/networking/metallb/metallb-operator-install.html)
* The management OCP cluster must have default storage class. [Storage Configuration Documentation](https://docs.openshift.com/container-platform/4.12/post_installation_configuration/storage-configuration.html) Example of how to set a default storage class: `oc patch storageclass ocs-storagecluster-ceph-rbd -p '{"metadata": {"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}'`
* The OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`).
* A valid [pull secret](https://console.redhat.com/openshift/install/platform-agnostic/user-provisioned) file for the `quay.io/openshift-release-dev` repository.
* A network MTU of 9000 or larger on the OCP cluster hosting the KubeVirt VMs is recommended for optimal network performance. Smaller MTU settings will work, but network latency and throughput of the hosted pods will be impacted. Multiqueue should only be enabled on NodePools when the MTU is 9000 or larger.

## Installing HyperShift Operator and cli tooling

Before creating a guest cluster, the hcp cli, hypershift cli, and HyperShift
Operator must be installed.

The `hypershift` cli tool is a development tool that is used to install
developer builds of the HyperShift Operator.

The `hcp` cli tool is used to manage the creation and destruction of guest
clusters.

### Build the HyperShift and HCP CLI

The command below builds latest hypershift and hcp cli tools from source and
places the cli tool within the /usr/local/bin directory.

!!! note

    The command below is the same if you use docker.

```shell
podman run --rm --privileged -it -v \
$PWD:/output docker.io/library/golang:1.20 /bin/bash -c \
'git clone https://github.com/openshift/hypershift.git && \
cd hypershift/ && \
make hypershift product-cli && \
mv bin/hypershift /output/hypershift && \
mv bin/hcp /output/hcp'

sudo install -m 0755 -o root -g root $PWD/hypershift /usr/local/bin/hypershift
sudo install -m 0755 -o root -g root $PWD/hcp /usr/local/bin/hcp
rm $PWD/hypershift
rm $PWD/hcp
```

## Deploy the HyperShift Operator

Use the hypershift cli tool to install the HyperShift operator into the
management cluster.

```shell
hypershift install
```

You will see the operator running in the `hypershift` namespace:

```sh
oc -n hypershift get pods

NAME                        READY   STATUS    RESTARTS   AGE
operator-755d587f44-lrtrq   1/1     Running   0          114s
operator-755d587f44-qj6pz   1/1     Running   0          114s
```

## Create a HostedCluster

Once all the [prerequisites](#prerequisites) are met, and the HyperShift
operator is installed, it is now possible to create a guest cluster.

Below is an example of how to create a guest cluster using environment
variables and the `hcp` cli tool.

!!! note

    The --release-image flag could be used to provision the HostedCluster with a specific OpenShift Release (the hypershift operator has a support matrix of releases supported by a given version of the operator)


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
--cores $CPU
```

!!! note

    A default NodePool will be created for the cluster with 2 vm worker replicas
    per the `--node-pool-replicas` flag.

After a few moments we should see our hosted control plane pods up and running:

~~~sh
oc -n clusters-$CLUSTER_NAME get pods

NAME                                                  READY   STATUS    RESTARTS   AGE
capi-provider-5cc7b74f47-n5gkr                        1/1     Running   0          3m
catalog-operator-5f799567b7-fd6jw                     2/2     Running   0          69s
certified-operators-catalog-784b9899f9-mrp6p          1/1     Running   0          66s
cluster-api-6bbc867966-l4dwl                          1/1     Running   0          66s
.
.
.
redhat-operators-catalog-9d5fd4d44-z8qqk              1/1     Running   0          66s
~~~

A guest cluster backed by KubeVirt virtual machines typically takes around 10-15
minutes to fully provision. The status of the guest cluster can be seen by
viewing the corresponding HostedCluster resource. For example, the output below
reflects what a fully provisioned HostedCluster object looks like.

```shell linenums="1"

oc get --namespace clusters hostedclusters

NAME            VERSION   KUBECONFIG                       PROGRESS   AVAILABLE   PROGRESSING   MESSAGE
example         4.14.0    example-admin-kubeconfig         Completed  True        False         The hosted control plane is available
```

## Accessing the HostedCluster

CLI access to the guest cluster is gained by retrieving the guest cluster's
kubeconfig. Below is an example of how to retrieve the guest cluster's
kubeconfig using the hcp cli.

```shell
hcp create kubeconfig --name $CLUSTER_NAME > $CLUSTER_NAME-kubeconfig
```

If we access the cluster, we will see we have two nodes.

```shell
oc --kubeconfig $CLUSTER_NAME-kubeconfig get nodes

NAME                  STATUS   ROLES    AGE   VERSION
example-n6prw         Ready    worker   32m   v1.27.4+18eadca
example-nc6g4         Ready    worker   32m   v1.27.4+18eadca
```

We can also check the ClusterVersion:

```shell
oc --kubeconfig $CLUSTER_NAME-kubeconfig get clusterversion

NAME      VERSION       AVAILABLE   PROGRESSING   SINCE   STATUS
version   4.14.0        True        False         5m39s   Cluster version is 4.14.0
```

## Influencing VM Scheduling

By default, KubeVirt VMs created by a NodePool are scheduled to any available
node with the appropriate capacity to run the VM. A topologySpreadConstraint
is used to try to spread VMs for a NodePool out across multiple underlying
nodes, but in general it is possible for the VMs to be scheduled to any node
that meets the requirements for running KubeVirt VMs.
For availability reasons, the topologySpreadConstraint is only a soft constraint
(`whenUnsatisfiable: ScheduleAnyway` policy) so, under certain circumstances,
VMs for a single nodepool can “clump” together on a single node due to live migration
to available nodes.
The De-Scheduler can optionally be used to continuously redistribute VMs so that
they again satisfy the topologySpreadConstraint as soon as possible.
To achieve that behavior, the cluster-kube-descheduler-operator should be installed
and configured with something like:
```yaml
apiVersion: operator.openshift.io/v1
kind: KubeDescheduler
metadata:
  name: cluster
  namespace: openshift-kube-descheduler-operator
spec:
  mode: Automatic
  managementState: Managed
  deschedulingIntervalSeconds: 60
  profiles:
  - SoftTopologyAndDuplicates
  - EvictPodsWithPVC
  - EvictPodsWithLocalStorage
  profileCustomizations:
    devEnableEvictionsInBackground: true
```

It is possible to influence where the KubeVirt VMs within a NodePool are
scheduled through the use of NodeSelectors. Below is an example of using a
NodeSelector on the VMs to place the VMs only on nodes with a specific label
through usage of the hcp command line and the --vm-node-selector argument.

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
--vm-node-selector labelKey1=labelVal1,labeKey2=labelVal2
```

In the example above, the KubeVirt VMs will only be scheduled to nodes that
 contain the labels labelKey1=labelVal1 and labelKey2=labelVal2.

## Scaling an existing NodePool

Manually scale a NodePool using the `oc scale` command:

```shell linenums="1"
NODEPOOL_NAME=$CLUSTER_NAME
NODEPOOL_REPLICAS=5

oc scale nodepool/$NODEPOOL_NAME --namespace clusters --replicas=$NODEPOOL_REPLICAS
```

After a while, in our hosted cluster this is what we will see:

```shell
oc --kubeconfig $CLUSTER_NAME-kubeconfig get nodes

NAME                  STATUS   ROLES    AGE     VERSION
example-9jvnf         Ready    worker   97s     v1.27.4+18eadca
example-n6prw         Ready    worker   116m    v1.27.4+18eadca
example-nc6g4         Ready    worker   117m    v1.27.4+18eadca
example-thp29         Ready    worker   4m17s   v1.27.4+18eadca
example-twxns         Ready    worker   88s     v1.27.4+18eadca
```

## Adding Additional NodePools

Create additional NodePools for a guest cluster by specifying a name, number of
replicas, and any additional information such as memory and cpu requirements.

For example, let's create a NodePool with more CPUs assigned to the VMs (4 vs 2):

```shell linenums="1"
export NODEPOOL_NAME=$CLUSTER_NAME-extra-cpu
export WORKER_COUNT="2"
export MEM="6Gi"
export CPU="4"
export DISK="16"

hcp create nodepool kubevirt \
  --cluster-name $CLUSTER_NAME \
  --name $NODEPOOL_NAME \
  --node-count $WORKER_COUNT \
  --memory $MEM \
  --cores $CPU \
  --root-volume-size $DISK
```

Check the status of the NodePool by listing `nodepool` resources in the `clusters`
namespace:

```shell
oc get nodepools --namespace clusters

NAME                      CLUSTER         DESIRED NODES   CURRENT NODES   AUTOSCALING   AUTOREPAIR   VERSION   UPDATINGVERSION   UPDATINGCONFIG   MESSAGE
example                   example         5               5               False         False        4.14.0  
example-extra-cpu         example         2                               False         False                  True              True             Minimum availability requires 2 replicas, current 0 available
```

After a while, in our hosted cluster this is what we will see:

```shell
oc --kubeconfig $CLUSTER_NAME-kubeconfig get nodes

NAME                      STATUS   ROLES    AGE     VERSION
example-9jvnf             Ready    worker   97s     v1.27.4+18eadca
example-n6prw             Ready    worker   116m    v1.27.4+18eadca
example-nc6g4             Ready    worker   117m    v1.27.4+18eadca
example-thp29             Ready    worker   4m17s   v1.27.4+18eadca
example-twxns             Ready    worker   88s     v1.27.4+18eadca
example-extra-cpu-zh9l5   Ready    worker   2m6s    v1.27.4+18eadca
example-extra-cpu-zr8mj   Ready    worker   102s    v1.27.4+18eadca
```

And the nodepool will be in the desired state:

```shell
oc get nodepools --namespace clusters

NAME                      CLUSTER         DESIRED NODES   CURRENT NODES   AUTOSCALING   AUTOREPAIR   VERSION   UPDATINGVERSION   UPDATINGCONFIG   MESSAGE
example                   example         5               5               False         False        4.14.0  
example-extra-cpu         example         2               2               False         False        4.14.0  
```

## Delete a HostedCluster

To delete a HostedCluster:

```shell
hcp destroy cluster kubevirt --name $CLUSTER_NAME
```

