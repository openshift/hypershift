# Hosted Control Plane Sizing Guidance

This guide is for administrators of **self-managed** management clusters who need to
right-size hosted control planes (HCPs) and the management cluster that hosts them.
Managed services (ROSA HCP, ARO HCP) ship their own tuned sizing configuration; the
values here are the general-purpose baseline for everyone else.

It covers four things:

1. [Recommended ClusterSizingConfiguration profiles](#recommended-clustersizingconfiguration-profiles)
2. [Control plane autoscaling guidance](#control-plane-autoscaling-guidance)
3. [Management cluster sizing](#management-cluster-sizing)
4. [Resource request overrides](#resource-request-overrides)

## Background: where control plane requests come from

Every control plane pod gets its resource requests from one of three layers. Later
layers win:

| Layer | Set by | Scope | Requires a new release? |
|-------|--------|-------|-------------------------|
| **1. Asset baseline** | CPO manifests under `control-plane-operator/controllers/hostedcontrolplane/v2/assets/` | Every HostedCluster | Yes |
| **2. Size effects** | `ClusterSizingConfiguration` `spec.sizes[].effects.resourceRequests` | Every HostedCluster in a t-shirt size | No — edit the CR |
| **3. Per-cluster override** | `resource-request-override.hypershift.openshift.io/<deployment>.<container>` annotation on the HostedCluster | One HostedCluster | No — edit the HostedCluster |

The asset baseline is deliberately sized for the *smallest* supported control plane.
Anything larger should be expressed as a size effect (layer 2) or, for one-off tuning,
as an annotation (layer 3). Layer 2 and layer 3 use the same mechanism internally: the
sizing controller renders size effects into `resource-request-override...` annotations
on the HostedCluster, and the control plane operator applies those annotations to the
matching container.

Current asset baselines for the three largest consumers:

| Component | Container | CPU | Memory |
|-----------|-----------|-----|--------|
| `kube-apiserver` | `kube-apiserver` | `500m` | `3Gi` |
| `etcd` | `etcd` | `300m` | `1Gi` |
| `openshift-apiserver` | `openshift-apiserver` | `250m` | `500Mi` |

!!! note

    Requests are *requests*, not limits. HyperShift does not set CPU limits on these
    containers, so a control plane can burst above its request when the management
    node has spare capacity. Requests determine scheduling and the guaranteed floor.

## Recommended ClusterSizingConfiguration profiles

`ClusterSizingConfiguration` is a cluster-scoped, singleton resource named `cluster`. It
sorts HostedClusters into t-shirt sizes by worker node count and applies per-size
effects. The HyperShift Operator creates it with the defaults below the first time it
starts.

Size effects are only applied when **size tagging is enabled** on the management
cluster:

```bash
hypershift install --enable-size-tagging
```

Without size tagging, every HostedCluster runs on the layer-1 asset baseline (the
`small` profile) regardless of how many worker nodes it has.

### Default profiles

| Size | Worker nodes | kube-apiserver | etcd | openshift-apiserver |
|------|--------------|----------------|------|---------------------|
| `small` | 0–10 | `500m` / `3Gi` | `300m` / `1Gi` | `250m` / `500Mi` |
| `medium` | 11–100 | `2` / `8Gi` | `1` / `4Gi` | `500m` / `1Gi` |
| `large` | 101+ | `4` / `16Gi` | `2` / `8Gi` | `1` / `2Gi` |

The `small` row is identical to the asset baseline by design, so a small cluster is
sized the same whether or not size tagging is enabled.

### Applying the profiles

```yaml
apiVersion: scheduling.hypershift.openshift.io/v1alpha1
kind: ClusterSizingConfiguration
metadata:
  name: cluster
spec:
  sizes:
  - name: small
    criteria:
      from: 0
      to: 10
    effects:
      resourceRequests:
      - deploymentName: kube-apiserver
        containerName: kube-apiserver
        cpu: 500m
        memory: 3Gi
      - deploymentName: etcd
        containerName: etcd
        cpu: 300m
        memory: 1Gi
      - deploymentName: openshift-apiserver
        containerName: openshift-apiserver
        cpu: 250m
        memory: 500Mi
  - name: medium
    criteria:
      from: 11
      to: 100
    effects:
      kasGoMemLimit: 6GiB
      maximumRequestsInflight: 800
      maximumMutatingRequestsInflight: 400
      resourceRequests:
      - deploymentName: kube-apiserver
        containerName: kube-apiserver
        cpu: "2"
        memory: 8Gi
      - deploymentName: etcd
        containerName: etcd
        cpu: "1"
        memory: 4Gi
      - deploymentName: openshift-apiserver
        containerName: openshift-apiserver
        cpu: 500m
        memory: 1Gi
  - name: large
    criteria:
      from: 101
    effects:
      kasGoMemLimit: 12GiB
      maximumRequestsInflight: 3000
      maximumMutatingRequestsInflight: 1000
      resourceRequests:
      - deploymentName: kube-apiserver
        containerName: kube-apiserver
        cpu: "4"
        memory: 16Gi
      - deploymentName: etcd
        containerName: etcd
        cpu: "2"
        memory: 8Gi
      - deploymentName: openshift-apiserver
        containerName: openshift-apiserver
        cpu: "1"
        memory: 2Gi
```

Verify that the configuration was accepted:

```bash
oc get clustersizingconfiguration cluster \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterSizingConfigurationValid")]}'
```

Check the size assigned to a HostedCluster:

```bash
oc get hostedcluster -A -L hypershift.openshift.io/cluster-size
```

### Choosing the right criteria for your fleet

Worker node count is a proxy for control plane load, not a measurement of it. Shift the
`criteria` boundaries down if your workloads are unusually API-heavy:

- **Object churn dominates etcd sizing.** etcd holds its whole keyspace in memory, so
  clusters with tens of thousands of Secrets, ConfigMaps or CRs need the `large` etcd
  request well before they reach 101 worker nodes.
- **Watch count dominates kube-apiserver memory.** Operators, service meshes and
  GitOps controllers each open long-lived watches. A 30-node cluster running a large
  operator catalog can outgrow the `medium` kube-apiserver request.
- **Namespace and project count drives openshift-apiserver.** Multi-tenant clusters
  with hundreds of projects should start at the `medium` openshift-apiserver request.

### Tuning the profiles for your own fleet

Do not guess. Measure the fleet you actually run, then set the request at roughly the
p95 of observed usage with headroom for spikes.

Observed usage over the last week, per size class:

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

If you have the VPA operator installed (see below), its recommendations are a
lower-effort starting point:

```bash
oc get vpa -A -o custom-columns=\
NS:.metadata.namespace,\
TARGET:.spec.targetRef.name,\
RECOMMENDATION:.status.recommendation.containerRecommendations
```

Two guardrails when raising the numbers:

- **Changing a size effect restarts control plane pods.** Resource requests are part of
  the pod template, so editing `ClusterSizingConfiguration` rolls every HostedCluster in
  the affected size class. Change one size at a time and use
  `spec.concurrency` to bound how many clusters transition in a sliding window.
- **Sum before you commit.** Multiply the new per-HCP total by your HostedCluster count
  and confirm the management cluster can still schedule it — see the next section.

## Control plane autoscaling guidance

Two distinct autoscaling mechanisms exist, and they solve different problems.

### Node-count-based sizing (default)

The `hostedclustersizing` controller assigns a t-shirt size from the worker node count
and applies that size's effects. This is the default when `--enable-size-tagging` is
set, works on all platforms, and needs no extra operators.

**Use it when:** your control plane load correlates reasonably with worker node count,
which is the common case.

### Resource-based control plane autoscaling

Resource-based autoscaling sizes a HostedCluster from the *observed* kube-apiserver
memory usage (via VPA recommendations) rather than from node count. It is documented in
full in [Resource-Based Control Plane Autoscaling](resource-based-control-plane-autoscaling.md).

**Prerequisites — this feature is not general-purpose:**

- AWS only.
- Requires the request serving isolation architecture
  (`hypershift.openshift.io/topology: dedicated-request-serving-components`).
- Requires the VPA operator installed on the management cluster.
- Requires per-size `capacity` in `ClusterSizingConfiguration`, or MachineSets labelled
  with `hypershift.openshift.io/cluster-size`.

**Enable it when all of the following hold:**

- You run request-serving-isolated HostedClusters on AWS.
- Node count is a poor predictor of load in your fleet — for example, a large share of
  clusters are small in node count but heavy in API traffic, or vice versa.
- You are over-provisioning noticeably: measured p95 kube-apiserver memory is well
  under the request for a whole size class, across many clusters.
- You can absorb the size transitions. Each transition reschedules request-serving pods,
  which is a brief, user-visible API disruption.

**Stay on node-count sizing when:**

- You are not on AWS, or not using request serving isolation. The feature will not
  engage at all.
- Your fleet is small (a handful of HostedClusters). Hand-tuned per-cluster annotations
  are simpler and more predictable.
- Your control plane load is spiky rather than trending. VPA smooths over its
  aggregation window and will lag sharp bursts; a fixed larger size class is safer.

**If you do enable it,** damp the transitions so clusters do not flap between sizes:

```yaml
spec:
  transitionDelay:
    increase: 30s   # react quickly when a cluster needs to grow
    decrease: 30m   # be slow to shrink; confirm the drop is sustained
  concurrency:
    slidingWindow: 10m
    limit: 5        # at most 5 clusters transitioning per 10m window
```

Scale up fast and scale down slowly. The cost of a delayed scale-down is wasted
capacity; the cost of a premature scale-down is a degraded API server.

## Management cluster sizing

### Per-HostedCluster footprint

A hosted control plane's footprint is dominated by kube-apiserver, etcd and
openshift-apiserver, and it multiplies by the replica count of the availability policy.
At the `small` baseline, summing the requests of all reconciled control plane workloads:

| Size | Availability policy | CPU requested | Memory requested |
|------|---------------------|---------------|------------------|
| `small` | `SingleReplica` | ≈ 3 vCPU | ≈ 10 GiB |
| `small` | `HighlyAvailable` | ≈ 6 vCPU | ≈ 22 GiB |
| `medium` | `HighlyAvailable` | ≈ 14 vCPU | ≈ 47 GiB |
| `large` | `HighlyAvailable` | ≈ 24 vCPU | ≈ 86 GiB |

!!! note

    These are *requested* totals for the full set of control plane workloads
    (kube-apiserver, etcd, OpenShift API servers, controller managers, OLM catalogs,
    CVO, konnectivity, the CPO itself, and so on), summed from the CPO reference
    manifests. Your exact totals will differ with platform and enabled capabilities.
    Actual consumption on an idle cluster is well below the requested total, but size
    the management cluster on requests, because requests are what the scheduler
    enforces.

### Worker node sizing on the management cluster

Pick node instance types so that a single control plane's largest pod fits comfortably
with room for the kubelet, CRI-O and the management cluster's own operators. Reserve
roughly 20–25% of each node for system overhead and headroom.

| HostedClusters | Suggested worker node size | Minimum worker nodes (HA HCPs) |
|----------------|----------------------------|--------------------------------|
| 1–5 (`small`) | 8 vCPU / 32 GiB | 3 |
| 6–20 (`small`/`medium`) | 16 vCPU / 64 GiB | 6 |
| 21–50 (mixed) | 32 vCPU / 128 GiB | 9 |
| 50+ | 32 vCPU / 128 GiB or larger, with request serving isolation | 12+ |

Guidance for choosing between "more small nodes" and "fewer large nodes":

- **Do not use nodes smaller than 8 vCPU / 32 GiB.** A single `large` kube-apiserver
  pod requests 16 GiB; on a 32 GiB node that leaves almost nothing for anything else,
  and the pod becomes unschedulable on a smaller node entirely.
- **Prefer nodes that fit 2–4 whole control planes.** That keeps the blast radius of a
  node failure small while avoiding the per-node overhead of many tiny nodes.
- **Spread across at least three availability zones** so that `HighlyAvailable` control
  planes can actually satisfy their multi-zone spread constraints. HyperShift sets
  pod anti-affinity on hostname for HA components; with fewer than three schedulable
  zones, replicas will remain Pending.
- **Reserve headroom for churn.** Cluster creation, upgrades and size transitions all
  need spare capacity to schedule new pods before old ones terminate. Keep at least one
  node's worth of unallocated capacity per zone, or use
  `spec.sizes[].management.placeholders` to hold warm capacity.

### Isolating control plane workloads

For anything beyond a handful of HostedClusters, dedicate management cluster nodes to
control plane workloads so that they cannot be starved by other tenants:

```bash
oc label node <node> hypershift.openshift.io/control-plane=true
oc adm taint node <node> hypershift.openshift.io/control-plane=true:NoSchedule
```

HyperShift already tolerates this taint and prefers labelled nodes. See
[Distribute HostedCluster workloads](distribute-hosted-cluster-workloads.md) for the
full set of labels, taints and node selectors, including per-HostedCluster isolation.

### Storage and etcd

etcd runs as a StatefulSet with a PersistentVolume per member. Use low-latency SSD-backed
storage (for example, AWS `gp3` with provisioned IOPS); etcd is sensitive to fsync
latency, and slow disks surface as API server timeouts rather than as storage errors.
Budget 3 PVs per `HighlyAvailable` HostedCluster.

### Verifying capacity

Compare requested to allocatable capacity before onboarding more HostedClusters:

```bash
oc adm top nodes -l hypershift.openshift.io/control-plane=true
oc describe node <node> | sed -n '/Allocated resources/,/Events/p'
```

Requested CPU and memory per hosted control plane namespace:

```promql
sum by (namespace) (
  kube_pod_container_resource_requests{resource="cpu"}
  * on(pod, namespace) group_left kube_pod_labels{label_hypershift_openshift_io_control_plane_component!=""}
)
```

If allocated requests exceed roughly 80% of allocatable on your control plane nodes, add
capacity before creating more HostedClusters.

## Resource request overrides

The `resource-request-override.hypershift.openshift.io` annotation tunes a single
container in a single HostedCluster. It is the escape hatch for clusters that do not fit
their size class — a workload-specific outlier, a temporary bump during a migration, or
a bespoke cluster you do not want to model as a new t-shirt size.

### Format

```text
resource-request-override.hypershift.openshift.io/<deployment>.<container>: <resource>=<quantity>[,<resource>=<quantity>]
```

- `<deployment>` is the control plane component name — the name of the Deployment or
  StatefulSet in the control plane namespace, for example `kube-apiserver`, `etcd`,
  `openshift-apiserver`, `control-plane-operator`.
- `<container>` is the container name within that workload. It may be an init container.
- The value is a comma-separated list of `resource=quantity` pairs using standard
  Kubernetes quantity notation.

Set the annotation on the **HostedCluster**; the HyperShift Operator propagates it to
the HostedControlPlane, and the control plane operator applies it to the matching
container.

### Examples

Give one API-heavy cluster more kube-apiserver capacity:

```yaml
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: example
  namespace: clusters
  annotations:
    resource-request-override.hypershift.openshift.io/kube-apiserver.kube-apiserver: cpu=4,memory=16Gi
    resource-request-override.hypershift.openshift.io/etcd.etcd: memory=8Gi
```

Or with `oc annotate`:

```bash
oc annotate hostedcluster -n clusters example \
  'resource-request-override.hypershift.openshift.io/openshift-apiserver.openshift-apiserver=cpu=1,memory=2Gi' \
  --overwrite
```

Shrink the control plane of a development cluster to pack more of them onto a small
management cluster:

```bash
oc annotate hostedcluster -n clusters dev \
  'resource-request-override.hypershift.openshift.io/kube-apiserver.kube-apiserver=cpu=250m,memory=1500Mi' \
  'resource-request-override.hypershift.openshift.io/etcd.etcd=cpu=100m,memory=600Mi' \
  --overwrite
```

Remove an override to fall back to the size class or asset baseline:

```bash
oc annotate hostedcluster -n clusters example \
  'resource-request-override.hypershift.openshift.io/kube-apiserver.kube-apiserver-'
```

### Verifying an override took effect

```bash
oc get deployment kube-apiserver -n clusters-example \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="kube-apiserver")].resources.requests}'
```

### Behavior and caveats

- **Overrides merge per resource, they do not replace the whole list.** Specifying only
  `memory=8Gi` leaves the CPU request at whatever the size class or asset baseline set.
- **Only requests are overridden.** Limits are not set for CPU or memory. The one
  exception is non-overcommittable extended resources such as
  `aro.openshift.io/swift-nic`, where the limit is set equal to the request because the
  API server requires it.
- **Invalid quantities are silently ignored.** A malformed value is skipped and the
  container keeps its previous request, with no error surfaced on the HostedCluster.
  Verify with the command above after annotating.
- **Size effects will overwrite manual annotations.** When size tagging is enabled and
  the size class declares a `resourceRequests` entry for the same
  `<deployment>.<container>`, the sizing controller rewrites the annotation on every
  reconcile. To pin a cluster's requests against size effects, either remove that
  container from the size's `resourceRequests` or move the cluster to a size class that
  does not declare it.
- **Changing an override restarts the pod.** Resource requests are part of the pod
  template. Overriding `kube-apiserver` on a `SingleReplica` cluster causes a brief API
  outage; on `HighlyAvailable` clusters the rollout is graceful.
- **The scheduler must be able to place the pod.** An override that exceeds the
  allocatable capacity of every management cluster node leaves the pod Pending
  indefinitely. Check node capacity before raising requests substantially.

## Related documentation

- [Resource-Based Control Plane Autoscaling](resource-based-control-plane-autoscaling.md)
- [Distribute HostedCluster workloads](distribute-hosted-cluster-workloads.md)
- [Configure the Kube API server](configure-ocp-components/kubeapi-server.md)
- [Cluster autoscaling (data plane)](autoscaling.md)
