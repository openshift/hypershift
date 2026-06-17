# Test Cases for RFE-8049: Enable HTTP Route Access for HCP Clusters with baseDomainPassthrough

## Overview

RFE-8049 requests that plain HTTP (insecure) routes created in HCP (KubeVirt) guest clusters
are accessible when `baseDomainPassthrough: true` is set on the KubeVirt platform spec.

Previously, only HTTPS traffic was forwarded via the TLS passthrough route on the management
cluster. HTTP traffic had no forwarding path and returned "application not found".

---

## Unit Tests (added to `reconcile_test.go`)

### Test: `TestReconcileDefaultIngressPassthroughService`

#### When NodePort service has both HTTP and HTTPS ports, it should configure both ports in the passthrough service

**Description:**  
Verifies that when the guest cluster's `router-nodeport-default` Service exposes both port 80
(HTTP NodePort) and port 443 (HTTPS NodePort), the passthrough ClusterIP Service created on
the infra cluster includes both `http-80` and `https-443` port entries. Endpoint slices for
both ports are then populated by the machine controller.

**Inputs:**
- `defaultNodePort` Service: ports `{Port: 80, NodePort: 32080}` and `{Port: 443, NodePort: 32443}`
- HCP with `InfraID: "test-infra-id"`

**Expected output:**
- Passthrough service has exactly 2 ports: `http-80` (Port 80 → NodePort 32080) and `https-443` (Port 443 → NodePort 32443)
- `service.Spec.Selector` is empty (endpoints managed externally)
- `service.Spec.Type` is `ClusterIP`
- Label `hypershift.openshift.io/infra-id: test-infra-id` is set

---

#### When NodePort service is missing the HTTPS port, it should return an error

**Description:**  
Verifies that if the guest router NodePort service only exposes port 80 (no HTTPS NodePort),
the reconciler returns a descriptive error instead of creating a misconfigured service.

**Inputs:**
- `defaultNodePort` Service: ports `{Port: 80, NodePort: 32080}` only

**Expected output:**
- Error: `"unable to detect default ingress NodePort https port"`

---

#### When NodePort service is missing the HTTP port, it should return an error

**Description:**  
Verifies that if the guest router NodePort service only exposes port 443 (no HTTP NodePort),
the reconciler returns a descriptive error.

**Inputs:**
- `defaultNodePort` Service: ports `{Port: 443, NodePort: 32443}` only

**Expected output:**
- Error: `"unable to detect default ingress NodePort http port"`

---

#### When NodePort service has no ports, it should return an error for the missing HTTPS port

**Description:**  
Verifies that an empty NodePort service causes an immediate error for the HTTPS port.

**Inputs:**
- `defaultNodePort` Service: no ports

**Expected output:**
- Error: `"unable to detect default ingress NodePort https port"`

---

### Test: `TestReconcileDefaultIngressPassthroughHTTPRoute`

#### When HCP has baseDomainPassthrough enabled, it should create an HTTP wildcard route for insecure guest routes

**Description:**  
Verifies that the HTTP passthrough route is created with the correct wildcard host, no TLS
configuration, and the correct target service port. The wildcard host `apps.{hcpName}.{baseDomain}`
(with `WildcardPolicySubdomain`) matches `*.apps.{hcpName}.{baseDomain}`, which is the domain
used by insecure routes in the guest cluster.

**Inputs:**
- HCP: `name=guest`, `spec.dns.baseDomain=apps.mgmt.example.com`, `spec.infraID=test-infra-id`
- cpService: `name=default-ingress-passthrough-service-abc123`

**Expected output:**
- `route.Spec.WildcardPolicy = Subdomain`
- `route.Spec.Host = "apps.guest.apps.mgmt.example.com"` (wildcard matches `*.apps.guest.apps.mgmt.example.com`)
- `route.Spec.TLS = nil` (plain HTTP, no TLS)
- `route.Spec.Port.TargetPort = "http-80"` (targets the HTTP port on the passthrough service)
- `route.Spec.To.Name = "default-ingress-passthrough-service-abc123"`
- Label `hypershift.openshift.io/infra-id: test-infra-id` is set

---

#### When HCP has a different name and baseDomain, it should set the correct HTTP route host

**Description:**  
Verifies that the HTTP route host is correctly computed from the HCP name and baseDomain for any cluster.

**Inputs:**
- HCP: `name=mycluster`, `spec.dns.baseDomain=apps.production.example.com`, `spec.infraID=my-infra-id`

**Expected output:**
- `route.Spec.Host = "apps.mycluster.apps.production.example.com"`
- `route.Spec.WildcardPolicy = Subdomain`
- `route.Spec.TLS = nil`
- Label `hypershift.openshift.io/infra-id: my-infra-id` is set

---

## End-to-End Test Description

### Pre-conditions

1. A management OCP cluster with wildcard routes allowed:
   ```shell
   oc patch ingresscontroller -n openshift-ingress-operator default --type=json \
     -p '[{ "op": "add", "path": "/spec/routeAdmission", "value": {wildcardPolicy: "WildcardsAllowed"}}]'
   ```

2. A KubeVirt HCP guest cluster created **without** specifying `--base-domain` (so `baseDomainPassthrough: true` is auto-set):
   ```shell
   hcp create cluster kubevirt \
     --name guest \
     --node-pool-replicas 2 \
     --pull-secret /path/to/pull-secret \
     --memory 6Gi \
     --cores 2
   ```

---

### Test Case: Verify HTTPS route (edge termination) works

**Steps:**
1. Connect to the guest cluster.
2. Deploy a sample app (e.g., `httpd`) in the `default` namespace.
3. Create an **edge route** (HTTPS with redirect):
   ```shell
   oc create route edge myapp --service=myapp-svc --port=8080
   ```
4. From outside the cluster, access `https://myapp-default.apps.guest.<baseDomain>`.

**Expected result:**  
- HTTP 200 response served via HTTPS; the guest router terminates TLS at port 443.
- This worked before the fix and should continue to work after.

---

### Test Case: Verify HTTP route (insecure) now works with the fix

**Steps:**
1. Connect to the guest cluster.
2. Deploy a sample app (e.g., `httpd`) in the `default` namespace.
3. Create an **insecure route** (plain HTTP):
   ```shell
   oc create route http myapp-http --service=myapp-svc --port=8080
   ```
4. From outside the cluster, access `http://myapp-http-default.apps.guest.<baseDomain>`.

**Expected result (before fix):**  
- "Application Not Found" returned by the management cluster's HAProxy.

**Expected result (after fix):**  
- HTTP 200 response from the application, routed through the management cluster's HTTP
  passthrough wildcard route → guest router HTTP NodePort.

---

### Test Case: Verify HTTP passthrough resources are created on the infra cluster

**Steps:**
1. After cluster creation, inspect the infra namespace on the management cluster.

**Expected resources:**
- Service `default-ingress-passthrough-service-{generateID}` now has **two** ports:
  - `https-443` → guest router HTTPS NodePort
  - `http-80` → guest router HTTP NodePort
- Route `default-ingress-passthrough-route-{generateID}` (HTTPS/TLS passthrough, unchanged)
- **New** Route `default-ingress-passthrough-http-route-{generateID}`:
  - `spec.wildcardPolicy: Subdomain`
  - `spec.host: apps.{hcpName}.{baseDomain}`
  - No TLS config
  - `spec.port.targetPort: http-80`

---

### Test Case: Verify cleanup on cluster deletion

**Steps:**
1. Delete the guest cluster:
   ```shell
   hcp destroy cluster kubevirt --name guest
   ```

**Expected result:**
- The HTTP route `default-ingress-passthrough-http-route-{generateID}` is deleted from the infra namespace.
- The HTTPS route `default-ingress-passthrough-route-{generateID}` is deleted.
- The service `default-ingress-passthrough-service-{generateID}` is deleted.

---

## Files Changed

| File | Change |
|------|--------|
| `control-plane-operator/hostedclusterconfigoperator/controllers/resources/ingress/reconcile.go` | Updated `ReconcileDefaultIngressPassthroughService` to add port 80; added `ReconcileDefaultIngressPassthroughHTTPRoute` |
| `control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests/ingress.go` | Added `IngressDefaultIngressPassthroughHTTPRoute` and `IngressDefaultIngressPassthroughHTTPRouteName` |
| `control-plane-operator/hostedclusterconfigoperator/controllers/resources/resources.go` | Updated `reconcileIngressController` to create HTTP route; updated cleanup to delete HTTP route |
| `control-plane-operator/hostedclusterconfigoperator/controllers/resources/ingress/reconcile_test.go` | Added unit tests for `ReconcileDefaultIngressPassthroughService` and `ReconcileDefaultIngressPassthroughHTTPRoute` |
| `docs/content/how-to/kubevirt/ingress-and-dns.md` | Updated to reflect that both HTTP and HTTPS are now supported |

---

## References

- Jira RFE: [RFE-8049](https://redhat.atlassian.net/browse/RFE-8049)
- Related component: `control-plane-operator/hostedclusterconfigoperator`
- Platform: KubeVirt with `baseDomainPassthrough: true`
