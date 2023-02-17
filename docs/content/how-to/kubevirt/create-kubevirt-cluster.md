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

The command below builds latest hypershift cli tool from source and places the
cli tool within the /usr/local/bin directory.

```shell
podman run --rm --privileged -it -v \
$PWD:/output docker.io/library/golang:1.18 /bin/bash -c \
'git clone https://github.com/openshift/hypershift.git && \
cd hypershift/ && \
make hypershift && \
mv bin/hypershift /output/hypershift'

sudo mv $PWD/hypershift /usr/local/bin
```

Use the hypershift cli tool to install the HyperShift operator into the
management cluster.

```shell
hypershift install
```

## Create a HostedCluster

Once all the [prerequisites](#prerequisites) are met, and the HyperShift
operator is installed, it is now possible to create a guest cluster.

Below is an example of how to create a guest cluster using environment
variables and the `hypershift` cli tool.

```shell linenums="1"
export CLUSTER_NAME=example
export PULL_SECRET="$HOME/pull-secret"
export MEM="6Gi"
export CPU="2"
export WORKER_COUNT="2"

hypershift create cluster kubevirt \
--name $CLUSTER_NAME \
--node-pool-replicas=$WORKER_COUNT \
--pull-secret $PULL_SECRET \
--memory $MEM \
--cores $CPU
```

!!! note

    A default NodePool will be created for the cluster with 2 vm worker replicas
    per the `--node-pool-replicas` flag.

A guest cluster backed by KubeVirt virtual machines typically takes around 10-15
minutes to fully provision. The status of the guest cluster can be seen by
viewing the corresponding HostedCluster resource. For example, the output below
reflects what a fully provisioned HostedCluster object looks like.

```shell linenums="1"
oc get --namespace clusters hostedclusters
NAMESPACE   NAME      VERSION   KUBECONFIG                 PROGRESS    AVAILABLE   PROGRESSING   MESSAGE
clusters    example   4.12.2    example-admin-kubeconfig   Completed   True        False         The hosted control plane is available
```

CLI access to the guest cluster is gained by retrieving the guest cluster's
kubeconfig. Below is an example of how to retrieve the guest cluster's
kubeconfig using the hypershift cli.

```shell
hypershift create kubeconfig --name $CLUSTER_NAME > $CLUSTER_NAME-kubeconfig
```

## Delete a HostedCluster

To delete a HostedCluster:

```shell
hypershift destroy cluster kubevirt --name $CLUSTER_NAME
```

## Optional MetalLB Configuration Steps

A load balancer is required. If MetalLB is in use, here are some example steps
outlining how to configure MetalLB after [installing MetalLB](https://docs.openshift.com/container-platform/4.12/networking/metallb/metallb-operator-install.html).

**Step 1. Create a MetalLB instance**
```shell
oc create -f - <<EOF
apiVersion: metallb.io/v1beta1
kind: MetalLB
metadata:
  name: metallb
  namespace: metallb-system
EOF
```

**Step 2. Create address pool with an available range of IP addresses within the node network**
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

**Step 3. Advertise the address pool using L2 protocol**
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

This process involves three steps.

**Step 1. Cluster Creation**

Create Hypershift KubeVirt cluster with a custom base domain you control. This
can be achieved using the `--base-domain` cli argument during cluster creation.

Below is an example.

```shell linenums="1"
export CLUSTER_NAME=example
export PULL_SECRET="$HOME/pull-secret"
export BASE_DOMAIN="example.com"

hypershift create cluster kubevirt \
--name $CLUSTER_NAME \
--node-pool-replicas=2 \
--pull-secret $PULL_SECRET \
--base-domain $BASE_DOMAIN
```

**Step 2. LoadBalancer Creation**

Create a LoadBalancer Service to route ingress traffic to the KubeVirt VMs
that are acting as nodes for the guest cluster.

The guest cluster must be inspected in order to learn what port to use as the
target port when routing to the KubeVirt VMs. The target port can be discovered
by using the kubeconfig for the new Hypershift KubeVirt cluster to retrieve the
default router's NodePort service.

Below is a combination of cli commands that will automatically detect the target
port of the guest cluster and store it in an environment variable.

```shell
hypershift create kubeconfig --name $CLUSTER_NAME > $CLUSTER_NAME-kubeconfig
export EXTERNAL_IP=$(oc --kubeconfig $CLUSTER_NAME-kubeconfig get services -n openshift-ingress router-nodeport-default -o wide --no-headers | sed -E 's|.*443:(.....).*$|\1|' |  tr -d '[:space:])
```

After the target port is discovered, create a LoadBalancer service to route
traffic to the guest cluster's KubeVirt VMs.

```shell linenums="1"
export CLUSTER_NAME=example
export CLUSTER_NAMESPACE=clusters-${CLUSTER_NAME}

cat << EOF > apps-LB-service.yaml
apiVersion: v1
kind: Service
metadata:
  labels:
    app: ${CLUSTER_NAME}
  name: ${CLUSTER_NAME}
  namespace: ${CLUSTER_NAMESPACE}
spec:
  ports:
  - name: https-443
    port: 443
    protocol: TCP
    targetPort: ${HTTPS_NODEPORT}
  selector:
    kubevirt.io: virt-launcher
  type: LoadBalancer
EOF

oc create -f apps-LB-service.yaml
```

**Step 3. Configure Wildcard DNS**

Once the LoadBalancer is created, configure a wildcard DNS A record or CNAME
that references the LoadBalancer service's external IP.

Get the LoadBalancer's external IP.

```shell
export EXTERNAL_IP=$(oc get service -n $KUBEVIRT_CLUSTER_NAMESPACE $KUBEVIRT_CLUSTER_NAME  | grep $KUBEVIRT_CLUSTER_NAME| awk '{ print $4 }' | tr -d '[:space:]')
```

Configure a wildcard `*.apps.<cluster_name>.<base_domain>.` DNS entry
referencing the IP stored in the $EXTERNAL_IP environment variable that is
routable both internally and externally of the cluster.

## Scaling an existing NodePool

Manually scale a NodePool using the `oc scale` command:

```shell linenums="1"
NODEPOOL_NAME=${CLUSTER_NAME}-work
NODEPOOL_REPLICAS=5

oc scale nodepool/$NODEPOOL_NAME --namespace clusters --replicas=$NODEPOOL_REPLICAS
```

## Adding Additional NodePools

Create additional NodePools for a guest cluster by specifying a name, number of
replicas, and any additional information such as memory and cpu requirements.

Create a NodePool:

```shell linenums="1"
export NODEPOOL_NAME=${CLUSTER_NAME}-workers
export WORKER_COUNT="2"
export MEM="6Gi"
export CPU="2"

hypershift create nodepool kubevirt \
  --cluster-name $CLUSTER_NAME \
  --name $NODEPOOL_NAME \
  --node-count $WORKER_COUNT \
  --memory $MEM \
  --cores $CPU
```

Check the status of the NodePool by listing `nodepool` resources in the `clusters`
namespace:

```shell
oc get nodepools --namespace clusters
```
