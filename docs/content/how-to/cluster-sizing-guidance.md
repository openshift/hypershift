# Hosted Control Plane Sizing Guidance

This guide is for administrators of **self-managed** management clusters who need to
right-size hosted control planes (HCPs) and the management cluster that hosts them.

!!! note "ClusterSizingConfiguration is not the answer for self-managed clusters"

    The `ClusterSizingConfiguration` API and the t-shirt-size machinery around it are
    currently used and tested only by managed services (ROSA HCP, ARO HCP). They are
    not enabled or validated for self-managed use, and this guide deliberately does not
    recommend them.

    The direction for self-managed sizing is to have HyperShift optionally create VPA
    resources for critical control plane workloads automatically. Until that exists,
    use one of the four options below.

## How resource requests work in a hosted control plane

Understanding one behavior makes everything else in this guide make sense:

!!! warning "Control plane resource requests are set once, at creation, and are not reconciled afterwards"

    When the control plane operator (CPO) reconciles a control plane Deployment or
    StatefulSet, it reads the live object first and **preserves the resource requests
    already on it**, discarding the values in its own manifest. This is intentional: it
    lets a service provider (or a VPA) resize control plane workloads in place without
    the CPO reverting the change on the next reconcile.

    Two consequences:

    - Changing the shipped defaults only affects control planes created *after* the
      change. Existing control planes keep the requests they were created with,
      including across a HyperShift or OCP upgrade.
    - Any change you make directly on a control plane Deployment or StatefulSet sticks.

    The one exception is the `resource-request-override.hypershift.openshift.io`
    annotation, which is re-applied on top of the preserved values on every reconcile.
    See [Option 3](#option-3-hostedcluster-resource-request-annotations).

Relevant code: `reconcileWorkload` and `setDefaultOptions` in
`support/controlplane-component/`.

## Recommended values by cluster size

The values below are the recommended **requests per container, per replica**. Multiply
by 3 for `HighlyAvailable` control planes. They apply to the three components that
dominate a hosted control plane's footprint; everything else can be left at its default.

| Profile | Worker nodes | kube-apiserver | etcd | openshift-apiserver |
|---------|--------------|----------------|------|---------------------|
| **Small** | 0–10 | `500m` / `3Gi` | `300m` / `1Gi` | `250m` / `500Mi` |
| **Medium** | 11–100 | `2` / `8Gi` | `1` / `4Gi` | `500m` / `1Gi` |
| **Large** | 101+ | `4` / `16Gi` | `2` / `8Gi` | `1` / `2Gi` |

The **Small** row is what HyperShift ships as the default, so a small cluster needs no
action at all.

!!! note "Requests, not limits"

    HyperShift does not set CPU or memory limits on these containers. A control plane
    can burst above its request when the management node has spare capacity. Requests
    determine scheduling and the guaranteed floor, which is why they are what you size.

### Worker node count is a proxy, not a measurement

Shift a cluster up a profile earlier than the node count suggests when:

- **Object count is high.** etcd holds its whole keyspace in memory, so a cluster with
  tens of thousands of Secrets, ConfigMaps or CRs needs the Large etcd request well
  before it reaches 101 worker nodes.
- **Watch count is high.** Operators, service meshes and GitOps controllers each hold
  long-lived watches against kube-apiserver. A 30-node cluster running a large operator
  catalog can outgrow the Medium kube-apiserver request.
- **Project count is high.** openshift-apiserver serves every project, route, image and
  build request. Multi-tenant clusters with hundreds of projects should start at the
  Medium openshift-apiserver request.

### Measure before you commit

Treat the table as a starting point and set requests at roughly the p95 of your own
observed usage, with headroom for spikes. Memory over the last week, per control plane:

```promql
quantile_over_time(0.95,
  sum by (namespace, container) (
    container_memory_working_set_bytes{container=~"kube-apiserver|etcd|openshift-apiserver"}
  )[7d:5m]
)
```

CPU over the same window:

```promql
quantile_over_time(0.95,
  sum by (namespace, container) (
    rate(container_cpu_usage_seconds_total{container=~"kube-apiserver|etcd|openshift-apiserver"}[5m])
  )[7d:5m]
)
```

## The four options available today

| Option | Scope | Applies to existing clusters | Survives pod recreation | Effort |
|--------|-------|------------------------------|-------------------------|--------|
| [1. Defaults](#option-1-use-the-out-of-the-box-defaults) | Fleet | n/a | Yes | None |
| [2. Edit the workload](#option-2-set-requests-directly-on-the-deployment-or-statefulset) | One component, one cluster | Yes | Yes | Manual, per cluster |
| [3. Annotations](#option-3-hostedcluster-resource-request-annotations) | One component, one cluster | Yes | Yes | Low, declarative |
| [4. VPA](#option-4-run-a-vpa-on-the-management-cluster) | Fleet or per cluster | Yes | Yes | Operator install |

### Option 1: use the out-of-the-box defaults

Do nothing. Every hosted control plane is created with the **Small** profile above.
This is the right choice for clusters up to roughly 10 worker nodes with ordinary API
traffic, and it is where most self-managed clusters should stay.

### Option 2: set requests directly on the Deployment or StatefulSet

Because CPO does not reconcile resource requests, you can edit a control plane workload
in place and the change will persist:

```bash
oc set resources deployment/kube-apiserver -n clusters-example \
  --containers=kube-apiserver --requests=cpu=2,memory=8Gi

oc set resources statefulset/etcd -n clusters-example \
  --containers=etcd --requests=cpu=1,memory=4Gi

oc set resources deployment/openshift-apiserver -n clusters-example \
  --containers=openshift-apiserver --requests=cpu=500m,memory=1Gi
```

**Use this when** you need to resize an existing control plane immediately, or you are
responding to an incident.

**Be aware:**

- It is imperative and per cluster. Nothing records *why* the cluster was resized, and
  nothing re-applies it if the control plane namespace is recreated (for example after
  a disaster-recovery restore or a cross-cluster migration).
- Editing a Deployment or StatefulSet triggers a rollout of that component.

### Option 3: HostedCluster resource request annotations

The `resource-request-override.hypershift.openshift.io` annotation is the declarative
version of Option 2. Set it on the **HostedCluster**; the HyperShift Operator syncs it
to the HostedControlPlane, and CPO applies it to the matching container on every
reconcile — on top of the preserved live requests.

#### Format

```text
resource-request-override.hypershift.openshift.io/<workload>.<container>: <resource>=<quantity>[,<resource>=<quantity>]
```

- `<workload>` is the control plane component name — the name of the Deployment or
  StatefulSet in the control plane namespace, for example `kube-apiserver`, `etcd`,
  `openshift-apiserver`, `control-plane-operator`.
- `<container>` is the container name within that workload. It may be an init container.
- The value is a comma-separated list of `resource=quantity` pairs using standard
  Kubernetes quantity notation.

#### Examples

Size a cluster to the Medium profile at creation time:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: example
  namespace: clusters
  annotations:
    resource-request-override.hypershift.openshift.io/kube-apiserver.kube-apiserver: cpu=2,memory=8Gi
    resource-request-override.hypershift.openshift.io/etcd.etcd: cpu=1,memory=4Gi
    resource-request-override.hypershift.openshift.io/openshift-apiserver.openshift-apiserver: cpu=500m,memory=1Gi
```

Or on an existing HostedCluster:

```bash
oc annotate hostedcluster -n clusters example \
  'resource-request-override.hypershift.openshift.io/kube-apiserver.kube-apiserver=cpu=2,memory=8Gi' \
  --overwrite
```

Shrink a development cluster to pack more of them onto a small management cluster:

```bash
oc annotate hostedcluster -n clusters dev \
  'resource-request-override.hypershift.openshift.io/kube-apiserver.kube-apiserver=cpu=250m,memory=1500Mi' \
  'resource-request-override.hypershift.openshift.io/etcd.etcd=cpu=100m,memory=600Mi' \
  --overwrite
```

Verify it took effect:

```bash
oc get deployment kube-apiserver -n clusters-example \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="kube-apiserver")].resources.requests}'
```

#### Behavior and caveats

- **Overrides merge per resource.** Specifying only `memory=8Gi` leaves the CPU request
  at whatever it already was.
- **Only requests are overridden.** No CPU or memory limits are set. The one exception
  is non-overcommittable extended resources such as `aro.openshift.io/swift-nic`, where
  the limit is set equal to the request because the API server requires it.
- **Invalid quantities are silently ignored.** A malformed value is skipped with no
  error surfaced on the HostedCluster. Always verify with the command above.
- **Removing the annotation does not restore the default.** The annotation stops being
  applied, but the preserved live request stays where the override left it. To go back
  to the shipped default, set the request explicitly with Option 2.
- **Changing an override restarts the pod.** On a `SingleReplica` cluster, overriding
  `kube-apiserver` causes a brief API outage. On `HighlyAvailable` clusters the rollout
  is graceful.
- **The scheduler must be able to place the pod.** An override larger than the
  allocatable capacity of every management cluster node leaves the pod Pending
  indefinitely.
- **This annotation conflicts with a VPA in an active update mode.** Pick one or the
  other per workload; see Option 4.

### Option 4: run a VPA on the management cluster

Because CPO preserves live resource requests, a Vertical Pod Autoscaler can resize
control plane workloads continuously and CPO will not fight it. This is the closest
thing to hands-off sizing available today, and it is the direction HyperShift is
heading for self-managed clusters.

#### Install the VPA operator

Install the Vertical Pod Autoscaler operator on the **management** cluster via OLM. It
only supports the `OwnNamespace` install mode, so it needs its own namespace. The full
install steps are in
[Resource-Based Control Plane Autoscaling](resource-based-control-plane-autoscaling.md#vpa-operator).

!!! important

    That guide configures the `VerticalPodAutoscalerController` with
    `recommendationOnly: true`, because the managed-services feature it describes only
    consumes VPA *recommendations*. For the self-managed use case here you want the VPA
    to actually apply its recommendations, so leave `recommendationOnly` unset or
    `false` on the `default` controller instance.

#### Create VPAs for the critical control plane workloads

Create one VPA per workload, per control plane namespace:

```yaml
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: kube-apiserver
  namespace: clusters-example
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: kube-apiserver
  updatePolicy:
    updateMode: Auto
  resourcePolicy:
    containerPolicies:
    - containerName: kube-apiserver
      controlledResources: ["cpu", "memory"]
      controlledValues: RequestsOnly
      minAllowed:
        cpu: 500m
        memory: 3Gi
      maxAllowed:
        cpu: "4"
        memory: 16Gi
    # Leave the sidecars alone.
    - containerName: "*"
      mode: "Off"
---
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: etcd
  namespace: clusters-example
spec:
  targetRef:
    apiVersion: apps/v1
    kind: StatefulSet
    name: etcd
  updatePolicy:
    updateMode: Auto
  resourcePolicy:
    containerPolicies:
    - containerName: etcd
      controlledResources: ["cpu", "memory"]
      controlledValues: RequestsOnly
      minAllowed:
        cpu: 300m
        memory: 1Gi
      maxAllowed:
        cpu: "2"
        memory: 8Gi
    - containerName: "*"
      mode: "Off"
---
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: openshift-apiserver
  namespace: clusters-example
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: openshift-apiserver
  updatePolicy:
    updateMode: Auto
  resourcePolicy:
    containerPolicies:
    - containerName: openshift-apiserver
      controlledResources: ["cpu", "memory"]
      controlledValues: RequestsOnly
      minAllowed:
        cpu: 250m
        memory: 500Mi
      maxAllowed:
        cpu: "1"
        memory: 2Gi
    - containerName: "*"
      mode: "Off"
```

Inspect what the VPA is recommending:

```bash
oc get vpa -A -o custom-columns=\
NS:.metadata.namespace,\
TARGET:.spec.targetRef.name,\
RECOMMENDATION:.status.recommendation.containerRecommendations
```

#### Guidance

- **Start in `updateMode: "Off"`.** Let the recommender observe for at least a week,
  compare its numbers to the table above, then switch to `Auto` once you trust them.
- **Always set `minAllowed` and `maxAllowed`.** Without a floor, the VPA will shrink an
  idle control plane to the point where it cannot absorb a burst. Without a ceiling, one
  runaway cluster can consume a whole management node. Use the Small profile as
  `minAllowed` and the Large profile as `maxAllowed`.
- **Use `controlledValues: RequestsOnly`.** HyperShift does not set limits on these
  containers and the VPA should not start.
- **Restrict the VPA to the main container.** The `containerName: "*"` entry with
  `mode: "Off"` keeps the VPA away from konnectivity, token-minter and audit-log
  sidecars, whose usage is unrelated to guest cluster load.
- **`updateMode: Auto` evicts pods to resize them.** On `SingleReplica` control planes
  that is a visible API outage. Restrict `Auto` to `HighlyAvailable` clusters, or pair
  it with a PodDisruptionBudget.
- **Do not combine `Auto` with Option 3 on the same container.** The annotation is
  re-applied on every CPO reconcile and the VPA will resize it back, producing a
  rollout loop.

## Management cluster sizing

### Per-HostedCluster footprint

A hosted control plane's footprint is dominated by kube-apiserver, etcd and
openshift-apiserver, multiplied by the replica count of the availability policy:

| Profile | Availability policy | CPU requested | Memory requested |
|---------|---------------------|---------------|------------------|
| Small | `SingleReplica` | ≈ 3 vCPU | ≈ 10 GiB |
| Small | `HighlyAvailable` | ≈ 6 vCPU | ≈ 22 GiB |
| Medium | `HighlyAvailable` | ≈ 14 vCPU | ≈ 47 GiB |
| Large | `HighlyAvailable` | ≈ 24 vCPU | ≈ 86 GiB |

!!! note

    These are *requested* totals for the full set of control plane workloads
    (kube-apiserver, etcd, the OpenShift API servers, controller managers, OLM catalogs,
    CVO, konnectivity, the CPO itself, and so on), summed from the CPO reference
    manifests. Your exact totals will differ with platform and enabled capabilities.
    Actual consumption on an idle cluster is well below the requested total, but size
    the management cluster on requests, because requests are what the scheduler
    enforces.

### Worker node sizing on the management cluster

Reserve roughly 20–25% of each node for kubelet, CRI-O and the management cluster's own
operators.

| HostedClusters | Suggested worker node size | Minimum worker nodes (HA HCPs) |
|----------------|----------------------------|--------------------------------|
| 1–5 (Small) | 8 vCPU / 32 GiB | 3 |
| 6–20 (Small/Medium) | 16 vCPU / 64 GiB | 6 |
| 21–50 (mixed) | 32 vCPU / 128 GiB | 9 |
| 50+ | 32 vCPU / 128 GiB or larger | 12+ |

- **Do not use nodes smaller than 8 vCPU / 32 GiB.** A Large kube-apiserver pod alone
  requests 16 GiB; on a 32 GiB node that leaves little for anything else, and on a
  smaller node it is unschedulable.
- **Prefer nodes that fit 2–4 whole control planes.** That keeps the blast radius of a
  node failure small without paying per-node overhead many times over.
- **Spread across at least three availability zones.** HyperShift sets hostname
  anti-affinity on `HighlyAvailable` components; with fewer than three schedulable
  zones, replicas stay Pending.
- **Reserve headroom for churn.** Cluster creation, upgrades and resizes all need spare
  capacity to schedule new pods before old ones terminate. Keep at least one node's
  worth of unallocated capacity per zone.

### Isolating control plane workloads

Beyond a handful of HostedClusters, dedicate management cluster nodes to control plane
workloads:

```bash
oc label node <node> hypershift.openshift.io/control-plane=true
oc adm taint node <node> hypershift.openshift.io/control-plane=true:NoSchedule
```

HyperShift already tolerates this taint and prefers labelled nodes. See
[Distribute HostedCluster workloads](distribute-hosted-cluster-workloads.md) for the
full set of labels, taints and node selectors.

### Storage and etcd

etcd runs as a StatefulSet with a PersistentVolume per member. Use low-latency
SSD-backed storage (for example, AWS `gp3` with provisioned IOPS). etcd is sensitive to
fsync latency, and slow disks surface as API server timeouts rather than storage errors.
Budget 3 PVs per `HighlyAvailable` HostedCluster.

### Verifying capacity

```bash
oc adm top nodes -l hypershift.openshift.io/control-plane=true
oc describe node <node> | sed -n '/Allocated resources/,/Events/p'
```

Requested CPU per control plane namespace:

```promql
sum by (namespace) (
  kube_pod_container_resource_requests{resource="cpu"}
  * on(pod, namespace) group_left kube_pod_labels{label_hypershift_openshift_io_control_plane_component!=""}
)
```

If allocated requests exceed roughly 80% of allocatable on your control plane nodes, add
capacity before creating more HostedClusters.

## Upgrade considerations

HyperShift's shipped defaults changed in a recent release:

| Component | Container | Previous | Current |
|-----------|-----------|----------|---------|
| `kube-apiserver` | `kube-apiserver` | `350m` / `2Gi` | `500m` / `3Gi` |
| `etcd` | `etcd` | `300m` / `600Mi` | `300m` / `1Gi` |
| `openshift-apiserver` | `openshift-apiserver` | `100m` / `200Mi` | `250m` / `500Mi` |

**Existing hosted control planes are not affected.** As described
[above](#how-resource-requests-work-in-a-hosted-control-plane), CPO preserves the live
requests of workloads that already exist, so upgrading HyperShift or OCP will not resize
them and will not trigger a rollout for this reason.

**Newly created hosted control planes request more.** The deltas are `+150m` CPU and
`+1Gi` memory for kube-apiserver, `+400Mi` memory for etcd, and `+150m` CPU and `+300Mi`
memory for openshift-apiserver — per replica, so three times that on a
`HighlyAvailable` control plane. That is roughly `+0.9` vCPU and `+5.1` GiB of requested
capacity per new HA hosted cluster.

Before creating hosted clusters on a management cluster that is running near capacity,
check available headroom with the commands in
[Verifying capacity](#verifying-capacity). If you need the old footprint on a specific
cluster, set it explicitly with [Option 3](#option-3-hostedcluster-resource-request-annotations).

## Related documentation

- [Resource-Based Control Plane Autoscaling](resource-based-control-plane-autoscaling.md)
- [Distribute HostedCluster workloads](distribute-hosted-cluster-workloads.md)
- [Configure the Kube API server](configure-ocp-components/kubeapi-server.md)
- [Cluster autoscaling (data plane)](autoscaling.md)
