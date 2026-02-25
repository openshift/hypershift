## Router Sharding for BareMetal HostedClusters at Scale

When hosting many HostedClusters on a bare metal management cluster using the `Route` service publishing strategy, all control plane routes (Konnectivity, OAuth, Ignition, OIDC) are served by the management cluster's default OpenShift router. At scale, this single router can become a bottleneck.

OpenShift supports [IngressController sharding](https://docs.openshift.com/container-platform/latest/networking/ingress-operator.html#nw-ingress-sharding_configuring-ingress), which allows you to distribute routes across multiple router deployments. This recipe shows how to use route label selectors to shard HostedCluster control plane routes across dedicated routers.

### Prerequisites

- A bare metal management cluster with MetalLB configured (see [Expose Data Plane Ingress via MetalLB](exposing-dataplane-with-metallb.md))
- HostedClusters using the `Route` publishing strategy for Konnectivity, OAuth, Ignition, and OIDC
- Sufficient MetalLB IP addresses for additional router LoadBalancer services

### How It Works

The Control Plane Operator creates routes for each HostedCluster in its HCP namespace. These routes use the management cluster's `*.apps` domain and are served by the default router. The CPO's reconciliation loop only sets specific fields on the route (host, TLS, target service) and does not overwrite existing labels, so user-added labels are preserved across reconciliation cycles.

By labeling routes and creating additional `IngressController` resources with matching `routeSelector`, you can distribute the load across multiple router instances.

### Step 1: Plan Your Sharding Strategy

Decide how to group your HostedClusters. Common strategies include:

- **By group size**: Split clusters into evenly sized groups (e.g., `shard: group-a`, `shard: group-b`)
- **By tenant**: Assign a shard per tenant or team
- **By criticality**: Separate production and non-production clusters

This recipe uses a label `hypershift.openshift.io/router-shard` to assign routes to shards.

### Step 2: Create Additional IngressControllers

Create a new `IngressController` for each shard. Each one deploys its own router pods and gets its own LoadBalancer service (via MetalLB):

```yaml
apiVersion: operator.openshift.io/v1
kind: IngressController
metadata:
  name: hcp-shard-a
  namespace: openshift-ingress-operator
spec:
  replicas: 2
  domain: apps.<management-cluster-domain>
  routeSelector:
    matchLabels:
      hypershift.openshift.io/router-shard: shard-a
  endpointPublishingStrategy:
    type: LoadBalancerService
    loadBalancer:
      scope: External
```

Repeat for each shard (e.g., `hcp-shard-b`, `hcp-shard-c`).

!!! Note
    The `domain` field should match the management cluster's apps domain so that the auto-generated route hostnames resolve correctly.

### Step 3: Exclude Sharded Routes from the Default Router

Update the default `IngressController` to avoid serving routes that belong to a shard. This prevents duplicate routing:

```bash
oc patch -n openshift-ingress-operator ingresscontroller/default \
  --type=merge \
  -p '{"spec":{"routeSelector":{"matchExpressions":[{"key":"hypershift.openshift.io/router-shard","operator":"DoesNotExist"}]}}}'
```

!!! Warning
    This change affects all routes on the management cluster. Ensure that non-HCP routes (console, monitoring, etc.) do not carry the `hypershift.openshift.io/router-shard` label, or they will stop being served by the default router.

### Step 4: Label the Routes in Each HCP Namespace

For each HostedCluster assigned to a shard, label its control plane routes:

```bash
HC_NAMESPACE=clusters-my-hosted-cluster
SHARD=shard-a

for route in $(oc get routes -n "${HC_NAMESPACE}" -o name); do
  oc label -n "${HC_NAMESPACE}" "${route}" hypershift.openshift.io/router-shard="${SHARD}"
done
```

The routes typically include:

- `konnectivity-server`
- `oauth`
- `ignition-server`

!!! Note
    These labels are preserved across CPO reconciliation cycles. The CPO does not overwrite or remove user-added labels on routes.

### Step 5: Verify the Sharding

- Confirm the new router pods are running:

```bash
oc get pods -n openshift-ingress -l ingresscontroller.operator.openshift.io/deployment-ingresscontroller=hcp-shard-a
```

- Verify routes are admitted by the correct router:

```bash
oc get route -n "${HC_NAMESPACE}" -o custom-columns=NAME:.metadata.name,HOST:.spec.host,SHARD:.metadata.labels.hypershift\.openshift\.io/router-shard,ROUTER:.status.ingress[0].routerName
```

### Automating Label Assignment

For environments with many HostedClusters, consider automating the label assignment. The following script labels all routes across multiple HCP namespaces based on a mapping file:

```bash
# mapping.txt format: <hcp-namespace> <shard>
# clusters-hc-01 shard-a
# clusters-hc-02 shard-a
# clusters-hc-03 shard-b

while read -r ns shard; do
  for route in $(oc get routes -n "${ns}" -o name 2>/dev/null); do
    oc label -n "${ns}" "${route}" hypershift.openshift.io/router-shard="${shard}" --overwrite
  done
done < mapping.txt
```

!!! Note
    New HostedClusters will have their routes served by the default router until labeled. You may want to integrate the labeling into your cluster provisioning workflow, or use a controller/CronJob to automatically label routes in new HCP namespaces.

### Considerations

- **DNS**: All sharded routers serve the same `*.apps` domain. Ensure DNS for the apps domain resolves to all router LoadBalancer IPs, or use separate wildcard DNS entries per shard if you assign custom hostnames.
- **MetalLB IP consumption**: Each additional `IngressController` with `LoadBalancerService` strategy requires its own IP from the MetalLB pool.
- **Scaling**: You can adjust `spec.replicas` on each `IngressController` independently based on the load of each shard.
- **KAS routes**: If using `Route` strategy for the APIServer with dedicated DNS (`Route.Hostname` set), the KAS route gets the `hypershift.openshift.io/hosted-control-plane` label and is served by the per-HCP router, not the default router. In that case, it does not need a shard label.
