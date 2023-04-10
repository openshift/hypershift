# Create a Kubevirt cluster

Install a nested OCP cluster running on VMs within a management OCP cluster

## Prerequisites

* Admin access to an OpenShift cluster (version 4.12+) specified by the `KUBECONFIG` environment variable.
* The management OCP cluster must have wildcard dns routes enabled. `oc patch ingresscontroller -n openshift-ingress-operator default --type=json -p '[{ "op": "add", "path": "/spec/routeAdmission", "value": {wildcardPolicy: "WildcardsAllowed"}}]'`
* The management OCP cluster must have Openshift Virtualization installed on it. [Instructions for installing Openshift Virtualization](https://docs.openshift.com/container-platform/4.12/virt/install/installing-virt-web.html)
* The Management OCP cluster must be configured with OVNKubernetes as the default pod network CNI.
* The Management OCP cluster must have LoadBalancer service support. [Instructions for installing MetalLB](https://docs.openshift.com/container-platform/4.12/networking/metallb/metallb-operator-install.html)
* The management OCP cluster must have default storage class. [Storage Configuration Documentation](https://docs.openshift.com/container-platform/4.12/post_installation_configuration/storage-configuration.html) Example of how to set a default storage class: `oc patch storageclass ocs-storagecluster-ceph-rbd -p '{"metadata": {"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}'`
* The OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`).
* A valid [pull secret](https://console.redhat.com/openshift/install/platform-agnostic/user-provisioned) file for the `quay.io/openshift-release-dev` repository.

## Installing HyperShift Operator and hypershift cli tool

Before creating a guest cluster, the Hypershift operator and hypershift cli tool
must be installed.

### Build the HyperShift CLI

The command below builds latest hypershift cli tool from source and places the
cli tool within the /usr/local/bin directory.

!!! note

    The command below is the same if you use docker.
  
```shell
podman run --rm --privileged -it -v \
$PWD:/output docker.io/library/golang:1.18 /bin/bash -c \
'git clone https://github.com/openshift/hypershift.git && \
cd hypershift/ && \
make hypershift && \
mv bin/hypershift /output/hypershift
sudo install -m 0755 -o root -g root $PWD/hypershift /usr/local/bin/hypershift
rm $PWD/hypershift
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
variables and the `hypershift` cli tool.

!!! note

    The --release-image flag could be used to provision the HostedCluster with a specific OpenShift Release (the hypershift operator has a support matrix of releases supported by a given version of the operator)


```shell linenums="1"
export CLUSTER_NAME=example
export PULL_SECRET="$HOME/pull-secret"
export MEM="6Gi"
export CPU="2"
export WORKER_COUNT="2"

hypershift create cluster kubevirt \
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
example         4.12.7    example-admin-kubeconfig         Completed  True        False         The hosted control plane is available
```

## Accessing the HostedCluster

CLI access to the guest cluster is gained by retrieving the guest cluster's
kubeconfig. Below is an example of how to retrieve the guest cluster's
kubeconfig using the hypershift cli.

```shell
hypershift create kubeconfig --name $CLUSTER_NAME > $CLUSTER_NAME-kubeconfig
```

If we access the cluster, we will see we have two nodes.

```shell
oc --kubeconfig $CLUSTER_NAME-kubeconfig get nodes

NAME                  STATUS   ROLES    AGE   VERSION
example-n6prw         Ready    worker   32m   v1.25.4+18eadca
example-nc6g4         Ready    worker   32m   v1.25.4+18eadca
```

We can also check the ClusterVersion:

```shell
oc --kubeconfig $CLUSTER_NAME-kubeconfig get clusterversion

NAME      VERSION       AVAILABLE   PROGRESSING   SINCE   STATUS
version   4.12.7        True        False         5m39s   Cluster version is 4.12.7
```

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
example-9jvnf         Ready    worker   97s     v1.25.4+18eadca
example-n6prw         Ready    worker   116m    v1.25.4+18eadca
example-nc6g4         Ready    worker   117m    v1.25.4+18eadca
example-thp29         Ready    worker   4m17s   v1.25.4+18eadca
example-twxns         Ready    worker   88s     v1.25.4+18eadca
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

hypershift create nodepool kubevirt \
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
example                   example         5               5               False         False        4.12.7                                       
example-extra-cpu         example         2                               False         False                  True              True             Minimum availability requires 2 replicas, current 0 available
```

After a while, in our hosted cluster this is what we will see:

```shell
oc --kubeconfig $CLUSTER_NAME-kubeconfig get nodes

NAME                      STATUS   ROLES    AGE     VERSION
example-9jvnf             Ready    worker   97s     v1.25.4+18eadca
example-n6prw             Ready    worker   116m    v1.25.4+18eadca
example-nc6g4             Ready    worker   117m    v1.25.4+18eadca
example-thp29             Ready    worker   4m17s   v1.25.4+18eadca
example-twxns             Ready    worker   88s     v1.25.4+18eadca
example-extra-cpu-zh9l5   Ready    worker   2m6s    v1.25.4+18eadca
example-extra-cpu-zr8mj   Ready    worker   102s    v1.25.4+18eadca
```

And the nodepool will be in the desired state:

```shell
oc get nodepools --namespace clusters

NAME                      CLUSTER         DESIRED NODES   CURRENT NODES   AUTOSCALING   AUTOREPAIR   VERSION   UPDATINGVERSION   UPDATINGCONFIG   MESSAGE
example                   example         5               5               False         False        4.12.7                                       
example-extra-cpu         example         2               2               False         False        4.12.7  
```

## Delete a HostedCluster

To delete a HostedCluster:

```shell
hypershift destroy cluster kubevirt --name $CLUSTER_NAME
```

## Default Ingress and DNS Behavior

Every OpenShift cluster comes setup with a default application ingress
controller which is expected have an wildcard DNS record associated with it.
By default, guest clusters created using the Hypershift KubeVirt provider
will automatically become a subdomain of the underlying OCP cluster that
the KubeVirt VMs run on.

For example, if an OCP cluster cluster has a default ingress DNS entry of
`*.apps.mgmt-cluster.example.com`, then the default ingress of a KubeVirt
guest cluster named `guest` running on that underlying OCP cluster will
be `*.apps.guest.apps.mgmt-cluster.example.com`.

!!! note

    For this default ingress DNS to work properly, the underlying cluster
    hosting the KubeVirt VMs must allow wildcard DNS routes. This can be
    configured using the following cli command. ```oc patch ingresscontroller -n openshift-ingress-operator default --type=json -p '[{ "op": "add", "path": "/spec/routeAdmission", "value": {wildcardPolicy: "WildcardsAllowed"}}]'```

## Customized Ingress and DNS Behavior

In lieu of the default ingress and DNS behavior, it is also possible to
configure a Hypershift KubeVirt guest cluster with a unique base domain
at creation time. This option does require some manual configuration
steps during creation though.

This process involves three steps:

1. Cluster creation
2. LoadBalancer creation
3. Wildcard DNS configuration

### Step 1 - Deploying the HostedCluster specifying our base domain

```shell linenums="1"
export CLUSTER_NAME=example
export PULL_SECRET="$HOME/pull-secret"
export MEM="6Gi"
export CPU="2"
export WORKER_COUNT="2"
export BASE_DOMAIN=hypershift.lab

hypershift create cluster kubevirt \
--name $CLUSTER_NAME \
--node-pool-replicas $WORKER_COUNT \
--pull-secret $PULL_SECRET \
--memory $MEM \
--cores $CPU \
--base-domain $BASE_DOMAIN
```

With above configuration we will end up having a HostedCluster with an ingress wildcard configured for `*.apps.example.hypershift.lab` (*.apps.<hostedcluster_name\>.<base_domain\>).

This time, the HostedCluster will not finish the deployment (will remain in `Partial` progress) as we saw in the previous section, since we have configured a base domain we need to make sure that the required DNS records and load balancer are in-place:

```shell linenums="1"
oc get --namespace clusters hostedclusters

NAME            VERSION   KUBECONFIG                       PROGRESS   AVAILABLE   PROGRESSING   MESSAGE
example                   example-admin-kubeconfig         Partial    True        False         The hosted control plane is available
```

If we access the HostedCluster this is what we will see:

```shell
hypershift create kubeconfig --name $CLUSTER_NAME > $CLUSTER_NAME-kubeconfig
```

```shell
oc --kubeconfig $CLUSTER_NAME-kubeconfig get co

NAME                                       VERSION   AVAILABLE   PROGRESSING   DEGRADED   SINCE   MESSAGE
console                                    4.12.7    False       False         False      30m     RouteHealthAvailable: failed to GET route (https://console-openshift-console.apps.example.hypershift.lab): Get "https://console-openshift-console.apps.example.hypershift.lab": dial tcp: lookup console-openshift-console.apps.example.hypershift.lab on 172.31.0.10:53: no such host
.
.
.
ingress                                    4.12.7    True        False         True       28m     The "default" ingress controller reports Degraded=True: DegradedConditions: One or more other status conditions indicate a degraded state: CanaryChecksSucceeding=False (CanaryChecksRepetitiveFailures: Canary route checks for the default ingress controller are failing)
```

In the next section we will fix that.

### Step 2 - Set up the LoadBalancer


!!! note

    If your cluster is on bare-metal you may need MetalLB to be able to provision functional LoadBalancer services. Take a look at the section [Optional MetalLB Configuration Steps](#optional-metallb-configuration-steps).

This option requires configuring a new LoadBalancer service that routes to the KubeVirt VMs as well as assign a wildcard DNS entry to the LoadBalancer's IP address.

First, we need to create a LoadBalancer Service that routes ingress traffic to the KubeVirt VMs.

A NodePort Service exposing the HostedCluster ingress already exists, we will grab the NodePorts and create the LoadBalancer service targeting these ports.

1. Grab NodePorts

    ```sh
    export HTTP_NODEPORT=$(oc --kubeconfig $CLUSTER_NAME-kubeconfig get services -n openshift-ingress router-nodeport-default -o jsonpath='{.spec.ports[?(@.name=="http")].nodePort}')
    export HTTPS_NODEPORT=$(oc --kubeconfig $CLUSTER_NAME-kubeconfig get services -n openshift-ingress router-nodeport-default -o jsonpath='{.spec.ports[?(@.name=="https")].nodePort}')
    ```

2. Create LoadBalancer Service

    ```sh
    cat << EOF | oc apply -f -
    apiVersion: v1
    kind: Service
    metadata:
      labels:
        app: $CLUSTER_NAME
      name: $CLUSTER_NAME-apps
      namespace: clusters-$CLUSTER_NAME
    spec:
      ports:
      - name: https-443
        port: 443
        protocol: TCP
        targetPort: ${HTTPS_NODEPORT}
      - name: http-80
        port: 80
        protocol: TCP
        targetPort: ${HTTP_NODEPORT}
      selector:
        kubevirt.io: virt-launcher
      type: LoadBalancer
    EOF
    ```

### Step 3 - Set up a wildcard DNS record for the `*.apps`

Now that we have the ingress exposed, next step is configure a wildcard DNS A record or CNAME that references the LoadBalancer Service's external IP.

1. Get the external IP.

  ```shell
  export EXTERNAL_IP=$(oc -n clusters-$CLUSTER_NAME get service $CLUSTER_NAME-apps -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
  ```

2. Configure a wildcard `*.apps.<hostedcluster_name\>.<base_domain\>.` DNS entry referencing the IP stored in $EXTERNAL_IP that is routable both internally and externally of the cluster.

For example, for the cluster used in this example and for an external ip value of `192.168.20.30` this is what DNS resolutions will look like:

```sh
dig +short test.apps.example.hypershift.lab

192.168.20.30
```

### Checking HostedCluster status after having fixed the ingress

Now that we fixed the ingress, we should see our HostedCluster progress moved from `Partial` to `Completed`.

```shell linenums="1"
oc get --namespace clusters hostedclusters

NAME            VERSION   KUBECONFIG                       PROGRESS    AVAILABLE   PROGRESSING   MESSAGE
example         4.12.7    example-admin-kubeconfig         Completed   True        False         The hosted control plane is available
```

## Optional MetalLB Configuration Steps

LoadBalancer type services are required. If MetalLB is in use, here are some example steps
outlining how to configure MetalLB after [installing MetalLB](https://docs.openshift.com/container-platform/4.12/networking/metallb/metallb-operator-install.html).

1. Create a MetalLB instance:

    ```shell
    oc create -f - <<EOF
    apiVersion: metallb.io/v1beta1
    kind: MetalLB
    metadata:
      name: metallb
      namespace: metallb-system
    EOF
    ```

2. Create address pool with an available range of IP addresses within the node network:

    ```shell
    oc create -f - <<EOF
    apiVersion: metallb.io/v1beta1
    kind: IPAddressPool
    metadata:
      name: metallb
      namespace: metallb-system
    spec:
      addresses:
      - 192.168.216.32-192.168.216.122
    EOF
    ```

3. Advertise the address pool using L2 protocol:

    ```shell
    oc create -f - <<EOF
    apiVersion: metallb.io/v1beta1
    kind: L2Advertisement
    metadata:
      name: l2advertisement
      namespace: metallb-system
    spec:
      ipAddressPools:
       - metallb
    EOF
    ```
