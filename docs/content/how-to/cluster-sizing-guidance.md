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

!!! warning "There is no one-size-fits-all number"

    Control plane resource consumption depends on the platform (AWS, Azure, KubeVirt,
    bare metal, IBM Cloud), the shape of the guest workload (node count, pod count, API
    churn, object count, number of active watches) and the enabled capability set. Any
    table in this document is a **starting point for your own measurement**, not a
    recommendation to apply blindly. Size from measurements taken in your own
    environment — see [Measure your own environment](#measure-your-own-environment).

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
    That is deliberate — it is the same mechanism managed services use to move a cluster
    from one size to another. See
    [Option 3](#option-3-hostedcluster-resource-request-annotations).

Relevant code: `reconcileWorkload` and `setDefaultOptions` in
`support/controlplane-component/`.

## The shipped defaults

Every hosted control plane is created with these requests, per container, per replica:

| Component | Container | CPU request | Memory request |
|-----------|-----------|-------------|----------------|
| `kube-apiserver` | `kube-apiserver` | `350m` | `2Gi` |
| `etcd` | `etcd` | `300m` | `600Mi` |
| `openshift-apiserver` | `openshift-apiserver` | `100m` | `200Mi` |

These are the three most scale-sensitive components, not the whole control plane. Other
workloads — `kube-controller-manager` (`100m` / `600Mi`, memory on par with etcd),
`openshift-oauth-apiserver`, CVO, OLM catalogs, konnectivity, the CPO itself and the
various sidecars — add roughly 1.9 vCPU and 5.7 GiB in aggregate at default request
levels. See [Per-HostedCluster footprint](#per-hostedcluster-footprint) for the totals
and for how to measure them on your own management cluster.

Two things to understand about these values:

- **They are intentional minimums, not target sizes.** They are set low enough that a
  hosted control plane can be scheduled and started on a modest management cluster.
  They are not a prediction of steady-state consumption under load.
- **No CPU or memory limits are set on these containers.** A control plane can burst
  well above its request whenever the management node has spare capacity. Requests
  govern scheduling and the guaranteed floor, which is why requests are the thing you
  size.

These baselines were originally derived around AWS-shaped assumptions. They will not be
equally appropriate on Azure, KubeVirt, bare metal or IBM Cloud, and managed products
(ROSA, ARO) apply different values of their own.

## Measure your own environment

Sizing decisions for self-managed clusters should be driven by measurements from your
own management cluster, not by a table. Observe for at least a week across a
representative workload, then set requests at roughly the p95 of observed usage with
headroom for spikes.

!!! important "Keep these queries per replica"

    Resource requests are set per container, per replica, so the measurements you
    compare them against must be per replica too. Every query below groups by `pod` for
    that reason. Dropping `pod` from the grouping sums all replicas of a
    `HighlyAvailable` control plane into one series, which would overstate the required
    request by roughly the replica count.

Memory working set over the last week, per control plane container, per replica:

```promql
quantile_over_time(0.95,
  sum by (namespace, pod, container) (
    container_memory_working_set_bytes{container=~"kube-apiserver|etcd|openshift-apiserver"}
  )[7d:5m]
)
```

CPU over the same window:

```promql
quantile_over_time(0.95,
  sum by (namespace, pod, container) (
    rate(container_cpu_usage_seconds_total{container=~"kube-apiserver|etcd|openshift-apiserver"}[5m])
  )[7d:5m]
)
```

Peak rather than p95, to size the headroom above the request:

```promql
max_over_time(
  sum by (namespace, pod, container) (
    container_memory_working_set_bytes{container=~"kube-apiserver|etcd|openshift-apiserver"}
  )[7d:5m]
)
```

How far current usage sits above the configured request, which is the signal that a
control plane has outgrown its defaults:

```promql
sum by (namespace, pod, container) (
  container_memory_working_set_bytes{container=~"kube-apiserver|etcd|openshift-apiserver"}
)
/
sum by (namespace, pod, container) (
  kube_pod_container_resource_requests{resource="memory",
    container=~"kube-apiserver|etcd|openshift-apiserver"}
)
```

If that ratio sits consistently above 1, the control plane is relying on burst capacity
it is not guaranteed, and it is a candidate for resizing.

### Illustrative starting points by cluster size

The values below are **not recommendations** and are not validated by perf and scale
testing. They are a rough sense of scale to compare your own measurements against, in
requests per container, per replica. Multiply by 3 for `HighlyAvailable` control planes.

| Cluster shape | Worker nodes | kube-apiserver | etcd | openshift-apiserver |
|---------------|--------------|----------------|------|---------------------|
| Small (defaults) | 0–10 | `350m` / `2Gi` | `300m` / `600Mi` | `100m` / `200Mi` |
| Medium | 11–100 | `2` / `8Gi` | `1` / `4Gi` | `500m` / `1Gi` |
| Large | 101+ | `4` / `16Gi` | `2` / `8Gi` | `1` / `2Gi` |

The Small row is what HyperShift ships, so a small cluster needs no action at all.

Worker node count is a weak proxy for control plane load. Consider moving up a row
earlier than the node count suggests when:

- **Object count is high.** etcd holds its whole keyspace in memory, so a cluster with
  tens of thousands of Secrets, ConfigMaps or CRs needs more etcd memory well before it
  reaches 101 worker nodes.
- **Watch count is high.** Operators, service meshes and GitOps controllers each hold
  long-lived watches against kube-apiserver. A 30-node cluster running a large operator
  catalog can outgrow the Medium kube-apiserver value.
- **Project count is high.** openshift-apiserver serves every project, route, image and
  build request. Multi-tenant clusters with hundreds of projects will need more than the
  default openshift-apiserver request.

## The four options available today

| Option | Scope | Applies to existing clusters | Survives pod recreation | Effort |
|--------|-------|------------------------------|-------------------------|--------|
| [1. Defaults](#option-1-use-the-out-of-the-box-defaults) | Fleet | n/a | Yes | None |
| [2. Edit the workload](#option-2-set-requests-directly-on-the-deployment-or-statefulset) | One component, one cluster | Yes | Yes | Manual, per component per cluster |
| [3. Annotations](#option-3-hostedcluster-resource-request-annotations) | One component, one cluster | Yes | Yes | Low, declarative, per cluster |
| [4. VPA](#option-4-run-a-vpa-on-the-management-cluster) | Per workload, per cluster | Yes | Yes | Operator install, plus a VPA resource per workload per cluster |

Note that none of these options is fleet-wide except leaving the defaults alone. Options
2, 3 and 4 all require a per-hosted-cluster action; the VPA option additionally requires
creating a VPA resource for each control plane workload you want managed. Until
HyperShift ships a controller that creates those VPA resources automatically, that is
manual work that scales with the number of hosted clusters.

### Option 1: use the out-of-the-box defaults

Do nothing. This is the right choice for clusters up to roughly 10 worker nodes with
ordinary API traffic, and it is where most self-managed clusters should stay. Because no
limits are set, a control plane that occasionally exceeds its requests will still be
served as long as the management node has spare capacity.

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

!!! danger "Every entry must contain an `=` separator"

    Each entry in the value must use `resource=quantity` form, for example
    `cpu=500m,memory=2Gi`. An invalid *quantity* (`cpu=banana`) is silently ignored, but
    an entry missing the `=` separator entirely (`cpu500m`) will **crash the control
    plane operator reconciler** for that hosted cluster. Double-check the format before
    applying the annotation.

#### Examples

Size a cluster above the defaults at creation time:

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
  'resource-request-override.hypershift.openshift.io/etcd.etcd=cpu=100m,memory=400Mi' \
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
- **Invalid quantities are silently ignored.** `cpu=banana` is skipped with no error
  surfaced on the HostedCluster. Always verify with the command above. Note that this
  tolerance does not extend to a missing `=` separator, which crashes the reconciler —
  see the warning above.
- **Removing the annotation does not restore the default.** The annotation stops being
  applied, but the preserved live request stays where the override last set it. To go
  back to the shipped default, set the request explicitly with Option 2.
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
thing to measurement-driven sizing available today, and it is the direction HyperShift
is heading for self-managed clusters.

!!! important "This is not a fleet-wide switch"

    Installing the VPA operator is only the first step. VPA acts on a specific
    `targetRef`, so you need one `VerticalPodAutoscaler` resource per workload, per
    control plane namespace — three per hosted cluster if you manage kube-apiserver,
    etcd and openshift-apiserver. Unless you write a controller to create them, this is
    manual work that grows with every hosted cluster you add, and new hosted clusters
    will not be covered until someone creates their VPA resources.

#### Install the VPA operator

Install the Vertical Pod Autoscaler operator on the **management** cluster via OLM. It
only supports the `OwnNamespace` install mode, so it needs its own namespace. The full
install steps are in
[Resource-Based Control Plane Autoscaling](resource-based-control-plane-autoscaling.md#vpa-operator).

!!! note

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
    updateMode: "Off"  # start here; switch to "Auto" after validating recommendations
  resourcePolicy:
    containerPolicies:
    - containerName: kube-apiserver
      controlledResources: ["cpu", "memory"]
      controlledValues: RequestsOnly
      minAllowed:
        cpu: 350m
        memory: 2Gi
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
    updateMode: "Off"  # start here; switch to "Auto" after validating recommendations
  resourcePolicy:
    containerPolicies:
    - containerName: etcd
      controlledResources: ["cpu", "memory"]
      controlledValues: RequestsOnly
      minAllowed:
        cpu: 300m
        memory: 600Mi
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
    updateMode: "Off"  # start here; switch to "Auto" after validating recommendations
  resourcePolicy:
    containerPolicies:
    - containerName: openshift-apiserver
      controlledResources: ["cpu", "memory"]
      controlledValues: RequestsOnly
      minAllowed:
        cpu: 100m
        memory: 200Mi
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

- **Start in `updateMode: "Off"`, as shown above.** Let the recommender observe for at
  least a week, compare its numbers against your own measurements, then switch to `Auto`
  once you trust them.
- **Always set `minAllowed` and `maxAllowed`.** Without a floor, the VPA will shrink an
  idle control plane to the point where it cannot absorb a burst. Without a ceiling, one
  runaway cluster can consume a whole management node. The shipped defaults are a
  reasonable `minAllowed`.
- **Use `controlledValues: RequestsOnly`.** HyperShift does not set limits on these
  containers and the VPA should not start.
- **Restrict the VPA to the main container.** The `containerName: "*"` entry with
  `mode: "Off"` keeps the VPA away from konnectivity, token-minter and audit-log
  sidecars, whose usage is unrelated to guest cluster load.
- **`updateMode: Auto` evicts pods to resize them.** On `SingleReplica` control planes
  that is an unavoidable API outage, and a PodDisruptionBudget does not help: one that
  blocks the eviction stops the VPA from ever resizing, and one that permits it permits
  the outage. Restrict `Auto` to `HighlyAvailable` clusters, and on `SingleReplica`
  clusters either stay in `"Off"` and apply the recommendations during a maintenance
  window, or use [Option 3](#option-3-hostedcluster-resource-request-annotations).
- **Do not combine `Auto` with Option 3 on the same container.** The annotation is
  re-applied on every CPO reconcile and the VPA will resize it back, producing a
  rollout loop.

## Management cluster sizing

### Per-HostedCluster footprint

A hosted control plane's footprint is dominated by kube-apiserver, etcd and
openshift-apiserver, multiplied by the replica count of the availability policy:

| Cluster shape | Availability policy | CPU requested | Memory requested |
|---------------|---------------------|---------------|------------------|
| Small (defaults) | `SingleReplica` | ≈ 2.7 vCPU | ≈ 8.5 GiB |
| Small (defaults) | `HighlyAvailable` | ≈ 5 vCPU | ≈ 17 GiB |
| Medium | `HighlyAvailable` | ≈ 13 vCPU | ≈ 47 GiB |
| Large | `HighlyAvailable` | ≈ 24 vCPU | ≈ 86 GiB |

!!! note

    These are *requested* totals for the full set of control plane workloads
    (kube-apiserver, etcd, the OpenShift API servers, controller managers, OLM catalogs,
    CVO, konnectivity, the CPO itself, and so on), summed from the CPO reference
    manifests, using the illustrative values above for the Medium and Large rows. Actual
    consumption on an idle cluster is well below the requested total, but size the
    management cluster on requests, because requests are what the scheduler enforces.

#### Measure the footprint instead of trusting the table

These totals vary by platform, OCP version and enabled capabilities, and this table will
drift as the control plane gains and loses components. Rather than relying on it staying
current, measure a real hosted control plane on your own management cluster.

To sum the requested resources for one hosted control plane, where
`clusters-<hostedcluster-name>` is the HCP namespace:

```bash
oc get pods -n "clusters-example" -o json | \
  jq '[.items[].spec.containers[].resources.requests // {}] |
    { cpu_millicores: [.[].cpu // "0" |
        if endswith("m") then rtrimstr("m") | tonumber
        else tonumber * 1000 end] | add,
      memory_MiB: [.[].memory // "0" |
        if endswith("Gi") then rtrimstr("Gi") | tonumber * 1024
        elif endswith("Mi") then rtrimstr("Mi") | tonumber
        else 0 end] | add
    } | "CPU: \(.cpu_millicores)m (\(.cpu_millicores / 1000) cores)",
        "Memory: \(.memory_MiB)Mi (\(.memory_MiB / 1024 | . * 10 | round / 10) GiB)"'
```

Example output:

```text
"CPU: 2245m (2.245 cores)"
"Memory: 8822Mi (8.6 GiB)"
```

The equivalent PromQL, if you have monitoring configured, is in
[Verifying capacity](#verifying-capacity). If your numbers differ significantly from the
table above, size your management cluster on what you observe, not on the table.

### Worker node sizing on the management cluster

Reserve roughly 20–25% of each node for kubelet, CRI-O and the management cluster's own
operators.

| HostedClusters | Suggested worker node size | Minimum worker nodes (HA HCPs) |
|----------------|----------------------------|--------------------------------|
| 1–5 (Small) | 8 vCPU / 32 GiB | 3 |
| 6–20 (Small/Medium) | 16 vCPU / 64 GiB | 6 |
| 21–50 (mixed) | 32 vCPU / 128 GiB | 9 |
| 50+ | 32 vCPU / 128 GiB or larger | 12+ |

- **Do not use nodes smaller than 8 vCPU / 32 GiB.** A kube-apiserver pod sized for a
  large cluster can request 16 GiB on its own; on a 32 GiB node that leaves little for
  anything else, and on a smaller node it is unschedulable.
- **Prefer nodes that fit 2–4 whole control planes.** That keeps the blast radius of a
  node failure small without paying per-node overhead many times over.
- **Spread across at least three availability zones.** HyperShift sets required zone
  anti-affinity and hostname anti-affinity on `HighlyAvailable` components (the zone
  constraint is relaxed to preferred on OpenStack and KubeVirt). With fewer than three
  schedulable zones on platforms with required zone spread, replicas stay Pending.
- **Reserve headroom for churn.** Cluster creation, upgrades and resizes all need spare
  capacity to schedule new pods before old ones terminate. Keep at least one node's
  worth of unallocated capacity per zone.
- **Remember that requests are not limits.** Control plane containers will use more than
  they request when load demands it. Sizing a management cluster so that requested
  capacity exactly equals allocatable capacity leaves nothing for that burst.

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

## Related documentation

- [Resource-Based Control Plane Autoscaling](resource-based-control-plane-autoscaling.md)
- [Distribute HostedCluster workloads](distribute-hosted-cluster-workloads.md)
- [Configure the Kube API server](configure-ocp-components/kubeapi-server.md)
- [Cluster autoscaling (data plane)](autoscaling.md)
