# Create a OpenStack cluster

Install an OCP cluster running on VMs within a management OCP cluster

## Limitations

* The HyperShift Operator with OpenStack support is currently in development and is not intended for production use.
* OpenStack CSI (Cinder and Manila) are not functional.
* Operators running in the workload cluster (e.g. console) won't be operational on day 1 and a manual and documented
  action is required to make them work on day 2.

## Prerequisites

* Admin access to an OpenShift cluster (version 4.17+) specified by the `KUBECONFIG` environment variable.
* The Management OCP cluster must be configured with OVNKubernetes as the default pod network CNI.
* The OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`).
* A valid [pull secret](https://console.redhat.com/openshift/install/platform-agnostic/user-provisioned) file for the `quay.io/openshift-release-dev` repository.
* OpenStack Octavia service must be running if Ingress is configured with an Octavia load balancer.

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
$PWD:/output docker.io/library/golang:1.22 /bin/bash -c \
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
hypershift install --tech-preview-no-upgrade
```

!!! note

    Hypershift on OpenStack is possible behind a feature gate, which is why we have
    to install the operator with `--tech-preview-no-upgrade`.

You will see the operator running in the `hypershift` namespace:

```shell
oc -n hypershift get pods

NAME                        READY   STATUS    RESTARTS   AGE
operator-755d587f44-lrtrq   1/1     Running   0          114s
operator-755d587f44-qj6pz   1/1     Running   0          114s
```

## Upload RHCOS image in OpenStack

For now, we need to manually push an RHCOS image that will be used when deploying the node pools
on OpenStack. In the future, the CAPI provider (CAPO) will handle the RHCOS image lifecycle by using
the image available in the chosen release payload.

Here is an example of how to upload an RHCOS image to OpenStack:

```shell
openstack image create --disk-format qcow2 --file rhcos-417.94.202407080309-0-openstack.x86_64.qcow2 rhcos
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
export WORKER_COUNT="2"

export OS_CLOUD="openstack"

# Image name is the name of the image in OpenStack that was pushed in the previous step.
export IMAGE_NAME="rhcos"

# Flavor for the nodepool
export FLAVOR="m1.large"

# Optional flags:
# External network to use for the API and Ingress endpoints.
export EXTERNAL_ID="5387f86a-a10e-47fe-91c6-41ac131f9f30"

# CA certificate path to use for the OpenStack API if using self-signed certificates.
export CA_CERT_PATH="$HOME/ca.crt"

# SSH Key for the nodepool VMs
export SSH_KEY="$HOME/.ssh/id_rsa.pub"

hcp create cluster openstack \
--name $CLUSTER_NAME \
--node-pool-replicas $WORKER_COUNT \
--pull-secret $PULL_SECRET \
--ssh-key $SSH_KEY \
--openstack-ca-cert-file $CA_CERT_PATH \
--openstack-external-network-id $EXTERNAL_ID \
--openstack-node-image-name $IMAGE_NAME \
--openstack-node-flavor $FLAVOR
```

!!! note

    A default NodePool will be created for the cluster with 2 vm worker replicas
    per the `--node-pool-replicas` flag.

!!! note

    To enable HA, the `--control-plane-availability-policy` flag can be set to `HighlyAvailable`.
    This requires at least 3 worker nodes in the management cluster.
    Pods will be scheduled across different nodes to ensure that the control plane is highly available.
    When the management cluster worker nodes are spread across different availability zones,
    the hosted control plane will be spread across different availability zones as well in `PreferredDuringSchedulingIgnoredDuringExecution` mode for
    `PodAntiAffinity`.

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

A guest cluster backed by OpenStack virtual machines typically takes around 10-15
minutes to fully provision. The status of the guest cluster can be seen by
viewing the corresponding HostedCluster resource. For example, the output below
reflects what a fully provisioned HostedCluster object looks like.

```shell linenums="1"

oc get --namespace clusters hostedclusters

NAME            VERSION   KUBECONFIG                       PROGRESS   AVAILABLE   PROGRESSING   MESSAGE
example         4.17.0    example-admin-kubeconfig         Completed  True        False         The hosted control plane is available
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
version   4.17.0        True        False         5m39s   Cluster version is 4.17.0
```

## Ingress and DNS

Once the workload cluster is deploying, the Ingress controller will be installed
and a router named `router-default` will be created in the `openshift-ingress` namespace.

You'll need to update your DNS with the external IP of that router so Ingress (and dependent operators like console) can work.
You can run this command to get the external IP:

```shell
oc --kubeconfig $CLUSTER_NAME-kubeconfig -n openshift-ingress get service/router-default -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
```

Now you need to create a DNS A record for `*.apps.<cluster-name>.<base-domain>` that matches the returned IP address.

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
export IMAGE_NAME="rhcos"
export FLAVOR="m1.xlarge"
export AZ="az1"

hcp create nodepool openstack \
  --cluster-name $CLUSTER_NAME \
  --name $NODEPOOL_NAME \
  --node-count $WORKER_COUNT \
  --openstack-node-image-name $IMAGE_NAME \
  --openstack-node-flavor $FLAVOR \
  --openstack-node-availability-zone $AZ
```

Check the status of the NodePool by listing `nodepool` resources in the `clusters`
namespace:

```shell
oc get nodepools --namespace clusters

NAME                      CLUSTER         DESIRED NODES   CURRENT NODES   AUTOSCALING   AUTOREPAIR   VERSION   UPDATINGVERSION   UPDATINGCONFIG   MESSAGE
example                   example         5               5               False         False        4.17.0                                       
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
example                   example         5               5               False         False        4.17.0                                       
example-extra-cpu         example         2               2               False         False        4.17.0  
```

## Delete a HostedCluster

To delete a HostedCluster:

```shell
hcp destroy cluster openstack --name $CLUSTER_NAME
```
