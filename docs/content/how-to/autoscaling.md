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
    Scale-from-zero (`min: 0`) is supported on AWS and Azure when the infrastructure provider supplies native capacity or the HyperShift Operator has the corresponding instance-type discovery provider configured. Other platforms require `min` >= 1.

### Scheduling Metadata When Scaling from Zero

Without a live node, the cluster autoscaler needs both resource capacity and scheduling metadata to determine whether a NodePool can run a pending pod. Native infrastructure-provider capacity replaces the CPU, memory, and GPU workaround annotations; it does not replace labels and taints.

For AWS and Azure autoscaling NodePools, the NodePool controller continuously reconciles `spec.nodeLabels`, architecture, and `spec.taints` into the `capacity.cluster-autoscaler.kubernetes.io/labels` and `capacity.cluster-autoscaler.kubernetes.io/taints` annotations on the MachineDeployment (Replace) or MachineSet (InPlace). Scheduling metadata does not require the optional instance-type discovery provider.

For AWS, the Control Plane Operator discovers the worker subnet's availability zone name and ID using the hosted cluster's AWS credentials and region. It records region, zone, and zone ID in the top-level `hypershift.openshift.io/aws-subnet-topology` annotation on the AWSMachineTemplate. The NodePool controller uses that result to advertise all of these topology labels:

- `failure-domain.beta.kubernetes.io/region`
- `failure-domain.beta.kubernetes.io/zone`
- `topology.ebs.csi.aws.com/zone`
- `topology.k8s.aws/zone-id`
- `topology.kubernetes.io/region`
- `topology.kubernetes.io/zone`

It also advertises `node.kubernetes.io/instance-type`. Discovered values take precedence over conflicting custom labels. These annotations describe the labels that AWS cloud controllers apply to live nodes; they do not change worker configuration. Arbitrary `spec.nodeLabels` are included, but labels added by other controllers cannot be inferred unless their values are known before a worker exists.

The lookup uses the NodePool's subnet ID or filters, not the control plane's zone. A subnet reference must resolve unambiguously; lookup failures are retried and logged rather than replaced with a guessed zone. The CPO role needs `ec2:DescribeSubnets`, which the generated HyperShift CPO role policies already grant. Custom roles must grant this permission as well.

Automatic zone discovery requires both the updated HyperShift Operator and an updated CPO from the hosted cluster's release payload. Updating the HyperShift Operator alone restores label/taint reconciliation but cannot discover zones for an older CPO. Existing pools receive annotation updates without a worker rollout.

#### Verify AWS Zone-Affine Scale-Up

Use a disposable AWS NodePool with `min: 0`, `max: 1`, and a unique `spec.nodeLabels` entry such as `scale-from-zero-test: "true"`. Do not scale a production pool to zero for this check. The test provisions a worker and incurs AWS charges.

1. Before creating a workload, verify that the pool has zero workers and that its MachineDeployment or MachineSet has all six topology labels and the correct instance type in the capacity labels annotation:

    ```bash
    oc --context <MANAGEMENT_CONTEXT> -n <CONTROL_PLANE_NAMESPACE> get machinedeployment <NODEPOOL_NAME> \
      -o go-template='{{index .metadata.annotations "capacity.cluster-autoscaler.kubernetes.io/labels"}}{{"\n"}}'
    ```

    For InPlace pools, replace `machinedeployment` with `machineset`. Confirm the zone against the worker subnet using credentials for the hosted cluster's AWS account, not the management account:

    ```bash
    aws ec2 describe-subnets --region <AWS_REGION> --subnet-ids <WORKER_SUBNET_ID> \
      --query 'Subnets[0].[AvailabilityZone,AvailabilityZoneId]' --output text
    ```

2. In the hosted cluster, create a pod restricted to that pool and zone, replacing `<WORKER_ZONE>` with the discovered zone:

    ```yaml
    apiVersion: v1
    kind: Pod
    metadata:
      name: scale-from-zero-zone-check
    spec:
      nodeSelector:
        scale-from-zero-test: "true"
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
            - matchExpressions:
              - key: topology.kubernetes.io/zone
                operator: In
                values: ["<WORKER_ZONE>"]
      containers:
      - name: check
        image: registry.access.redhat.com/ubi9/ubi-minimal:latest
        command: ["sleep", "3600"]
        resources:
          requests:
            cpu: 100m
            memory: 64Mi
    ```

    Add matching tolerations if the test NodePool has taints.

3. Verify the autoscaler selects the zero-replica pool, provisions one worker, and schedules the pod. Check that the worker's actual zone labels match the annotations. Verify that a pod requiring a different zone is not assigned to this pool.

4. Delete the test pod in the hosted cluster and clean up the disposable NodePool according to your test environment's procedures.

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
