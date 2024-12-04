# Create an OpenStack cluster

This document explains how to create HostedClusters and Nodepools using the OpenStack platform.

## Overview

When you create a HostedCluster with the OpenStack platform, HyperShift will install the [OpenStack CAPI
provider](https://github.com/kubernetes-sigs/cluster-api-provider-openstack) in the Hosted Control Plane (HCP) namespace.
Upon scaling up a NodePool, a Machine will be created, and the CAPI provider will create the necessary resources in OpenStack.

## Limitations

* Although the HyperShift Operator with OpenStack support is currently in development and is not intended for production use,
  it is possible to create and manage clusters for development and testing purposes and it's expected to work as described in this document.

## Prerequisites

* Admin access to an OpenShift cluster (version 4.17+) specified by the `KUBECONFIG` environment variable.
  This cluster is referred to as the Management OCP cluster.
* The Management OCP cluster must be configured with OVNKubernetes as the default pod network CNI.
* The OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`) must be installed.
* The `hcp` CLI must be installed and is the production tool to manage the hosted clusters.
* The `hypershift` CLI must be installed to deploy the HyperShift Operator. In production, it is not recommended to use that CLI to
  manage the hosted clusters.
* The HyperShift Operator must be installed in the Management OCP cluster.
* A valid [pull secret](https://console.redhat.com/openshift/install/platform-agnostic/user-provisioned) file for the `quay.io/openshift-release-dev` repository.
* OpenStack Octavia service must be running in the cloud hosting the guest cluster when ingress is configured with an Octavia load balancer.
  In the future, we'll explore other Ingress options like MetalLB.
* The default external network (on which the kube-apiserver LoadBalancer type service is created) of the Management OCP cluster must be reachable from the guest cluster.
* The RHCOS image must be uploaded to OpenStack.

### Install the HyperShift and HCP CLI

The `hcp` CLI tool is used to manage the creation and destruction of guest
clusters.

The `hypershift` CLI tool is a development tool that is used to install
developer builds of the HyperShift Operator.
The command below builds latest hypershift and hcp cli tools from source and
places the CLI tool within the `/usr/local/bin` directory.

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

### Deploy the HyperShift Operator

Use the hypershift cli tool to install the HyperShift operator into the
management cluster.

```shell
hypershift install --tech-preview-no-upgrade
```

!!! note

    HyperShift on OpenStack is possible behind a feature gate, which is why we have
    to install the operator with `--tech-preview-no-upgrade`. Once the platform
    is GA, the operator will be able to be installed without that flag.

Once installed, you should see the operator running in the `hypershift` namespace:

```shell
oc -n hypershift get pods

NAME                        READY   STATUS    RESTARTS   AGE
operator-755d587f44-lrtrq   1/1     Running   0          114s
operator-755d587f44-qj6pz   1/1     Running   0          114s
```

### Upload RHCOS image in OpenStack

For now, we need to manually push an RHCOS image that will be used when deploying the node pools
on OpenStack. In the [future](https://issues.redhat.com/browse/OSASINFRA-3492), the CAPI provider (CAPO) will handle the RHCOS image
lifecycle by using the image available in the chosen release payload.

Here is an example of how to upload an RHCOS image to OpenStack:

```shell
openstack image create --disk-format qcow2 --file rhcos-openstack.x86_64.qcow2 rhcos
```

!!! note

    The `rhcos-openstack.x86_64.qcow2` file is the RHCOS image that was downloaded from the OpenShift mirror.
    You can download the latest RHCOS image from the [Red Hat OpenShift Container Platform mirror](https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/).

## Create a floating IP for the Ingress (optional)

To get Ingress healthy in a HostedCluster without manual intervention, you need to create a floating IP that will be used by the Ingress service.

```shell
openstack floating ip create <external-network-id>
```

If you provide the floating IP to the `--openstack-ingress-floating-ip` flag without pre-creating it, cloud-provider-openstack will create it for you
only if the Neutron API policy allows a user to create floating IP with a specific IP address.

## Update the DNS record for the Ingress (optional)

If you use a pre-defined floating IP for ingress, you need to create a DNS record for the following wildcard domain that needs to point to the Ingress floating IP:
`*.apps.<cluster-name>.<base-domain>`

## Create a HostedCluster

Once all the [prerequisites](#prerequisites) are met, it is now possible to create a guest cluster.

Below is an example of how to create a guest cluster using environment
variables and the `hcp` cli tool.

!!! note

    The --release-image flag could be used to provision the HostedCluster with a specific OpenShift Release (the hypershift operator has a support matrix of releases supported by a given version of the operator)

```shell
export CLUSTER_NAME=example
export BASE_DOMAIN=hypershift.lab
export PULL_SECRET="$HOME/pull-secret"
export WORKER_COUNT="2"

export OS_CLOUD="openstack"

# Image name is the name of the image in OpenStack that was pushed in the previous step.
export IMAGE_NAME="rhcos"

# Flavor for the nodepool
export FLAVOR="m1.large"

# Pre-defined floating IP for Ingress
export INGRESS_FLOATING_IP="<ingress-floating-ip>"

# Optional flags:
# External network to use for the Ingress endpoint.
export EXTERNAL_NETWORK_ID="5387f86a-a10e-47fe-91c6-41ac131f9f30"

# CA certificate path to use for the OpenStack API if using self-signed certificates.
# In 4.18, this is not required as the CA cert found in clouds.yaml will be used.
export CA_CERT_PATH="$HOME/ca.crt"

# In 4.18, this is not required as the file will be discovered.
export CLOUDS_YAML="$HOME/clouds.yaml"

# SSH Key for the nodepool VMs
export SSH_KEY="$HOME/.ssh/id_rsa.pub"

hcp create cluster openstack \
--name $CLUSTER_NAME \
--base-domain $BASE_DOMAIN \
--node-pool-replicas $WORKER_COUNT \
--pull-secret $PULL_SECRET \
--ssh-key $SSH_KEY \
--openstack-credentials-file $CLOUDS_YAML \
--openstack-ca-cert-file $CA_CERT_PATH \
--openstack-external-network-id $EXTERNAL_NETWORK_ID \
--openstack-node-image-name $IMAGE_NAME \
--openstack-node-flavor $FLAVOR \
--openstack-ingress-floating-ip $INGRESS_FLOATING_IP
```

!!! note

    A default NodePool will be created for the cluster with 2 VM worker replicas
    per the `--node-pool-replicas` flag.

!!! note

    When using `hcp` CLI, High Availability will be enabled by default.
    Pods will be scheduled across different nodes to ensure that the control plane is highly available.
    When the management cluster worker nodes are spread across different availability zones,
    the hosted control plane will be spread across different availability zones as well in
    `PreferredDuringSchedulingIgnoredDuringExecution` mode for `PodAntiAffinity`.
    If your management cluster doesn't have enough workers (less than 3), which is not recommended nor supported,
    you'll need to specify the `--control-plane-availability-policy` flag to `SingleReplica`.

After a few moments we should see our hosted control plane pods up and running:

```shell
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
```

A guest cluster backed by OpenStack virtual machines typically takes around 10-15
minutes to fully provision.

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

## Ingress and DNS (optional)

If you haven't created the HostedCluster with `--openstack-ingress-floating-ip`, you'll need to
update the DNS record with the floating IP address that was assigned to the `router-default` Service.

Once the workload cluster is deploying, the Ingress controller will be installed
and a router named `router-default` will be created in the `openshift-ingress` namespace.

You'll need to update your DNS with the external IP of that router so Ingress (and dependent operators like console) can work.

Once the HostedCluster is created, you need to wait for the `router-default` service to get an external IP:

```shell
oc -w --kubeconfig $CLUSTER_NAME-kubeconfig -n openshift-ingress get service/router-default -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
```

When the external IP exists, you can now create a DNS A record for `*.apps.<cluster-name>.<base-domain>` that matches the returned IP address.
Once this is done, the Ingress operator will become healthy and the console will be accessible shortly after.

!!! note

    The DNS propagation time can vary so you might need to wait a few minutes before your HostedCluster becomes healthy.

## Access to the guest cluster

Once the HostedCluster is healthy, you should be able to access the OpenShift console by navigating
to `https://console-openshift-console.apps.<cluster-name>.<base-domain>` in your browser.

To get the `kubeadmin` password, you can run this command:
```shell
oc get --namespace clusters Secret/${CLUSTER_NAME}-kubeadmin-password -o jsonpath='{.data.password}' | base64 --decode
```

To know whether the HostedCluster is healthy, you can verify with this command:
```shell
oc get --namespace clusters hostedclusters

NAME            VERSION   KUBECONFIG                       PROGRESS   AVAILABLE   PROGRESSING   MESSAGE
example         4.17.0    example-admin-kubeconfig         Completed  True        False         The hosted control plane is available
```

## Scaling an existing NodePool

Manually scale a NodePool using the `oc scale` command:

```shell
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
replicas, and any additional information such as availability zones, or platform-specific information
like the additional ports to create for each node.

For example, let's create a new NodePool with 2 replicas in the `az1` availability zone with an additional
port for SR-IOV, with no port security and address pairs:

```shell
export NODEPOOL_NAME=$CLUSTER_NAME-extra-az
export WORKER_COUNT="2"
export IMAGE_NAME="rhcos"
export FLAVOR="m1.xlarge"
export AZ="az1"
export SRIOV_NEUTRON_NETWORK_ID="f050901b-11bc-4a75-a553-878509255760"
export ADDRESS_PAIRS="192.168.0.1-192.168.0.2"

hcp create nodepool openstack \
  --cluster-name $CLUSTER_NAME \
  --name $NODEPOOL_NAME \
  --node-count $WORKER_COUNT \
  --openstack-node-image-name $IMAGE_NAME \
  --openstack-node-flavor $FLAVOR \
  --openstack-node-availability-zone $AZ \
  --openstack-node-additional-port=network-id:$SRIOV_NEUTRON_NETWORK_ID,vnic-type:direct,address-pairs:$ADDRESS_PAIRS,disable-port-security:true
```

Check the status of the NodePool by listing `nodepool` resources in the `clusters`
namespace:

```shell
oc get nodepools --namespace clusters

NAME                      CLUSTER         DESIRED NODES   CURRENT NODES   AUTOSCALING   AUTOREPAIR   VERSION   UPDATINGVERSION   UPDATINGCONFIG   MESSAGE
example                   example         5               5               False         False        4.17.0
example-extra-az          example         2                               False         False                  True              True             Minimum availability requires 2 replicas, current 0 available
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
example-extra-az-zh9l5    Ready    worker   2m6s    v1.27.4+18eadca
example-extra-az-zr8mj    Ready    worker   102s    v1.27.4+18eadca
```

And the nodepool will be in the desired state:

```shell
oc get nodepools --namespace clusters

NAME                      CLUSTER         DESIRED NODES   CURRENT NODES   AUTOSCALING   AUTOREPAIR   VERSION   UPDATINGVERSION   UPDATINGCONFIG   MESSAGE
example                   example         5               5               False         False        4.17.0
example-extra-az          example         2               2               False         False        4.17.0
```

## Delete a HostedCluster

To delete a HostedCluster:

```shell
hcp destroy cluster openstack --name $CLUSTER_NAME
```

The process will take a few minutes to complete and will destroy all resources associated with the HostedCluster including OpenStack resources.
