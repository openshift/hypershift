---
title: Configure Custom Ingress for KubeVirt HCP
---

# Configure Custom Ingress for KubeVirt HCP

This recipe walks through deploying a KubeVirt-based Hosted Control Plane with
a custom `baseDomain` (without `baseDomainPassthrough`) on a bare-metal
management cluster using MetalLB for LoadBalancer services.

This is the typical setup when the guest cluster needs its own DNS domain
separate from the management cluster's `*.apps` domain, and an external load
balancer (F5, HAProxy, etc.) or MetalLB handles VIP advertisement.

## Prerequisites

- A bare-metal OpenShift management cluster with KubeVirt (OpenShift
  Virtualization) installed
- MetalLB Operator installed (see
  [Optional MetalLB Configuration Steps](../../how-to/kubevirt/ingress-and-dns.md#optional-metallb-configuration-steps))
- A DNS zone you control for the custom `baseDomain`
- VM network configured with a secondary bridge interface (the VMs must have
  machineNetwork connectivity, not just pod network)

## Environment Variables

Set these once — all subsequent commands reference them:

```shell
export CLUSTER_NAME=my-kubevirt-hcp
export BASE_DOMAIN=example.com
export HCP_NAMESPACE="clusters-${CLUSTER_NAME}"
export PULL_SECRET="$HOME/pull-secret"
export MEM="6Gi"
export CPU="2"
export WORKER_COUNT="2"
```

## Step 1 — Create the HostedCluster

```shell
hcp create cluster kubevirt \
  --name $CLUSTER_NAME \
  --node-pool-replicas $WORKER_COUNT \
  --pull-secret $PULL_SECRET \
  --memory $MEM \
  --cores $CPU \
  --base-domain $BASE_DOMAIN
```

Because `--base-domain` is provided, the webhook does **not** enable
`baseDomainPassthrough`. The cluster will stay in `Partial` progress until
ingress is manually configured.

## Step 2 — Configure MetalLB

### 2.1 — Create the MetalLB instance

```yaml
apiVersion: metallb.io/v1beta1
kind: MetalLB
metadata:
  name: metallb
  namespace: metallb-system
```

### 2.2 — Create the IPAddressPool

Adjust the address range to match available IPs on your bare-metal network:

```yaml
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: hcp-ingress-pool
  namespace: metallb-system
spec:
  addresses:
  - 192.168.216.32-192.168.216.122
```

### 2.3 — Create the L2Advertisement

If your network uses a specific bridge interface (e.g., `br-sdn`), add
`interfaces` and `nodeSelectors` as needed:

```yaml
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: hcp-ingress-l2
  namespace: metallb-system
spec:
  ipAddressPools:
  - hcp-ingress-pool
```

## Step 3 — Retrieve the Guest Router NodePorts

Wait for the guest cluster to have running worker nodes, then extract the
dynamically assigned NodePorts:

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

## Step 4 — Retrieve VM machineNetwork IPs

```shell
oc get vmi -n $HCP_NAMESPACE \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.interfaces[0].ipAddress}{"\n"}{end}'
```

Example output:

```
my-kubevirt-hcp-workers-abc12-xyz34    192.168.216.50
my-kubevirt-hcp-workers-abc12-xyz56    192.168.216.51
```

!!! warning

    Use the **machineNetwork IPs** (the VM's network interface on the
    secondary bridge), not the virt-launcher pod IPs. The guest router's
    NodePort only listens on machineNetwork IPs. Using pod IPs causes
    `connection refused` errors. See
    [Ingress and DNS - Troubleshooting](../../how-to/kubevirt/ingress-and-dns.md#troubleshooting)
    for details.

## Step 5 — Create the LoadBalancer Service (no selector)

```yaml
apiVersion: v1
kind: Service
metadata:
  labels:
    app: ${CLUSTER_NAME}
  name: ${CLUSTER_NAME}-apps-ingress
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
```

The Service has **no `selector`**. Traffic routing is handled entirely by the
EndpointSlice below.

## Step 6 — Create the EndpointSlice

Replace IP addresses with the values from Step 4:

```yaml
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata:
  name: ${CLUSTER_NAME}-apps-ingress
  namespace: ${HCP_NAMESPACE}
  labels:
    kubernetes.io/service-name: ${CLUSTER_NAME}-apps-ingress
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
```

## Step 7 — Configure Wildcard DNS

Get the VIP assigned by MetalLB:

```shell
export EXTERNAL_IP=$(oc -n $HCP_NAMESPACE get service ${CLUSTER_NAME}-apps-ingress \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
echo "LoadBalancer VIP: $EXTERNAL_IP"
```

Create a wildcard DNS record:

```
*.apps.my-kubevirt-hcp.example.com.  IN  A  <EXTERNAL_IP>
```

Verify:

```shell
dig +short test.apps.${CLUSTER_NAME}.${BASE_DOMAIN}
```

!!! important

    DNS must resolve correctly both **externally** and **from inside the guest
    VMs**. If the VMs resolve `*.apps` to the MetalLB VIP but the return
    traffic path is broken (hairpin), the ingress canary checks will fail.
    Common fixes: enable SNAT on the MetalLB L2 advertisement, or use
    split-horizon DNS so guest VMs resolve directly to their own machineNetwork
    IPs.

!!! tip

    As an alternative to split-horizon DNS, you can inject custom DNS
    configuration directly into the KubeVirt VMs using a JSON patch on the
    NodePool. This overrides the VM's DNS resolver so it points to a
    nameserver that returns the correct IPs from inside the guest network:

    ```yaml
    apiVersion: hypershift.openshift.io/v1beta1
    kind: NodePool
    metadata:
      name: my-kubevirt-hcp
      namespace: clusters
    spec:
      platform:
        kubevirt:
          jsonPatch:
          - op: add
            path: /spec/template/spec/dnsPolicy
            value: "None"
          - op: add
            path: /spec/template/spec/dnsConfig
            value:
              nameservers:
              - "10.0.0.53"
              searches:
              - "example.com"
    ```

    See [Configuring VMs with JSON Patch](../../how-to/kubevirt/configuring-vm-with-jsonpatch.md)
    for full details on the JSON patch mechanism.

## Step 8 — Verify

Check HostedCluster progresses to `Completed`:

```shell
oc get --namespace clusters hostedclusters
```

Expected output:

```
NAME              VERSION   KUBECONFIG                         PROGRESS    AVAILABLE   PROGRESSING   MESSAGE
my-kubevirt-hcp   4.17.0    my-kubevirt-hcp-admin-kubeconfig   Completed   True        False         The hosted control plane is available
```

Verify ingress from outside:

```shell
curl -vk https://console-openshift-console.apps.${CLUSTER_NAME}.${BASE_DOMAIN}
```

Check the ingress operator is not degraded inside the guest:

```shell
oc --kubeconfig $CLUSTER_KUBECONFIG get co ingress
```

## Maintenance

The EndpointSlice is **not** automatically managed. Update it when:

| Event | Action |
|-------|--------|
| **Scale up** (new VMs) | Add new VM machineNetwork IPs to the EndpointSlice |
| **Scale down** | Remove decommissioned VM IPs |
| **Live migration** | Update IPs if machineNetwork address changed |

Quick command to get current VM IPs:

```shell
oc get vmi -n $HCP_NAMESPACE \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.interfaces[0].ipAddress}{"\n"}{end}'
```

## Traffic Flow

```
Client
  └─> *.apps.my-kubevirt-hcp.example.com     (DNS wildcard)
       └─> MetalLB VIP (e.g. 192.168.216.32) (L2 advertisement)
            └─> VM machineNetwork IP          (EndpointSlice)
                 └─> NodePort (e.g. 31245)    (guest router)
                      └─> guest Route         (application)
```
