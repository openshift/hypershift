# Ingress and DNS configuration

By default, the HyperShift operator will configure the KubeVirt platform guest
cluster's ingress and DNS behavior to reuse what is provided by the underlying
infra cluster that the KubeVirt VMs are running on. This section describes
that default behavior in greater detail as well as information on advanced usage
options.

## Default Ingress and DNS Behavior

Every OpenShift cluster comes setup with a default application ingress
controller which is expected to have an wildcard DNS record associated with it.
By default, guest clusters created using the Hypershift KubeVirt provider
will automatically become a subdomain of the underlying OCP cluster that
the KubeVirt VMs run on.

For example, if an OCP cluster has a default ingress DNS entry of
`*.apps.mgmt-cluster.example.com`, then the default ingress of a KubeVirt
guest cluster named `guest` running on that underlying OCP cluster will
be `*.apps.guest.apps.mgmt-cluster.example.com`.

!!! note

    For this default ingress DNS to work properly, the underlying cluster
    hosting the KubeVirt VMs must allow wildcard DNS routes. This can be
    configured using the following cli command. ```oc patch ingresscontroller -n openshift-ingress-operator default --type=json -p '[{ "op": "add", "path": "/spec/routeAdmission", "value": {wildcardPolicy: "WildcardsAllowed"}}]'```

!!! note

    When using the default guest cluster ingress, connectivity is limited to HTTPS
    traffic over port 443. Plain HTTP traffic over port 80 will be rejected. This
    limitation only applies to the default ingress behavior and not the custom ingress
    behavior where manual creation of an ingress LoadBalancer and DNS is performed.

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

hcp create cluster kubevirt \
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
hcp create kubeconfig --name $CLUSTER_NAME > $CLUSTER_NAME-kubeconfig
```

```shell
oc --kubeconfig $CLUSTER_NAME-kubeconfig get co

NAME                                       VERSION   AVAILABLE   PROGRESSING   DEGRADED   SINCE   MESSAGE
console                                    4.14.0    False       False         False      30m     RouteHealthAvailable: failed to GET route (https://console-openshift-console.apps.example.hypershift.lab): Get "https://console-openshift-console.apps.example.hypershift.lab": dial tcp: lookup console-openshift-console.apps.example.hypershift.lab on 172.31.0.10:53: no such host
.
.
.
ingress                                    4.14.0    True        False         True       28m     The "default" ingress controller reports Degraded=True: DegradedConditions: One or more other status conditions indicate a degraded state: CanaryChecksSucceeding=False (CanaryChecksRepetitiveFailures: Canary route checks for the default ingress controller are failing)
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
example         4.14.0    example-admin-kubeconfig         Completed   True        False         The hosted control plane is available
```

## Optional MetalLB Configuration Steps

LoadBalancer type services are required. If MetalLB is in use, here are some example steps
outlining how to configure MetalLB after [installing MetalLB using CLI](https://docs.redhat.com/en/documentation/openshift_container_platform/4.19/html/networking_operators/metallb-operator#nw-metallb-installing-operator-cli_metallb-operator-install).

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
