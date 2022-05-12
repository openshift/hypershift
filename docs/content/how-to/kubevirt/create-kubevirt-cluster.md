# Create a Kubevirt cluster

Install a nested OCP cluster running on VMs within a management OCP cluster

## Prerequisites

* The HyperShift CLI (`hypershift`).

    Install it using Go 1.18+:
       ```shell
       go get -u github.com/openshift/hypershift@latest
       ```

* Admin access to an OpenShift cluster (version 4.8+) specified by the `KUBECONFIG` environment variable.
* The management OCP cluster must have Openshift Virtualization installed on it (More info:
  https://docs.openshift.com/container-platform/4.10/virt/about-virt.html?extIdCarryOver=true&sc_cid=7013a0000026OSTAA2)
* The management OCP cluster must have valid storage class (More info:
  https://docs.openshift.com/container-platform/4.10/post_installation_configuration/storage-configuration.html)
* The OpenShift CLI (`oc`) or Kubernetes CLI (`kubectl`).
* A valid [pull secret](https://cloud.redhat.com/openshift/install/aws/installer-provisioned) file for the `quay.io/openshift-release-dev` repository.
* A base domain that can be [configured for ingress](#handling-ingress-and-dns)

## Before you begin

Install HyperShift into the management cluster:

```shell linenums="1"
hypershift install
```

## Create a HostedCluster

Create a new cluster, specifying the base domain and the pull secret
provided in the [Prerequisites](#prerequisites):

```shell linenums="1"
export CLUSTER_NAME=example
export BASE_DOMAIN=example.com
export PULL_SECRET="$HOME/pull-secret"

hypershift create cluster kubevirt \
--name $CLUSTER_NAME \
--base-domain $BASE_DOMAIN \
--node-pool-replicas=3 \
--pull-secret $PULL_SECRET \
```

!!! note

    A default NodePool will be created for the cluster with 3 replicas per the
    `--node-pool-replicas` flag.

After a few minutes, check the `hostedclusters` resources in the `clusters`
namespace and when ready it will look similar to the following:

```
oc get --namespace clusters hostedclusters
NAME      VERSION   KUBECONFIG                 AVAILABLE
example   4.10.0     example-admin-kubeconfig   True
```

Eventually the cluster's kubeconfig will become available and can be printed to
standard out using the `hypershift` CLI:

```shell
hypershift create kubeconfig
```
## Handling Ingress and DNS

Every OpenShift cluster comes setup with a default application ingress
controller which is expected have an external DNS record associated with it.

For example, a Hypershift cluster named `my-cluster` with the base domain
`example.com` is created, then it is expected that the wildcard domain
`*apps.my-cluster.example.com` is routable.

There are two ways to set this up.
- Option 1: reuse the management cluster's wildcard DNS routing and make the
Hypershift KubeVirt cluster a subdomain of the management cluster.
- Option 2: Setup a new LoadBalancer and wildcard DNS record for the `*.apps`.

### Option 1: Hypershift KubeVirt cluster is a Subdomain of MGMT Cluster

Since a Hypershift KubeVirt cluster runs within an existing OpenShift
management cluster, a simple way to configure ingress and DNS for the Hypershift
cluster is to re-use the wildcard DNS `*.apps.<cluster name>.<base domain>`
that already exists for the management cluster.

For example, if the management cluster has the base domain `example.com` with
the cluster name `mgmt-cluster` then the management cluster's wildcard DNS
record will look like this, `*.apps.mgmt-cluster.example.com`. Now if we use
the management cluster's application DNS entry `apps.mgmt-cluster.example.com`
as the base domain for a Hypershift KubeVirt cluster called `kv-cluster` then
the resulting application DNS for the Hypershift cluster becomes
 `*.apps.kv-cluster.apps.mgmt-cluster.example.com` which is a subdomain of the
management cluster's DNS entry.

**Step 1**
Enable wildcard routes on the management cluster.

`oc patch ingresscontroller -n openshift-ingress-operator default --type=json -p '[{ "op": "add", "path": "/spec/routeAdmission", "value": {wildcardPolicy: "WildcardsAllowed"}}]'`

**Step 2**
Create Hypershift KubeVirt cluster with the base domain `apps.<mgmt cluster>.<mgmt cluster base domain>`

If the management cluster is called `mgmt-cluster` and it has the base domain
`example.com` then the Hypershift base domain should be
`apps.mgmt-cluster.example.com`. This would be the `--base-domain` cli arg
passed into the hypershift binary during cluster creation.

**Step 3**

Create an OCP route and ClusterIP service that routes application
traffic through the management cluster into the Hypershift KubeVirt VMs.

First detect the Hypershift KubeVirt cluster's ingress NodePort. This requires
using the kubeconfig for the new Hypershift KubeVirt cluster to retrieve
the application NodePort service in order to determine what port to route
ingress traffic to.

```
hypershift create kubeconfig > test-kubeconfig
export HTTPS_NODEPORT=$(oc --kubeconfig test-kubeconfig get services -n openshift-ingress router-nodeport-default -o wide | awk '{print $5}' | awk -F "443:" '{print $2}' | awk -F "/" '{print $1}' | tr -d '[:space:]')
```

Alter these environment variables to fit your environment.
```
export KUBEVIRT_CLUSTER_NAME=kv-cluster
export KUBEVIRT_CLUSTER_NAMESPACE=clusters-kv-cluster
export KUBEVIRT_CLUSTER_BASE_DOMAIN=${KUBEVIRT_CLUSTER_NAME}.apps.mgmt-cluster.example.com
```

Post an OCP Route and Service on the management cluster which routes management
cluster ingress traffic to the Hypershift KubeVirt cluster ingress service's
port on the KubeVirt VMs

```
cat << EOF > apps-ingress-service.yaml
apiVersion: v1
kind: Service
metadata:
  labels:
    app: ${KUBEVIRT_CLUSTER_NAME}
  name: apps-ingress
  namespace: ${KUBEVIRT_CLUSTER_NAMESPACE}
spec:
  internalTrafficPolicy: Cluster
  ipFamilies:
  - IPv4
  ipFamilyPolicy: SingleStack
  ports:
  - name: https-443
    port: 443
    protocol: TCP
    targetPort: $HTTPS_NODEPORT
  selector:
    kubevirt.io: virt-launcher
  sessionAffinity: None
  type: ClusterIP
EOF

cat << EOF > apps-ingress-subdomain-route-443.yaml
apiVersion: route.openshift.io/v1
kind: Route
metadata:
  name: ${KUBEVIRT_CLUSTER_NAME}-443
  namespace: ${KUBEVIRT_CLUSTER_NAMESPACE}
spec:
  host: data.apps.$KUBEVIRT_CLUSTER_BASE_DOMAIN
  wildcardPolicy: Subdomain
  tls:
    termination: passthrough
  port:
    targetPort: https-443
  to:
    kind: Service
    name: apps-ingress
    weight: 100
EOF

oc create -f apps-ingress-service.yaml
oc create -f apps-ingress-subdomain-route-443.yaml
```

Once the OCP Route and Service are created, the default ingress for the Hypershift
KubeVirt cluster should be routable.

### Option 2: New LoadBalancer and DNS Entry

This option requires configuring a new LoadBalancer service that routes
to the KubeVirt VMs as well as assign a wildcard DNS entry to the
LoadBalancer's ip address.

**Step 1**

Create Hypershift KubeVirt cluster using a base domain you control.

**Step 2**

Create a LoadBalancer Service that routes ingress traffic to the KubeVirt VMs.

Detect the Hypershift KubeVirt cluster's ingress NodePort. This requires
using the kubeconfig for the new Hypershift KubeVirt cluster to retrieve
the application NodePort service in order to determine what port to route
ingress traffic to.

```
hypershift create kubeconfig > test-kubeconfig
export HTTPS_NODEPORT=$(oc --kubeconfig test-kubeconfig get services -n openshift-ingress router-nodeport-default -o wide | awk '{print $5}' | awk -F "443:" '{print $2}' | awk -F "/" '{print $1}' | tr -d '[:space:]')
```

Alter these environment variables to fit your environment.
```
export KUBEVIRT_CLUSTER_NAME=kv-cluster
export KUBEVIRT_CLUSTER_NAMESPACE=clusters-kv-cluster
```

Post an OCP LoadBalancer Service on the management cluster that routes ingress
traffic into the KubeVirt VMs.
```
cat << EOF > apps-LB-service.yaml
apiVersion: v1
kind: Service
metadata:
  labels:
    app: ${KUBEVIRT_CLUSTER_NAME}
  name: ${KUBEVIRT_CLUSTER_NAME}-apps
  namespace: ${KUBEVIRT_CLUSTER_NAMESPACE}
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

**Step 3**

Configure wildcard DNS A record or CNAME that references the LoadBalancer
Service's external IP.

Get the external IP.
```
export EXTERNAL_IP=$(oc get service -n $KUBEVIRT_CLUSTER_NAMESPACE $KUBEVIRT_CLUSTER_NAME  | grep $KUBEVIRT_CLUSTER_NAME| awk '{ print $4 }' | tr -d '[:space:]')
```

Configure a wildcard `*.apps.<cluster_name>.<base_domain>.` DNS entry
referencing the IP stored in $EXTERNAL_IP that is routable both internally
and externally of the cluster.

## Add NodePools

Create additional NodePools for a cluster by specifying a name, number of replicas
and additional information such as instance type.

Create a NodePool:

```shell linenums="1"
export NODEPOOL_NAME=${CLUSTER_NAME}-work
export NODEPOOL_REPLICAS=2

hypershift create nodepool kubevirt \
  --cluster-name $CLUSTER_NAME \
  --name $NODEPOOL_NAME \
  --node-count $NODEPOOL_REPLICAS \
```

Check the status of the NodePool by listing `nodepool` resources in the `clusters`
namespace:

```shell
oc get nodepools --namespace clusters
```

## Scale a NodePool

Manually scale a NodePool using the `oc scale` command:

```shell linenums="1"
NODEPOOL_NAME=${CLUSTER_NAME}-work
NODEPOOL_REPLICAS=5

oc scale nodepool/$NODEPOOL_NAME --namespace clusters --replicas=$NODEPOOL_REPLICAS
```

## Delete a HostedCluster

To delete a HostedCluster:

```shell
hypershift destroy cluster kubevirt --name $CLUSTER_NAME
```
