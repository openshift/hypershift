# Ingress and DNS configuration

This guide covers how to configure ingress and DNS for KubeVirt-based Hosted
Control Plane (HCP) clusters.

## How KubeVirt ingress works

On KubeVirt HCP clusters, the guest cluster's default IngressController always
uses the `NodePortService` endpoint publishing strategy. This means the guest
cluster's router pods are exposed through a NodePort Service
(`router-nodeport-default` in the `openshift-ingress` namespace) that listens
on dynamically assigned ports on each guest VM's network interface.

There are two modes for routing external traffic to these NodePorts:

| Mode | When it applies | Who manages ingress routing |
|------|----------------|----------------------------|
| **baseDomainPassthrough** (default) | No `baseDomain` specified, or `baseDomainPassthrough` explicitly set to `true` | HyperShift (automatic) |
| **Custom baseDomain** | Explicit `baseDomain` provided at creation time | User (manual) |

### When baseDomainPassthrough is auto-enabled

When creating a KubeVirt HostedCluster **without** specifying a `baseDomain`,
the HyperShift webhook automatically enables `baseDomainPassthrough`:

- If `spec.dns.baseDomain` is empty, the webhook sets
  `spec.platform.kubevirt.baseDomainPassthrough = true`
- If you provide an explicit `baseDomain`, the webhook does **not** enable
  baseDomainPassthrough, and you are responsible for configuring ingress manually

!!! important

    `baseDomainPassthrough` is **immutable** after HostedCluster creation. If
    you create a cluster with a custom `baseDomain` (and therefore without
    baseDomainPassthrough), you cannot enable it later without recreating the
    cluster.

## Default: baseDomainPassthrough

When `baseDomainPassthrough` is enabled (the default when no `baseDomain` is
specified), HyperShift automatically configures all ingress routing
infrastructure on the management cluster. No manual LoadBalancer or DNS setup
is required.

### What HyperShift creates automatically

1. **A wildcard passthrough Route** on the management cluster with
   `TLSTerminationPassthrough` and `WildcardPolicySubdomain`. This Route
   matches all `*.apps.<guest>.<mgmt-apps-domain>` requests and forwards
   them without terminating TLS.

2. **A ClusterIP Service** on the management cluster with an empty selector
   (no pod selector). The Service's target port is set to the guest router's
   HTTPS NodePort.

3. **EndpointSlices** managed by the Machine controller, pointing to the VM's
   machineNetwork IPs (not pod IPs) on the correct NodePort. These are
   automatically updated when VMs are added, removed, or live-migrated.

### Resulting DNS domain

The guest cluster's base domain is auto-detected as a subdomain of the
management cluster's `*.apps` domain. For example:

- Management cluster apps domain: `*.apps.mgmt-cluster.example.com`
- Guest cluster named `guest`: `*.apps.guest.apps.mgmt-cluster.example.com`

### Prerequisites

The management cluster must allow wildcard DNS routes:

```shell
oc patch ingresscontroller -n openshift-ingress-operator default \
  --type=json \
  -p '[{ "op": "add", "path": "/spec/routeAdmission", "value": {wildcardPolicy: "WildcardsAllowed"}}]'
```

!!! note

    When using baseDomainPassthrough, connectivity is limited to HTTPS traffic
    over port 443. Plain HTTP traffic over port 80 will be rejected. This
    limitation only applies to the default ingress behavior, not the custom
    baseDomain configuration described below.

## Custom baseDomain (without baseDomainPassthrough)

When you provide an explicit `baseDomain` at creation time, HyperShift does
**not** enable `baseDomainPassthrough` and does **not** create any ingress
routing infrastructure on the management cluster. You are fully responsible
for configuring:

1. A LoadBalancer Service on the management cluster
2. An EndpointSlice pointing to the VM machineNetwork IPs
3. A wildcard DNS record for `*.apps.<cluster-name>.<baseDomain>`

The traffic flow for this configuration is:

```
Client
  └─> *.apps.<cluster>.<baseDomain>     (DNS wildcard)
       └─> LoadBalancer VIP              (MetalLB / external LB)
            └─> VM machineNetwork IP     (EndpointSlice target)
                 └─> NodePort            (guest router)
                      └─> guest Route    (application)
```

This process involves four steps:

1. Cluster creation
2. LoadBalancer and EndpointSlice creation
3. Wildcard DNS configuration
4. Verification

### Step 1 - Deploy the HostedCluster with a custom baseDomain

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

This creates a HostedCluster with ingress wildcard `*.apps.example.hypershift.lab`.

The HostedCluster will remain in `Partial` progress until the LoadBalancer and
DNS are configured:

```shell linenums="1"
oc get --namespace clusters hostedclusters

NAME            VERSION   KUBECONFIG                       PROGRESS   AVAILABLE   PROGRESSING   MESSAGE
example                   example-admin-kubeconfig         Partial    True        False         The hosted control plane is available
```

### Step 2 - Set up the LoadBalancer and EndpointSlice

!!! warning

    Do **not** use a pod selector (such as `kubevirt.io: virt-launcher`) on the
    LoadBalancer Service. KubeVirt VMs typically have two network interfaces: the
    **pod network** (used by the virt-launcher pod on the management cluster) and
    the **machineNetwork** (the VM's actual network, often on a secondary bridge
    interface). The guest router's NodePort only listens on the machineNetwork
    IPs, not on the pod network IPs. A pod selector resolves to pod network IPs,
    which causes `connection refused` or `http: server gave HTTP response to
    HTTPS client` errors.

    Instead, create a Service with no selector and manually manage an
    EndpointSlice that points to the VM machineNetwork IPs.

!!! note

    If your cluster is on bare metal you may need MetalLB to be able to provision
    functional LoadBalancer services. See the
    [Optional MetalLB Configuration Steps](#optional-metallb-configuration-steps)
    section.

#### 1. Retrieve the guest cluster NodePorts

```shell
export CLUSTER_KUBECONFIG="${CLUSTER_NAME}-kubeconfig"
hcp create kubeconfig --name $CLUSTER_NAME > $CLUSTER_KUBECONFIG

export HTTP_NODEPORT=$(oc --kubeconfig $CLUSTER_KUBECONFIG get services \
  -n openshift-ingress router-nodeport-default \
  -o jsonpath='{.spec.ports[?(@.name=="http")].nodePort}')

export HTTPS_NODEPORT=$(oc --kubeconfig $CLUSTER_KUBECONFIG get services \
  -n openshift-ingress router-nodeport-default \
  -o jsonpath='{.spec.ports[?(@.name=="https")].nodePort}')

echo "HTTP NodePort: $HTTP_NODEPORT"
echo "HTTPS NodePort: $HTTPS_NODEPORT"
```

#### 2. Retrieve the VM machineNetwork IPs

```shell
export HCP_NAMESPACE="clusters-${CLUSTER_NAME}"

oc get vmi -n $HCP_NAMESPACE \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.interfaces[0].ipAddress}{"\n"}{end}'
```

Save the VM IPs for use in the EndpointSlice below. For example:

```
example-workers-abc12-xyz34    192.168.216.50
example-workers-abc12-xyz56    192.168.216.51
```

#### 3. Create the LoadBalancer Service (no selector)

```shell
cat << EOF | oc apply -f -
apiVersion: v1
kind: Service
metadata:
  labels:
    app: ${CLUSTER_NAME}
  name: ${CLUSTER_NAME}-apps
  namespace: ${HCP_NAMESPACE}
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
  type: LoadBalancer
EOF
```

Note that the Service has **no `selector` field**. Traffic routing is handled
by the EndpointSlice created in the next step.

#### 4. Create the EndpointSlice

Replace the IP addresses below with the VM machineNetwork IPs retrieved in
step 2:

```shell
cat << EOF | oc apply -f -
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata:
  name: ${CLUSTER_NAME}-apps-endpoints
  namespace: ${HCP_NAMESPACE}
  labels:
    kubernetes.io/service-name: ${CLUSTER_NAME}-apps
addressType: IPv4
ports:
- name: https-443
  port: ${HTTPS_NODEPORT}
  protocol: TCP
- name: http-80
  port: ${HTTP_NODEPORT}
  protocol: TCP
endpoints:
- addresses:
  - "192.168.216.50"
  - "192.168.216.51"
EOF
```

!!! important

    The EndpointSlice must be updated manually whenever the guest cluster's
    VMs change:

    - **Scaling up**: Add new VM machineNetwork IPs to the EndpointSlice
    - **Scaling down**: Remove decommissioned VM IPs
    - **Live migration**: Update IPs if the VM's machineNetwork address changes

    Run `oc get vmi -n $HCP_NAMESPACE` to retrieve the current VM IPs after
    any scaling or migration event.

### Step 3 - Set up a wildcard DNS record for `*.apps`

Configure a wildcard DNS A record or CNAME that references the LoadBalancer
Service's external IP:

1. Get the external IP:

    ```shell
    export EXTERNAL_IP=$(oc -n $HCP_NAMESPACE get service ${CLUSTER_NAME}-apps \
      -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
    ```

2. Configure a wildcard `*.apps.<cluster_name>.<base_domain>.` DNS entry
   referencing the IP stored in `$EXTERNAL_IP`. The DNS record must be
   routable both from outside the cluster **and from inside the guest VMs**
   (see the [Troubleshooting](#troubleshooting) section for hairpin issues).

    For example, for a cluster named `example` with base domain `hypershift.lab`
    and external IP `192.168.20.30`:

    ```shell
    dig +short test.apps.example.hypershift.lab

    192.168.20.30
    ```

### Step 4 - Verify the HostedCluster status

Once the LoadBalancer and DNS are in place, the HostedCluster progress should
move from `Partial` to `Completed`:

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

## Troubleshooting

### CanaryChecksRepetitiveFailures with custom baseDomain

When using a custom `baseDomain` (without `baseDomainPassthrough`), the ingress
operator may report `Degraded` with errors like:

```
CanaryChecksRepetitiveFailures: Canary route checks for the default ingress
controller are failing. Last 1 error messages:
error sending canary HTTP request: http: server gave HTTP response to HTTPS client
```

or:

```
connection refused
```

#### Diagnostic steps

1. **Verify DNS resolution from inside the guest VMs.** The canary check runs
   from inside the guest cluster, so DNS must resolve correctly from within
   the VMs:

    ```shell
    oc --kubeconfig $CLUSTER_KUBECONFIG debug node/<any-guest-node> -- \
      chroot /host nslookup canary-openshift-ingress-canary.apps.${CLUSTER_NAME}.${BASE_DOMAIN}
    ```

    Compare this IP with the LoadBalancer VIP. If they differ, DNS is
    misconfigured.

2. **Verify the LoadBalancer endpoints use machineNetwork IPs, not pod IPs.**
   Check the EndpointSlice:

    ```shell
    oc get endpointslice -n $HCP_NAMESPACE -l kubernetes.io/service-name=${CLUSTER_NAME}-apps -o yaml
    ```

    The IP addresses in the EndpointSlice must be the VM machineNetwork IPs
    (the same IPs returned by `oc get vmi`), **not** the virt-launcher pod
    IPs. If the EndpointSlice contains pod network IPs (typically in a
    different CIDR than the machineNetwork), the guest router NodePort will
    not be reachable and connections will be refused.

3. **Test NodePort reachability from inside the guest.** Curl the LoadBalancer
   VIP from within a guest VM:

    ```shell
    oc --kubeconfig $CLUSTER_KUBECONFIG debug node/<any-guest-node> -- \
      chroot /host curl -vk --connect-timeout 5 https://<EXTERNAL_IP>:443
    ```

    If this returns `connection refused` but the same curl works from outside
    the guest VMs, the issue is **DNS hairpin** — the VMs are sending traffic
    to a VIP that routes back to themselves, but the return path is broken.
    Common fixes include enabling SNAT on the load balancer or configuring
    split-horizon DNS so that guest VMs resolve `*.apps` directly to their
    own machineNetwork IPs instead of the external VIP.
