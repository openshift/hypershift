# Autoscaling

This guide covers configuring node pool and cluster autoscaling for HostedClusters. Autoscaling automatically adjusts the number of worker nodes based on workload demands.

## Node Pool Autoscaling

Node pool autoscaling enables individual NodePools to automatically scale between a minimum and maximum number of nodes based on pending pod resource requests.

### Enable Autoscaling on a NodePool

To enable autoscaling, set `spec.autoScaling` and remove `spec.replicas`:

```bash
oc patch nodepool -n <HOSTED_CLUSTER_NAMESPACE> <NODEPOOL_NAME> --type merge -p '{
  "spec": {
    "replicas": null,
    "autoScaling": {
      "min": 1,
      "max": 5
    }
  }
}'
```

| Field | Description |
|-------|-------------|
| `min` | Minimum number of nodes to maintain. Must be >= 0 and <= `max`. See note below about platform restrictions. |
| `max` | Maximum number of nodes the autoscaler can scale to. Must be >= 1 and >= `min`. |

!!! note
    `autoScaling` and `replicas` are mutually exclusive. When enabling autoscaling, `replicas` must be set to `null`.

!!! note
    Scale-from-zero (`min: 0`) is supported on AWS and Azure when native capacity or the corresponding instance-type discovery provider is available. Other platforms require `min` >= 1.

### Labels and Taints When Scaling from Zero

Without a live worker, the autoscaler needs scheduling labels and taints in addition to resource capacity. For AWS and Azure autoscaling NodePools, HyperShift reconciles `spec.nodeLabels` and `spec.taints` into the `capacity.cluster-autoscaler.kubernetes.io/labels` and `capacity.cluster-autoscaler.kubernetes.io/taints` annotations on the MachineDeployment (Replace) or MachineSet (InPlace). Native provider capacity replaces CPU/memory/GPU workaround annotations, not labels or taints; their reconciliation does not require the optional capacity-discovery provider.

**Topology labels are user-configured, not discovered automatically.** If workloads use zone/region affinity, selectors, or volume topology requirements, supply the corresponding labels on the NodePool before relying on scale-from-zero. For example, for an AWS pool in `eu-central-1b`:

```yaml
spec:
  nodeLabels:
    topology.kubernetes.io/region: eu-central-1
    topology.kubernetes.io/zone: eu-central-1b
    topology.ebs.csi.aws.com/zone: eu-central-1b
    topology.k8s.aws/zone-id: euc1-az3
    failure-domain.beta.kubernetes.io/region: eu-central-1
    failure-domain.beta.kubernetes.io/zone: eu-central-1b
```

Use the actual region, zone name, and zone ID for the pool's subnet in the tenant AWS account; these example values are not a universal zone-name/zone-ID mapping. Include the label keys required by your workloads, along with any custom labels. Label propagation does not validate placement against AWS. Incorrect values can misrepresent the pool to the autoscaler's simulation and result in unsuitable scale-up decisions or workloads remaining pending.

Verify the generated annotation before testing scale-up:

```bash
oc -n <CONTROL_PLANE_NAMESPACE> get machinedeployment <NODEPOOL_NAME> \
  -o go-template='{{index .metadata.annotations "capacity.cluster-autoscaler.kubernetes.io/labels"}}{{"\n"}}'
```

For InPlace pools, use `machineset` instead. Configuring only these labels or taints does not change the NodePool rollout hash or replace workers. Validate actual node labels and workload scheduling on a disposable test pool; this configuration is not a guarantee of successful scheduling.

### Verify Autoscaling is Enabled

Check the `AutoscalingEnabled` condition on the NodePool:

```bash
oc get nodepool -n <HOSTED_CLUSTER_NAMESPACE> <NODEPOOL_NAME> -o jsonpath='{.status.conditions[?(@.type=="AutoscalingEnabled")].status}'
```

The output should be `True`.

### Disable Autoscaling

To disable autoscaling and return to a fixed replica count:

```bash
oc patch nodepool -n <HOSTED_CLUSTER_NAMESPACE> <NODEPOOL_NAME> --type merge -p '{
  "spec": {
    "autoScaling": null,
    "replicas": 2
  }
}'
```

## Cluster Autoscaling

Cluster autoscaling configures global autoscaling behavior that applies to all NodePools in a HostedCluster. This includes scale-down policies, node group balancing, and expander strategies.

### Configure Cluster Autoscaling

Set `spec.autoscaling` on the HostedCluster:

```bash
oc patch hostedcluster -n <HOSTED_CLUSTER_NAMESPACE> <HOSTED_CLUSTER_NAME> --type merge -p '{
  "spec": {
    "autoscaling": {
      "scaling": "ScaleUpAndScaleDown",
      "maxNodesTotal": 10,
      "expanders": ["LeastWaste"],
      "scaleDown": {
        "delayAfterAddSeconds": 300,
        "unneededDurationSeconds": 600,
        "utilizationThresholdPercent": 50
      }
    }
  }
}'
```

### Configuration Reference

#### Scaling Behavior

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `scaling` | string | `ScaleUpAndScaleDown` | `ScaleUpOnly` or `ScaleUpAndScaleDown`. Controls whether the autoscaler can scale down nodes. |
| `maxNodesTotal` | int | unlimited | Maximum total nodes across all NodePools. The autoscaler will not scale beyond this limit. |
| `maxPodGracePeriod` | int | 600 | Maximum seconds to wait for graceful pod termination before scaling down. |
| `maxNodeProvisionTime` | string | 15m | Maximum time to wait for a node to provision, in Go duration format (e.g., `15m`, `20m`). |
| `podPriorityThreshold` | int | -10 | Pods with priority below this threshold won't trigger scale-up. |
| `kubeClientQPS` | int | 5 | Maximum queries-per-second the autoscaler may send to the kube-apiserver (`--kube-client-qps`). Must be between -1 and 1000. `-1` disables client-side rate limiting; `0` uses the client-go default QPS of 5; `1000` is the maximum. When omitted, the flag is not set. |
| `kubeClientBurst` | int | 10 | Maximum burst of queries to the kube-apiserver (`--kube-client-burst`). Must be between 1 and 2000. `1` is the minimum; `2000` is the maximum. When omitted, the flag is not set. |

!!! note
    Defaults listed in the configuration reference tables represent the cluster autoscaler's effective behavior when the field is omitted. The only API-enforced default is `scaling`, which defaults to `ScaleUpAndScaleDown`.

#### Expanders

Expanders control how the autoscaler selects which NodePool to scale when multiple NodePools can satisfy pending pods. Set via `spec.autoscaling.expanders`:

| Expander | Description |
|----------|-------------|
| `LeastWaste` | Selects the NodePool with the least idle CPU and memory after scaling. |
| `Priority` | Selects the NodePool with the highest user-defined priority. |
| `Random` | Selects a NodePool randomly. |

Default: `[Priority, LeastWaste]`. Up to 3 expanders can be specified in priority order.

!!! note
    The `Priority` expander uses a ConfigMap named `cluster-autoscaler-priority-expander` in the `kube-system` namespace of the guest cluster to determine NodePool priorities. The ConfigMap maps integer priorities to node group name patterns (regex). Higher values mean higher priority. See the [upstream documentation](https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/expander/priority/readme.md) for details on the ConfigMap format.

#### Scale Down Configuration

The `scaleDown` field is only valid when `scaling` is set to `ScaleUpAndScaleDown`:

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `delayAfterAddSeconds` | int | 600 | Seconds to wait after scale-up before evaluating scale-down. |
| `delayAfterDeleteSeconds` | int | 0 | Seconds to wait after node deletion before evaluating scale-down. |
| `delayAfterFailureSeconds` | int | 180 | Seconds to wait after a failed scale-down before retrying. |
| `unneededDurationSeconds` | int | 600 | How long a node must be unneeded before it is eligible for removal. |
| `utilizationThresholdPercent` | int | 50 | Nodes with utilization below this percentage are candidates for removal. 0 means only completely idle nodes; 100 means any node can be removed. |

#### Node Group Balancing

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `balancingIgnoredLabels` | []string | [] | Labels the autoscaler should ignore when comparing node groups for balancing. Platform-specific labels are added automatically. |
| `maxFreeDifferenceRatioPercent` | int | 10 | Maximum allowed difference in free resources between node groups to be considered similar for balancing. 0 = exact match required; 100 = any difference allowed. |

#### Node Behavior (Tech Preview)

!!! note
    These fields require the management cluster to be installed with the `HostedClusterAutoscalerConfiguration` feature gate, part of the `TechPreviewNoUpgrade` feature set (`hypershift install --tech-preview-no-upgrade`). They are omitted from the Default HostedCluster/HostedControlPlane CRD schemas. Setting `HostedCluster.spec.configuration.featureGate` (the guest cluster's feature gates) does not enable these fields — the gate is a management-cluster-level setting.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `localStorageScaleDown` | string | `Consider` | `Skip` protects nodes running pods with disk-backed local storage (`emptyDir` volumes without `medium: Memory` or `hostPath` volumes). `Consider` allows such nodes to be removed like any other node. Sets `--skip-nodes-with-local-storage`. |
| `similarNodeGroupBalancing` | string | `Balance` | `Balance` or `DoNotBalance`. Controls whether the autoscaler tries to keep NodePools with matching instance type and labels at similar sizes during scale-up. Sets `--balance-similar-node-groups`. |
| `daemonSetUtilization` | string | unset (autoscaler default applies) | `Include` or `Exclude`. Controls whether DaemonSet pod resource requests count toward node utilization for scale-down eligibility. When omitted, no flag is rendered and the autoscaler's own default (include) applies. Sets `--ignore-daemonsets-utilization`. |

`localStorageScaleDown` and `daemonSetUtilization` only affect scale-down behavior: with `scaling: ScaleUpOnly` their values are still stored and take effect automatically once `scaling` is changed back to `ScaleUpAndScaleDown`.

```bash
oc patch hostedcluster -n <HOSTED_CLUSTER_NAMESPACE> <HOSTED_CLUSTER_NAME> --type merge -p '{
  "spec": {
    "autoscaling": {
      "localStorageScaleDown": "Skip",
      "similarNodeGroupBalancing": "DoNotBalance",
      "daemonSetUtilization": "Exclude"
    }
  }
}'
```

#### Resource Limits (Tech Preview)

!!! note
    These fields require the management cluster to be installed with the `HostedClusterAutoscalerConfiguration` feature gate, part of the `TechPreviewNoUpgrade` feature set (`hypershift install --tech-preview-no-upgrade`). They are omitted from the Default HostedCluster/HostedControlPlane CRD schemas. Setting `HostedCluster.spec.configuration.featureGate` (the guest cluster's feature gates) does not enable these fields — the gate is a management-cluster-level setting.

`spec.autoscaling.resourceLimits` bounds the aggregate CPU, memory, and GPU resources across
**all** guest-cluster nodes, read by the autoscaler through its guest kubeconfig. These are
cluster-wide ceilings that apply in addition to, not instead of, `maxNodesTotal` and each
NodePool's own `autoScaling.max`. At least one of `cores`, `memory`, or `gpus` must be set;
an empty `resourceLimits: {}` is rejected.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `cores.min` / `cores.max` | int | unset (effectively unlimited) | Minimum and maximum total CPU cores, in whole cores, across all guest-cluster nodes. `min` may be `0`; `max` must be at least `1`. Sets `--cores-total=<min>:<max>`. |
| `memory.min` / `memory.max` | int | unset (effectively unlimited) | Minimum and maximum total memory, in whole GiB, across all guest-cluster nodes. `min` may be `0`; `max` must be at least `1`. Sets `--memory-total=<min>:<max>`. |
| `gpus` | []object | unset (no GPU limits) | 1–64 unique `{type, min, max}` entries bounding the number of GPU devices of each `type` across all guest-cluster nodes. `min` and `max` may both be `0` (a `max: 0` entry blocks scale-up of that type). `type` must match the `cluster-api/accelerator` label on the GPU-capable NodePool's guest Nodes, and cannot be `cpu` or `memory` (reserved). Sets one `--gpu-total=<type>:<min>:<max>` per entry. |

A configured minimum only constrains scale-down (it prevents aggregate capacity from dropping
below the floor) and never triggers scale-up on its own. A configured maximum only constrains
scale-up. The core or memory budget is shared across all NodePools — it is not a separate
allowance granted to each NodePool.

!!! note
    When any `resourceLimits` minimum (`cores.min`, `memory.min`, or a `gpus[].min`) is greater
    than `0`, `scaleDown.delayAfterDeleteSeconds` must be explicitly set to at least `60`
    seconds. With a shorter delay (or omitted, which defaults to `0`), the autoscaler can resume
    scale-down evaluation before a prior node deletion is fully accounted for by Cluster API,
    letting it delete a second node whose removal would drop aggregate capacity below the
    configured floor. This is enforced at admission time. The `60`-second floor is a value
    confirmed sufficient by live testing, not a provable bound on Cluster API's node-deletion
    convergence time: a sufficiently slow or degraded cloud-provider deletion could still exceed
    it. Closing this race fully requires the autoscaler itself to account for in-flight
    deletions, which is tracked as future work and not delivered by this feature.

!!! warning
    A GPU limit depends on the GPU-capable NodePool's guest Nodes carrying the
    `cluster-api/accelerator` label matching the configured `type`, which HyperShift does not
    yet propagate automatically through the scale-from-zero path. Until that propagation lands,
    verify the label is present on real guest Nodes before relying on a `gpus` limit for
    enforcement.

```bash
oc patch hostedcluster -n <HOSTED_CLUSTER_NAMESPACE> <HOSTED_CLUSTER_NAME> --type merge -p '{
  "spec": {
    "autoscaling": {
      "resourceLimits": {
        "cores": {"min": 0, "max": 128},
        "memory": {"min": 0, "max": 512},
        "gpus": [
          {"type": "nvidia-tesla-t4", "min": 0, "max": 8}
        ]
      }
    }
  }
}'
```

#### Cordoning (Tech Preview)

!!! note
    This field requires the management cluster to be installed with the `HostedClusterAutoscalerConfiguration` feature gate, part of the `TechPreviewNoUpgrade` feature set (`hypershift install --tech-preview-no-upgrade`). It is omitted from the Default HostedCluster/HostedControlPlane CRD schemas. Setting `HostedCluster.spec.configuration.featureGate` (the guest cluster's feature gates) does not enable this field — the gate is a management-cluster-level setting.

`spec.autoscaling.scaleDown.cordonNodeBeforeTerminating` is nested under `scaleDown`, so it is
only valid when `scaling` is `ScaleUpAndScaleDown`.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `cordonNodeBeforeTerminating` | string | unset (HyperShift renders `false`) | `Enabled` or `Disabled`. Controls whether the autoscaler cordons a guest-cluster node (marks it unschedulable) before draining and terminating it during scale-down. When omitted, HyperShift explicitly renders `--cordon-node-before-terminating=false` rather than deferring to the autoscaler image's own default. Sets `--cordon-node-before-terminating`. |

The cordon is applied to the guest-cluster Node object through the autoscaler's guest kubeconfig.
The backing management-cluster Machine is deleted separately through Cluster API, after the node
has drained; cordoning does not change that deletion ordering, only whether the node is marked
unschedulable beforehand.

```bash
oc patch hostedcluster -n <HOSTED_CLUSTER_NAMESPACE> <HOSTED_CLUSTER_NAME> --type merge -p '{
  "spec": {
    "autoscaling": {
      "scaling": "ScaleUpAndScaleDown",
      "scaleDown": {
        "cordonNodeBeforeTerminating": "Enabled"
      }
    }
  }
}'
```

#### New Pod Scale-Up Delay (Tech Preview)

!!! note
    This field requires the management cluster to be installed with the `HostedClusterAutoscalerConfiguration` feature gate, part of the `TechPreviewNoUpgrade` feature set (`hypershift install --tech-preview-no-upgrade`). It is omitted from the Default HostedCluster/HostedControlPlane CRD schemas. Setting `HostedCluster.spec.configuration.featureGate` (the guest cluster's feature gates) does not enable this field — the gate is a management-cluster-level setting.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `scaleUp.newPodScaleUpDelay` | string (Go duration) | unset (autoscaler default `0s` applies) | Delays scale-up evaluation for pods younger than this duration, measured from the pod's creation timestamp. Must be between `0s` and `1h`. Sets `--new-pod-scale-up-delay`. |

`newPodScaleUpDelay` is useful for short-lived, bursty, or batch workloads that should not
trigger scale-up before they have a chance to schedule on existing capacity. The delay is
measured from the pod's `metadata.creationTimestamp`, not from when it first becomes
unschedulable: a pod already older than the effective delay can be considered for scale-up on the
next autoscaler scan. The per-pod
`cluster-autoscaler.kubernetes.io/pod-scale-up-delay` annotation can only extend this delay for an
individual pod; if the annotation specifies a shorter delay than the global setting, the
autoscaler ignores the shorter value and keeps the longer, global delay.

```bash
oc patch hostedcluster -n <HOSTED_CLUSTER_NAMESPACE> <HOSTED_CLUSTER_NAME> --type merge -p '{
  "spec": {
    "autoscaling": {
      "scaleUp": {
        "newPodScaleUpDelay": "30s"
      }
    }
  }
}'
```

#### Log Verbosity (Tech Preview)

!!! note
    This field requires the management cluster to be installed with the `HostedClusterAutoscalerConfiguration` feature gate, part of the `TechPreviewNoUpgrade` feature set (`hypershift install --tech-preview-no-upgrade`). It is omitted from the Default HostedCluster/HostedControlPlane CRD schemas. Setting `HostedCluster.spec.configuration.featureGate` (the guest cluster's feature gates) does not enable this field — the gate is a management-cluster-level setting.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `logVerbosity` | int | `4` | Integer from `0` through `10`. Sets the hosted cluster autoscaler's klog verbosity level. Sets `--v`. |

`logVerbosity` is a top-level `autoscaling` field, independent of `scaling`, `scaleDown`, and
`scaleUp`: it changes only the management-cluster autoscaler container's own log output, not
scaling policy, the control plane operator's logs, or guest control-plane logs. When omitted,
this defaults to `4`, preserving HyperShift's current behavior; an explicit `0` is distinct from
omission and is honored as a valid, lower-verbosity choice. Higher values increase log volume and
can add processing overhead — remove the field to restore the default `4` and normal log volume
after a diagnostic session.

```bash
oc patch hostedcluster -n <HOSTED_CLUSTER_NAMESPACE> <HOSTED_CLUSTER_NAME> --type merge -p '{
  "spec": {
    "autoscaling": {
      "logVerbosity": 6
    }
  }
}'
```

## How It Works

1. When a pod cannot be scheduled due to insufficient resources, the cluster autoscaler identifies NodePools with autoscaling enabled that can satisfy the pod's requirements.
2. The autoscaler selects a NodePool based on the configured expander strategy and triggers a scale-up by increasing the NodePool's replica count.
3. HyperShift provisions new platform-specific machine instances for the NodePool and the new nodes join the guest cluster.
4. When `scaling` is set to `ScaleUpAndScaleDown`, the autoscaler monitors node utilization. Nodes that remain underutilized (below `utilizationThresholdPercent`) for longer than `unneededDurationSeconds` are removed.

## Monitoring

Check the current state of autoscaling:

```bash
# View NodePool autoscaling status
oc get nodepools -n <HOSTED_CLUSTER_NAMESPACE> -o wide

# Check autoscaler deployment in the control plane namespace
oc get deployment cluster-autoscaler -n <HOSTED_CLUSTER_NAMESPACE>-<HOSTED_CLUSTER_NAME>

# View autoscaler logs
oc logs deployment/cluster-autoscaler -n <HOSTED_CLUSTER_NAMESPACE>-<HOSTED_CLUSTER_NAME>
```
