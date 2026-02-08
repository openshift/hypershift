# Primary UDN Support for HyperShift Hosted Clusters

This document lists all code changes made to the HyperShift codebase to enable Primary User-Defined Network (UDN) support for KubeVirt hosted clusters.

## What is Primary UDN?

Primary UDN is an OVN-Kubernetes feature that isolates a namespace's pods on a separate network from the management cluster's default network. For HyperShift:
- Control plane pods get Primary UDN IPs as their default interface
- Worker VMs get Primary UDN IPs
- Both are isolated from the management cluster's default network

---

## Setup Requirements for Primary UDN Hosted Clusters

To enable Primary UDN for a HyperShift hosted cluster, the following configuration must be in place **before** creating the HostedCluster:

### 1. Namespace Configuration

The hosted cluster namespace must have the Primary UDN annotation:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: clusters-<cluster-name>
  annotations:
    k8s.ovn.org/primary-user-defined-network: hcp-<cluster-name>
```

### 2. UserDefinedNetwork Resource

A UserDefinedNetwork resource with `role: Primary` must be created in the namespace:

```yaml
apiVersion: k8s.ovn.org/v1
kind: UserDefinedNetwork
metadata:
  name: hcp-<cluster-name>
  namespace: clusters-<cluster-name>
spec:
  topology: Layer2
  layer2:
    role: Primary        # This makes it a Primary UDN
    subnets:
      - 10.150.0.0/16   # IP range for pods/VMs
    ipam:
      mode: Enabled
      lifecycle: Persistent
```

### 3. HostedCluster Platform Configuration

The HostedCluster must be configured with the KubeVirt platform and include a JSONPatch annotation to modify the VM template for Primary UDN compatibility:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: <cluster-name>
  namespace: clusters-<cluster-name>
  annotations:
    hypershift.openshift.io/kubevirt-vm-jsonpatch: |
      - op: replace
        path: /spec/template/spec/domain/devices/interfaces/0/bridge
        value: null
      - op: add
        path: /spec/template/spec/domain/devices/interfaces/0/binding
        value:
          name: l2bridge
      - op: remove
        path: /spec/template/metadata/annotations/kubevirt.io~1allow-pod-bridge-network-live-migration
spec:
  platform:
    type: KubeVirt
    kubevirt:
      # ... other kubevirt config
```

**What These JSONPatches Do**:

1. **Remove bridge binding**: Removes the default `bridge` network binding (incompatible with Primary UDN)
2. **Add l2bridge binding**: Adds `l2bridge` binding which gives VMs direct access to the OVN L2 network and allows them to get their own Primary UDN IP
3. **Remove live migration annotation**: Removes `kubevirt.io/allow-pod-bridge-network-live-migration` annotation which causes OVN-K CNI to skip IP configuration on eth0, breaking route discovery for l2bridge on Primary UDN

**Why These Changes Are Required**:
- `bridge` binding shares the pod's IP with the VM - doesn't work with Primary UDN's dual-network model
- `l2bridge` binding gives the VM direct access to the OVN L2 network, allowing it to get its own Primary UDN IP
- The live migration annotation interferes with Primary UDN networking setup and must be removed

---

## Testing with Custom Images

Since the code changes are not yet in official releases, custom operator images must be deployed for testing:

### 1. Custom Control Plane Operator Image

The HostedCluster needs an annotation to use a custom CPO image with the UDN fixes:

```bash
oc annotate hostedcluster <cluster-name> -n clusters \
  hypershift.openshift.io/control-plane-operator-image=<custom-cpo-image> \
  --overwrite
```

**Example**:
```bash
oc annotate hostedcluster test-primary-udn -n clusters \
  hypershift.openshift.io/control-plane-operator-image=quay.io/ramlavi/hypershift-control-plane-operator:udn-fix-20251130 \
  --overwrite
```

**What This Does**:
- Tells the hypershift-operator to use your custom CPO image instead of the default
- The CPO pod will automatically restart and apply changes from the custom image
- This image contains the etcd statefulset fix, ignition server certificate fix, and ignition endpoint logic

### 2. Custom KubeVirt CAPI Provider Image (since I don't have access to the capi image URL)

The hypershift-operator deployment needs an environment variable override for the KubeVirt CAPI provider:

```bash
oc set env deployment/operator -n hypershift \
  IMAGE_KUBEVIRT_CAPI_PROVIDER=<custom-capi-provider-image>
```

**Example - Use image cloned from the original capi image**:
```bash
oc set env deployment/operator -n hypershift \
  IMAGE_KUBEVIRT_CAPI_PROVIDER=quay.io/ramlavi/cluster-api-provider-kubevirt:4.18
```

**What This Does**:
- Overrides the default KubeVirt CAPI provider image used by the operator
- Required if there are any CAPI provider changes needed for Primary UDN support
- The operator will restart to pick up the new environment variable

### 3. Custom HyperShift Operator Image

If there are changes to the hypershift-operator itself (network policies, ignition endpoint detection):

```bash
oc set image deployment/operator -n hypershift "*=<custom-operator-image>"
```

**Example**:
```bash
oc set image deployment/operator -n hypershift "*=quay.io/ramlavi/hypershift-operator:udn-fix-20251130"
```

**What This Does**:
- Updates all containers in the operator deployment to use the custom image
- Required for testing network policy changes and Primary UDN detection logic

---

## Problem Statement

Even with the above setup in place, without the code changes below, HyperShift hosted clusters cannot use Primary UDN because:
1. Worker VMs cannot reach the ignition server during boot (wrong endpoint URL)
2. TLS certificate verification fails (wrong CA and server certificates)
3. Network policies block required service access (DNS, ClusterIP services)
4. Operator lacks permissions for new Kubernetes resources

---

## Code Changes

### Change 1: ETCD EndpointSlice Mirroring (Manual Workaround)

**What Broke**:
After the hosted cluster is created and etcd starts, control plane components like kube-apiserver fail to connect to etcd with connection timeout errors.

**Root Cause**:
Headless services (services without a ClusterIP) don't get automatic EndpointSlice mirroring by OVN-Kubernetes for Primary UDN networks because:
- There's no ClusterIP for OVN-K to mirror/program
- It's expected that the operator managing the headless service (in this case HyperShift) generates the appropriate EndpointSlices

This causes:
- DNS lookups for `etcd-client.clusters-<cluster-name>.svc` return the default network IP (e.g., `10.128.x.x`)
- Control plane pods using Primary UDN cannot reach etcd at the default network IP (infrastructure-locked)
- kube-apiserver and other components fail to start

**What Needed to Change**:
Manual mirror EndpointSlices must be created for `etcd-client` and `etcd-discovery` services that point to etcd's UDN IP instead of the default network IP. The original EndpointSlices must be "orphaned" (removed from DNS) so only the UDN IP is returned.

**Code/Workaround Required**:

This is a **manual workaround** applied after cluster creation (automated via script in the spike, but not part of HyperShift code):

1. Extract the UDN IP from the etcd pod:
```bash
oc -n clusters-<cluster-name> get pod etcd-0 \
  -o jsonpath='{.metadata.annotations.k8s\.v1\.cni\.cncf\.io/network-status}' | \
  jq -r '.[] | select(.name == "ovn-kubernetes" and .default == true) | .ips[0]'
```

2. Create mirror EndpointSlices with special OVN-K labels pointing to the UDN IP:
```yaml
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata:
  name: etcd-client-hcp-<cluster-name>-mirror
  namespace: clusters-<cluster-name>
  labels:
    endpointslice.kubernetes.io/managed-by: manual-workaround
    kubernetes.io/service-name: etcd-client
    k8s.ovn.org/service-name: etcd-client
  ownerReferences:
  - apiVersion: v1
    kind: Service
    name: etcd-client
    uid: <etcd-client-service-uid>
addressType: IPv4
endpoints:
- addresses:
  - 10.150.0.x  # UDN IP instead of default 10.128.x.x
  conditions:
    ready: true
  targetRef:
    kind: Pod
    name: etcd-0
    uid: <etcd-0-pod-uid>
ports:
- name: etcd-client
  port: 2379
  protocol: TCP
```

3. "Orphan" the original EndpointSlice by removing its labels:
```bash
# Stop the EndpointSlice controller from managing it
oc -n clusters-<cluster-name> label endpointslice <original-eps-name> \
  endpointslice.kubernetes.io/managed-by-

# Hide it from DNS (so only the mirror with UDN IP is used)
oc -n clusters-<cluster-name> label endpointslice <original-eps-name> \
  kubernetes.io/service-name-
```

4. Repeat for `etcd-discovery` service

**Impact**: 
Without this workaround, the hosted control plane cannot start because kube-apiserver and other control plane components cannot reach etcd.

**Note**: 
This is a temporary workaround. Since headless services have no ClusterIP, OVN-Kubernetes cannot mirror them automatically - it's the responsibility of the operator (HyperShift) to create the appropriate EndpointSlices with the correct UDN IPs.

---

### Change 2: ETCD Listen on All Interfaces for KubeVirt

**What Broke**:
Even with the mirror EndpointSlices created, etcd was still unreachable from control plane pods because etcd was only listening on a specific IP (from the `POD_IP` environment variable), not on all interfaces.

**Root Cause**:
The etcd StatefulSet configuration was using `POD_IP` environment variable for `ETCD_LISTEN_CLIENT_URLS`:
```
ETCD_LISTEN_CLIENT_URLS="https://$(POD_IP):2379,https://localhost:2379"
```

In Primary UDN setups:
- The pod has two IPs: default network IP (e.g., `10.128.x.x`) and UDN IP (e.g., `10.150.x.x`)
- `POD_IP` only contains one of these IPs
- etcd was only listening on that single IP
- Control plane pods on Primary UDN needed to connect via the UDN IP

**What Needed to Change**:
For KubeVirt platforms, etcd should listen on all interfaces (`10.128.x.x` AND `10.150.x.x`, or just `0.0.0.0`), so it can accept connections from both the default network and Primary UDN.

**Code Changed**:

**File**: `control-plane-operator/controllers/hostedcontrolplane/v2/etcd/statefulset.go`

```go
// Detect KubeVirt platform
isKubeVirt := hcp.Spec.Platform.Type == hyperv1.KubevirtPlatform

if !ipv4 {
    // IPv6 logic...
} else if isKubeVirt {
    // IPv4 KubeVirt: listen on all interfaces
    // Note: 0.0.0.0 includes localhost (127.0.0.1), so no need to specify it separately
    util.UpsertEnvVar(c, corev1.EnvVar{
        Name:  "ETCD_LISTEN_CLIENT_URLS",
        Value: "https://0.0.0.0:2379",
    })
}
```

**Before** (non-KubeVirt):
```
ETCD_LISTEN_CLIENT_URLS="https://$(POD_IP):2379,https://localhost:2379"
```

**After** (KubeVirt):
```
ETCD_LISTEN_CLIENT_URLS="https://0.0.0.0:2379"
```

**Impact**: 
This code change makes etcd automatically listen on all interfaces for KubeVirt platforms, allowing control plane pods to reach etcd via any of the pod's IPs (default or UDN).

**Note**: 
Using `0.0.0.0` is temporary, and should be replaced by a init-container that fetches the primaryUDN IP and sets it as a env var or something like that.
---

### Change 3: Ignition Server Endpoint Selection

**What Broke**:
Worker VMs failed to boot with the error on console:
```
ignition[843]: GET https://ignition-server-clusters-test-primary-udn.apps.hypershift.qinqon.corp/ignition: 
dial tcp: lookup ignition-server-clusters-test-primary-udn.apps.hypershift.qinqon.corp on 172.30.0.10:53: 
read udp 10.150.0.120:39990->172.30.0.10:53: i/o timeout
```

The VM was trying to reach an external route hostname during early boot, but Primary UDN's network isolation prevented it from resolving external DNS names or reaching routes before kubelet established full connectivity.

**Why it broke**
[Openshift ingress router](https://github.com/openshift/router) pod does not support primary UDN yet.

**What Needed to Change**:
The ignition endpoint URL needed to be set to the internal ClusterIP service DNS name (`ignition-server.{namespace}.svc.cluster.local`) instead of the external route hostname for Primary UDN namespaces.

**Code Added**:

**File**: `hypershift-operator/controllers/hostedcluster/hostedcluster_controller.go`

```go
// Detect Primary UDN by checking namespace label
isPrimaryUDN := false
if hcluster.Spec.Platform.Type == hyperv1.KubevirtPlatform {
    namespace := &corev1.Namespace{}
    if err := r.Client.Get(ctx, client.ObjectKey{Name: hcluster.Namespace}, namespace); err == nil {
        if label, exists := namespace.Labels["k8s.ovn.org/primary-user-defined-network"]; exists && label != "" {
            isPrimaryUDN = true
        }
    }
}

// Set ignition endpoint based on UDN type
if isPrimaryUDN {
    hcluster.Status.IgnitionEndpoint = fmt.Sprintf("ignition-server.%s.svc.cluster.local", hcluster.Namespace)
} else {
    hcluster.Status.IgnitionEndpoint = servicePublishingStrategyMapping.Route.Hostname
}
```

---

### Change 4: Service Network Access Policy

**What Broke**:
Worker VMs failed to reach the internal ignition service with DNS timeout errors:
```
dial tcp: lookup ignition-server.clusters-test-primary-udn.svc.cluster.local on 172.30.0.10:53: 
read udp 10.150.0.120:39990->172.30.0.10:53: i/o timeout
```

The NetworkPolicy for `virt-launcher` pods was blocking ALL egress traffic to the service CIDR (`172.30.0.0/16`), which includes:
- DNS service at `172.30.0.10:53`
- All ClusterIP services including `ignition-server`

**What Needed to Change**:
The `reconcileVirtLauncherNetworkPolicy()` function was adding the service CIDR to the blocked networks list. This needed to be removed because:
- Primary UDN namespaces have an "infrastructure-locked" default network interface that needs service access
- DNS resolution is essential for reaching services by name
- Primary UDN already provides network isolation at the OVN level, so blocking service CIDR was redundant and overly restrictive

**Code Changed**:

**File**: `hypershift-operator/controllers/hostedcluster/network_policies.go`

```go
// BEFORE (wrong - blocked service network):
for _, network := range managementClusterNetwork.Spec.ServiceNetwork {
    blockedIPv4Networks, blockedIPv6Networks = addToBlockedNetworks(network, blockedIPv4Networks, blockedIPv6Networks)
}

// AFTER (correct - allow service network):
// Removed the loop entirely - service network is no longer added to blocked list
```

---

### Change 5: Ignition CA Certificate Reference

**What Broke**:
Worker VMs failed to fetch ignition with TLS certificate verification error:
```
ignition[843]: GET error: Get "https://ignition-server.clusters-test-primary-udn.svc.cluster.local/ignition": 
tls: failed to verify certificate: x509: certificate signed by unknown authority
```

Even though the DNS resolution and network connectivity worked, the TLS handshake failed because the worker VM's ignition configuration contained the wrong CA certificate to verify the server.

**What Needed to Change**:
The `getIgnitionCACert()` function was injecting the `ignition-server-ca-cert` secret into the worker's userdata, but the actual `ignition-server` TLS certificate is signed by the `root-ca`, not by `ignition-server-ca-cert`. The function needed to reference the correct CA secret.

**Code Changed**:

**File**: `hypershift-operator/controllers/nodepool/token.go`

```go
// BEFORE (wrong):
caSecret := ignitionserver.IgnitionCACertSecret(t.controlplaneNamespace)

// AFTER (correct):
caSecret := &corev1.Secret{
    ObjectMeta: metav1.ObjectMeta{
        Namespace: t.controlplaneNamespace,
        Name:      "root-ca",
    },
}
```

---

### Change 6: Ignition Server TLS Certificate

**What Broke**:
After fixing the CA certificate issue, worker VMs still failed TLS verification with a different error - the server's certificate didn't match the DNS name being requested. The `ignition-server-proxy` was using a certificate that only contained the external route hostname in its SANs, not the internal service DNS name.

**What Needed to Change**:
The `ignition-server-proxy` HAProxy deployment needed to mount a different certificate secret - one that includes the internal service DNS name (`ignition-server.{namespace}.svc.cluster.local`) in its Subject Alternative Names. The correct secret is `ignition-server`, which contains both external and internal DNS names.

**Code Changed**:

**File**: `control-plane-operator/controllers/hostedcontrolplane/v2/assets/ignition-server-proxy/deployment.yaml`

```yaml
volumes:
- name: serving-cert
  secret:
    defaultMode: 416
    secretName: ignition-server  # Changed from: ignition-server-serving-cert
```

---

### Change 7: Internal DNS for Apps Domain (Ingress Fix)

**What Broke**:

The `ingress` and `console` cluster operators are degraded due to **DNS resolving to unreachable IPs**.

**Symptoms**:
- `ingress` operator: `Degraded=True` with `CanaryChecksRepetitiveFailures`
- `console` operator: `Degraded=True` with OAuth authentication failures

**Error Messages**:
```bash
# Get ingress operator status
oc --kubeconfig ${CLUSTER_NAME}-kubeconfig get co ingress -o jsonpath='{.status.conditions[?(@.type=="Degraded")].message}'
# Output:
# Canary route checks for the default ingress controller are failing.
# error sending canary HTTP Request: Timeout: Get "https://canary-openshift-ingress-canary.apps.test-primary-udn.apps.hypershift.qinqon.corp": 
# context deadline exceeded (Client.Timeout exceeded while awaiting headers)

# Get console operator status
oc --kubeconfig ${CLUSTER_NAME}-kubeconfig get co console -o jsonpath='{.status.conditions[?(@.type=="Degraded")].message}'
# Output:
# Error initializing authenticator: failed to construct OAuth endpoint cache: 
# failed to setup an async cache - caching func returned error: context deadline exceeded
```

**Root Cause - DNS Resolution Points to Wrong Network**:

The core issue is that **DNS queries return IPs on the wrong network**:

| What Resolves | Returns | Actually Needed |
|---------------|---------|-----------------|
| `*.apps.test-primary-udn.apps...` | `192.168.122.253` (external VIP) | `172.31.x.x` (guest ClusterIP) |
| `oauth-clusters-test-primary-udn.apps...` | `192.168.122.253` (external VIP) | Internal service IP |

**Why External VIP Doesn't Work**:

When DNS returns the external IP (`192.168.122.253`), the traffic flow becomes:

1. Control plane pod resolves hostname → gets `192.168.122.253` (external VIP)
2. Request goes to management cluster router (passthrough route)
3. Router forwards to EndpointSlice target: `10.150.0.111` (worker VM's **Primary UDN IP**)
4. **💥 FAILS**: Management cluster cannot route to Primary UDN (`10.150.0.0/16`) - network is isolated

**Network Flow Diagram (Before Fix)**:
```
ingress-operator pod
    |
    | DNS: "canary-openshift-ingress-canary.apps.test-primary-udn.apps..."
    |
    v
❌ Management cluster DNS → Returns: 192.168.122.253 (external VIP)
    |
    v
Management cluster router → Passthrough route → NodePort service
    |
    v
EndpointSlice target: 10.150.0.111 (Primary UDN IP)
    |
    X UNREACHABLE - Primary UDN is isolated from management cluster network
```

**Verification Commands**:

```bash
# Check what DNS returns from inside a UDN pod (what konnectivity-https-proxy sees)
oc -n clusters-${CLUSTER_NAME} exec deploy/konnectivity-agent -- \
  nslookup canary-openshift-ingress-canary.apps.test-primary-udn.apps.hypershift.qinqon.corp

# Returns: 192.168.122.253 (external VIP - WRONG!)

# Check what the passthrough service points to
oc get endpointslice -n clusters-${CLUSTER_NAME} -l kubernetes.io/service-name=router-default -o yaml

# Shows: 10.150.0.111 (Primary UDN IP - UNREACHABLE from management cluster)

# Get the internal ClusterIP that WOULD work
oc --kubeconfig ${CLUSTER_NAME}-kubeconfig get svc -n openshift-ingress router-internal-default \
  -o jsonpath='{.spec.clusterIP}'

# Returns: 172.31.88.98 (ClusterIP - REACHABLE via konnectivity tunnel)

# Test from UDN pod - external VIP FAILS
oc -n clusters-${CLUSTER_NAME} exec deploy/konnectivity-agent -- \
  curl -k --connect-timeout 5 https://192.168.122.253/healthz
# Result: timeout

# Test from UDN pod - internal ClusterIP WORKS  
oc -n clusters-${CLUSTER_NAME} exec deploy/konnectivity-agent -- \
  curl -k --connect-timeout 5 https://172.31.88.98/healthz
# Result: success
```

**The Fix - Override DNS Resolution**:

The problem is DNS returning the wrong IP. The solution is to **make DNS return the internal ClusterIP instead**.

**Solution Implemented** (for Ingress Operator):

The `konnectivity-https-proxy` already has `ResolveFromGuestClusterDNS=true` [enabled](https://github.com/openshift/hypershift/blob/9ee4ac7c2d9303e92e4b282262142df28efc2ff7/konnectivity-https-proxy/cmd.go#L47), meaning it queries the guest cluster's CoreDNS for hostname resolution. The fix leverages this by configuring the guest cluster's DNS to return the **internal router ClusterIP** instead of the external VIP.

**How It Works**:
1. Create a custom CoreDNS deployment inside the guest cluster (`internal-apps-dns` namespace)
2. Configure it with `hosts` entries that map apps domain hostnames to the internal router ClusterIP
3. Patch the DNS operator to forward apps domain queries to this internal DNS server
4. Konnectivity proxy resolves apps hostnames → gets internal router IP → traffic stays internal

**Implementation** (added to `cluster-sync.sh`):

```bash
# 1. Get the internal router ClusterIP from guest cluster
ROUTER_IP=$(oc --kubeconfig ${GUEST_KUBECONFIG} get svc router-internal-default \
  -n openshift-ingress -o jsonpath='{.spec.clusterIP}')

# 2. Create internal DNS namespace
oc --kubeconfig ${GUEST_KUBECONFIG} create ns internal-apps-dns

# 3. Create CoreDNS ConfigMap with hosts entries
cat <<EOF | oc --kubeconfig ${GUEST_KUBECONFIG} apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: coredns-config
  namespace: internal-apps-dns
data:
  Corefile: |
    .:5353 {
        log
        errors
        hosts {
            ${ROUTER_IP} console-openshift-console.apps.${CLUSTER_NAME}.apps.hypershift.qinqon.corp
            ${ROUTER_IP} canary-openshift-ingress-canary.apps.${CLUSTER_NAME}.apps.hypershift.qinqon.corp
            ${ROUTER_IP} downloads-openshift-console.apps.${CLUSTER_NAME}.apps.hypershift.qinqon.corp
            fallthrough
        }
        forward . 172.31.0.10
    }
EOF

# 4. Deploy CoreDNS pod
cat <<EOF | oc --kubeconfig ${GUEST_KUBECONFIG} apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: internal-apps-dns
  namespace: internal-apps-dns
spec:
  replicas: 1
  selector:
    matchLabels:
      app: internal-apps-dns
  template:
    metadata:
      labels:
        app: internal-apps-dns
    spec:
      containers:
      - name: coredns
        image: registry.redhat.io/openshift4/ose-coredns-rhel9:v4.17
        args: ["-conf", "/etc/coredns/Corefile"]
        ports:
        - containerPort: 5353
          protocol: UDP
        - containerPort: 5353
          protocol: TCP
        volumeMounts:
        - name: config
          mountPath: /etc/coredns
      volumes:
      - name: config
        configMap:
          name: coredns-config
EOF

# 5. Create DNS service
cat <<EOF | oc --kubeconfig ${GUEST_KUBECONFIG} apply -f -
apiVersion: v1
kind: Service
metadata:
  name: internal-apps-dns
  namespace: internal-apps-dns
spec:
  selector:
    app: internal-apps-dns
  ports:
  - name: dns-udp
    port: 5353
    targetPort: 5353
    protocol: UDP
  - name: dns-tcp
    port: 5353
    targetPort: 5353
    protocol: TCP
EOF

# 6. Patch DNS operator to forward apps domain to internal DNS
DNS_SVC_IP=$(oc --kubeconfig ${GUEST_KUBECONFIG} get svc internal-apps-dns \
  -n internal-apps-dns -o jsonpath='{.spec.clusterIP}')
oc --kubeconfig ${GUEST_KUBECONFIG} patch dns.operator.openshift.io default --type=merge \
  -p "{\"spec\":{\"servers\":[{\"name\":\"internal-apps\",\"zones\":[\"apps.${CLUSTER_NAME}.apps.hypershift.qinqon.corp\"],\"forwardPlugin\":{\"upstreams\":[\"${DNS_SVC_IP}:5353\"]}}]}}"
```

**Before Fix** (broken):
```
ingress-operator → konnectivity-proxy → guest DNS → 192.168.122.253 (external VIP)
                                                          ↓
                                              passthrough route → NodePort
                                                          ↓
                                              EndpointSlice: 10.150.0.138 (Primary UDN IP)
                                                          ↓
                                                    ❌ UNREACHABLE
```

**After Fix** (working):
```
ingress-operator → konnectivity-proxy → guest DNS (internal) → 172.31.88.98 (internal router ClusterIP)
                                                                      ↓
                                                              ✅ Traffic stays internal to guest cluster
```

**Proof from Konnectivity Logs**:
```bash
# Check konnectivity-https-proxy logs for DNS resolution
oc -n clusters-${CLUSTER_NAME} logs deploy/ingress-operator -c konnectivity-https-proxy | grep -i resolv
# Output:
# {"msg":"Successfully looked up address from guest cluster","address":"172.31.88.98"}
# {"msg":"Host name resolved","ip":"172.31.88.98"}
```

**Verify the Fix**:
```bash
# Check ingress operator status after fix
oc --kubeconfig ${CLUSTER_NAME}-kubeconfig get co ingress
# Output: Available=True, Degraded=False
```

**Result**:
- ✅ `ingress` ClusterOperator: `Available=True`, `Degraded=False`
- ✅ Canary route checks now pass

**Note**: `console` ClusterOperator OAuth proxy fix is now implemented (see Change 8)

---

### Change 8: Console OAuth Authentication (OAuth Bridge)

**What Broke**:

The `console` ClusterOperator is degraded with OAuth authentication errors.

**Error Message**:
```bash
oc --kubeconfig ${CLUSTER_NAME}-kubeconfig get co console -o jsonpath='{.status.conditions[?(@.type=="Degraded")].message}'
# Output: Error initializing authenticator: context deadline exceeded
```

**Why the Ingress DNS Fix Doesn't Help Here**:

The guest VM only has Primary UDN network - it can't reach the management cluster network where OAuth runs:

```
Console pod (guest cluster VM)     OAuth pod (HCP namespace, management cluster)
      |                                           |
      | Primary UDN only (10.150.0.0/16)         | Management network (172.30.x.x)
      |                                           |
      X-------- No route between networks --------X
```

| Issue | Ingress (Change 7) | OAuth (Change 8) |
|-------|-------------------|-----------------|
| Service location | Guest cluster router | Management cluster |
| DNS fix works? | ✅ Yes - router is reachable via ClusterIP | ❌ No - OAuth server not in guest cluster |
| Problem type | DNS only | DNS + Network isolation |

The ingress fix worked because we could redirect DNS to the guest cluster's internal router ClusterIP. But OAuth is served by the **management cluster** - there's no OAuth service inside the guest cluster to redirect to.

**The Fix**:

All HCP namespace pods (including OAuth) get a Primary UDN IP via the `ovn-udn1` interface. Create a Service in the guest cluster that points to OAuth pod's Primary UDN IP.

**Solution**:
1. Get OAuth pod's Primary UDN IP from `k8s.ovn.org/pod-networks` annotation
2. Create Service + Endpoints in guest cluster pointing to OAuth's Primary UDN IP
3. Update guest cluster DNS to resolve OAuth hostname to the service ClusterIP

**Implementation Steps**:

**Step 1: Get OAuth pod's Primary UDN IP**
```bash
# OAuth pod has both network interfaces - get its Primary UDN IP from the pod-networks annotation
OAUTH_UDN_IP=$(oc get pod -n clusters-${CLUSTER_NAME} -l app=oauth-openshift \
  -o jsonpath='{.items[0].metadata.annotations.k8s\.ovn\.org/pod-networks}' | \
  jq -r ".\"clusters-${CLUSTER_NAME}/hcp-${CLUSTER_NAME}\".ip_address" | cut -d/ -f1)
echo "OAuth Primary UDN IP: ${OAUTH_UDN_IP}"
# Example output: 10.150.0.128
```

**Step 2: Create Service + Endpoints in guest cluster**

This creates a ClusterIP service in the guest cluster that routes directly to OAuth's Primary UDN IP:
```bash
cat <<EOF | oc --kubeconfig ${CLUSTER_NAME}-kubeconfig apply -f -
apiVersion: v1
kind: Service
metadata:
  name: oauth-bridge
  namespace: default
spec:
  ports:
  - port: 443
    targetPort: 6443  # OAuth listens on 6443
---
apiVersion: v1
kind: Endpoints
metadata:
  name: oauth-bridge
  namespace: default
subsets:
- addresses:
  - ip: ${OAUTH_UDN_IP}  # OAuth pod's Primary UDN IP (e.g., 10.150.0.128)
  ports:
  - port: 6443
EOF

# Get the new service's ClusterIP
OAUTH_SVC_IP=$(oc --kubeconfig ${CLUSTER_NAME}-kubeconfig get svc oauth-bridge -n default \
  -o jsonpath='{.spec.clusterIP}')
echo "OAuth service ClusterIP: ${OAUTH_SVC_IP}"
# Example output: 172.31.242.61
```

**Step 3: Update guest cluster DNS**

Add the OAuth hostname to the internal DNS ConfigMap:
```bash
# Update the internal DNS ConfigMap to include OAuth hostname
oc --kubeconfig ${CLUSTER_NAME}-kubeconfig patch cm coredns-config -n internal-apps-dns --type=json -p="[
  {\"op\": \"replace\", \"path\": \"/data/Corefile\", \"value\": \".:5353 {
    log
    errors
    hosts {
        ${ROUTER_IP} console-openshift-console.apps.${CLUSTER_NAME}.apps.hypershift.qinqon.corp
        ${ROUTER_IP} canary-openshift-ingress-canary.apps.${CLUSTER_NAME}.apps.hypershift.qinqon.corp
        ${ROUTER_IP} downloads-openshift-console.apps.${CLUSTER_NAME}.apps.hypershift.qinqon.corp
        ${OAUTH_SVC_IP} oauth-clusters-${CLUSTER_NAME}.apps.hypershift.qinqon.corp
        fallthrough
    }
    forward . 172.31.0.10
}\"}
]"

# Restart internal DNS to pick up changes
oc --kubeconfig ${CLUSTER_NAME}-kubeconfig rollout restart deploy/internal-apps-dns -n internal-apps-dns

# Also restart main CoreDNS to clear cache
oc --kubeconfig ${CLUSTER_NAME}-kubeconfig delete pod -n openshift-dns -l dns.operator.openshift.io/daemonset-dns=default
```

**Network Flow After Fix**:
```
Console pod (guest VM)
    |
    | DNS: "oauth-clusters-test-primary-udn.apps..."
    |
    v
✅ Guest cluster DNS → Returns: 172.31.242.61 (oauth-bridge service ClusterIP)
    |
    v
oauth-bridge Service (guest cluster, default namespace)
    |
    | port: 443 → targetPort: 6443
    | Endpoints: 10.150.0.128:6443 (OAuth pod's Primary UDN IP)
    |
    v
✅ OAuth pod (HCP namespace) - responds directly on Primary UDN interface
```

**Verification Commands**:
```bash
# Verify OAuth pod has Primary UDN IP
oc get pod -n clusters-${CLUSTER_NAME} -l app=oauth-openshift \
  -o jsonpath='{.items[0].metadata.annotations.k8s\.ovn\.org/pod-networks}' | jq .
# Should show both "default" (infrastructure-locked) and Primary UDN network

# Verify DNS returns internal IP from inside guest cluster
oc --kubeconfig ${CLUSTER_NAME}-kubeconfig debug node/$(oc --kubeconfig ${CLUSTER_NAME}-kubeconfig get nodes -o jsonpath='{.items[0].metadata.name}') -- \
  nslookup oauth-clusters-${CLUSTER_NAME}.apps.hypershift.qinqon.corp
# Should return: 172.31.242.61 (internal ClusterIP, not external VIP)

# Test OAuth connectivity from inside guest cluster
oc --kubeconfig ${CLUSTER_NAME}-kubeconfig debug node/$(oc --kubeconfig ${CLUSTER_NAME}-kubeconfig get nodes -o jsonpath='{.items[0].metadata.name}') -- \
  curl -k --connect-timeout 5 https://oauth-clusters-${CLUSTER_NAME}.apps.hypershift.qinqon.corp/healthz
# Should return: ok

# Check console operator status
oc --kubeconfig ${CLUSTER_NAME}-kubeconfig get co console
# Should show: Available=True, Degraded=False
```

**IP Stability Note**:
If the OAuth pod restarts, its Primary UDN IP may change. The Endpoints resource would need to be updated. For production, consider:
- A controller that watches OAuth pod and updates Endpoints
- Using a sidecar or init container to keep IPs in sync

---

### Change 9: HyperShift HCCO UDN-aware passthrough EndpointSlices for selectorless Services

**What Broke**:

The `[sig-kubevirt]` infra-to-guest **LoadBalancer** connectivity checks fail when the **HCP namespace** (`clusters-<cluster-name>`) is a **Primary UDN** namespace and the mirrored management Service is intentionally **selectorless**.

**Symptoms**:

- LoadBalancer VIP is assigned (e.g., `192.168.122.x`), but infra-client pod traffic to the VIP fails (commonly `Connection refused`).
- OVN-Kubernetes programs the service load balancer with `reject=true` because it sees **no usable UDN endpoints** for the Service.

**Root Cause - EndpointSlice label/annotation mismatch for Primary UDN**:

In Primary UDN namespaces, OVN-Kubernetes' UDN service controller **does not consume** the “default” EndpointSlices labeled `kubernetes.io/service-name`. Instead, it expects **UDN EndpointSlices** labeled and annotated as described in the OVN-K docs:

- label: `k8s.ovn.org/service-name: <service-name>`
- annotation: `k8s.ovn.org/endpointslice-network: <udn-network-name>`

**Note**: The OVN-K EndpointSlice mirror controller is behaving as designed here. It only mirrors EndpointSlices created by the Kubernetes default controller (`endpointslice-controller.k8s.io`), and it does **not** process custom/out-of-band EndpointSlices. Since our passthrough Service is selectorless by design, there is nothing for the mirror controller to mirror.

The actual problem is that HyperShift's hostedclusterconfigoperator creates these passthrough EndpointSlices only in the default form (label `kubernetes.io/service-name`) and **does not add** the UDN label/annotation (`k8s.ovn.org/service-name` + `k8s.ovn.org/endpointslice-network`). As a result, OVN-K's UDN service controller ignores them and programs the LoadBalancer VIP with no usable UDN backends.

**Manual workaround (deploy UDN-style EndpointSlices)**:

Create **one EndpointSlice per VM** (mirroring the existing per-VM EndpointSlices) but add the minimal OVN-K metadata so the UDN service controller consumes it:

- label: `k8s.ovn.org/service-name: <mirrored-mgmt-service-name>`
- annotation: `k8s.ovn.org/endpointslice-network: <primary-udn-network-name>` (for HyperShift/KubeVirt this is typically `clusters-<cluster-name>_hcp-<cluster-name>`)

**Manifest used** (apply once per VM / `virt-launcher`):

```yaml
apiVersion: discovery.k8s.io/v1
kind: EndpointSlice
metadata:
  # One per VM. Pick a stable name (example shown).
  name: udn-<mirrored-mgmt-service-name>-<vm-name>-ipv4
  namespace: clusters-<cluster-name>
  labels:
    endpointslice.kubernetes.io/managed-by: manual-workaround
    k8s.ovn.org/service-name: <mirrored-mgmt-service-name>
  annotations:
    k8s.ovn.org/endpointslice-network: clusters-<cluster-name>_hcp-<cluster-name>
addressType: IPv4
ports:
- name: http
  protocol: TCP
  # Use the mirrored management Service's .spec.ports[?port==8080].targetPort
  port: <targetPort>
endpoints:
- addresses:
  # Use the VM / virt-launcher **Primary UDN** IP (role:"primary" from k8s.ovn.org/pod-networks)
  - <primary-udn-ip>
  conditions:
    ready: true
    serving: true
    terminating: false
  # Optional (recommended; matches OVN mirror output):
  nodeName: <infra-node-name>
  targetRef:
    kind: Pod
    namespace: clusters-<cluster-name>
    name: <virt-launcher-pod-name>
    uid: <virt-launcher-pod-uid>
```

**Note (proper fix)**:

This manual workaround should be replaced by a real fix in HyperShift HCCO, where these selectorless per-VM EndpointSlices are created today:

- `control-plane-operator/hostedclusterconfigoperator/controllers/machine/machine.go`

Key code change (add UDN label/annotation so OVN-K consumes these EndpointSlices on Primary UDN):

```go
endpointSlice.Labels["k8s.ovn.org/service-name"] = cpService.Name
endpointSlice.Annotations["k8s.ovn.org/endpointslice-network"] = "<clusters-<name>_hcp-<name>>"
delete(endpointSlice.Labels, discoveryv1.LabelServiceName) // remove kubernetes.io/service-name
```

---

## Origin conformance: KubeVirt services (Primary UDN)

This section is a **validation checklist** for the Origin `[sig-kubevirt] services` conformance tests when the HyperShift **hosted control plane namespace** (`clusters-<cluster-name>`) is configured as a **Primary UDN namespace**.

**Validation notes**:

- Tests were run **serially, one-by-one** (to eliminate cross-talk / parallel interference between tests).
- Tests were run with **minimal monitors** by setting `MONITORS="event-collector"` (i.e., disabling the default broader monitor set). With the broader/default monitors enabled, additional monitor failures can occur and may not reflect the actual connectivity assertions of the test itself.

| Test (full string) | Current status on Primary UDN | Why it fails (when it fails) | Change needed to pass |
|---|---:|---|---|
| `[sig-kubevirt] services when running openshift cluster on KubeVirt virtual machines should allow connections to pods from guest cluster PodNetwork pod via LoadBalancer service across different guest nodes [Suite:openshift/conformance/parallel]` | ✅ passed | - | - |
| `[sig-kubevirt] services when running openshift cluster on KubeVirt virtual machines should allow connections to pods from guest hostNetwork pod via NodePort across different guest nodes [Suite:openshift/conformance/parallel]` | ✅ passed | - | - |
| `[sig-kubevirt] services when running openshift cluster on KubeVirt virtual machines should allow connections to pods from guest podNetwork pod via NodePort across different guest nodes [Suite:openshift/conformance/parallel]` | ✅ passed | - | - |
| `[sig-kubevirt] services when running openshift cluster on KubeVirt virtual machines should allow direct connections to pods from guest cluster pod in pod network across different guest nodes [Suite:openshift/conformance/parallel]` | ✅ passed | - | - |
| `[sig-kubevirt] services when running openshift cluster on KubeVirt virtual machines should allow direct connections to pods from guest cluster pod in host network across different guest nodes [Suite:openshift/conformance/parallel]` | ✅ passed | - | - |
| `[sig-kubevirt] services when running openshift cluster on KubeVirt virtual machines should allow connections to pods from infra cluster pod via LoadBalancer service across different guest nodes [Suite:openshift/conformance/parallel]` | ✅ passed | - | -|
| `[sig-kubevirt] services when running openshift cluster on KubeVirt virtual machines should allow connections to pods from infra cluster pod via NodePort across different infra nodes [Suite:openshift/conformance/parallel]` | ⚠️ passed with test alteration | The test uses `serverVMPod.Status.PodIP` as the “guest node IP”, which in Primary UDN environments is **not** the Primary UDN IP. | **Origin test change**: when Primary UDN is configured, use the `virt-launcher` **Primary UDN** IP (from `k8s.ovn.org/pod-networks`, `role:"primary"`) instead of `pod.status.podIP`.<br/><br/>**Repro**: `repro-kubevirt-infra-nodeport.sh` |

---

## Summary

**Total Changes**: 5 HyperShift code files modified + 2 workarounds in cluster-sync.sh

### Completed Changes:
1. **ETCD EndpointSlice mirroring** (manual workaround) - Create manual mirror EndpointSlices pointing to etcd's UDN IP so control plane can reach etcd
2. **ETCD listen address** - Listen on all interfaces for KubeVirt platforms
3. **Ignition endpoint** - Use internal DNS for Primary UDN
4. **Network policy** - Allow service CIDR access for DNS and ClusterIP services
5. **CA certificate** - Use correct CA (`root-ca`) in worker userdata
6. **Server certificate** - Use cert with internal DNS name in SANs
7. **Internal DNS for apps domain** (cluster-sync.sh) - Create internal CoreDNS in guest cluster that returns internal router ClusterIP for apps routes, fixing ingress operator canary checks
8. **Console OAuth bridge** (cluster-sync.sh) - Create Service/Endpoints in guest cluster pointing to OAuth pod's Primary UDN IP, with DNS override

**Current Result**: 
- ✅ Control plane can start and connect to etcd
- ✅ Worker VMs can boot, fetch ignition, and join the hosted cluster
- ✅ `ingress` operator healthy (`Available=True`, `Degraded=False`)
- ✅ `console` operator healthy (`Available=True`, `Degraded=False`) - OAuth bridge to pod's Primary UDN IP

---
## Architecture Notes

### Primary UDN Network Model

**Control Plane Pods**:
- Primary UDN IP on `ovn-udn1` interface (default route)
- Infrastructure-locked IP on `eth0` interface (restricted)

**Worker VMs**:
- Primary UDN IP (`10.150.0.0/16` range)
- Direct L2 connectivity via `l2bridge` binding
- Routes to service CIDR (`172.30.0.0/16`) for ClusterIP access

**Key Constraint**: Primary UDN provides network isolation at OVN level. Pods/VMs on Primary UDN cannot reach management cluster default network without explicit routes.

### Required Configuration

For Primary UDN hosted clusters:
1. **Namespace** must have label: `k8s.ovn.org/primary-user-defined-network=<network-name>`
2. **UserDefinedNetwork** resource with `role: Primary` must exist
3. **VMs** must use `l2bridge` network binding (not `bridge` or `masquerade`)
4. **Routes** must be configured to service CIDR and host network if needed

