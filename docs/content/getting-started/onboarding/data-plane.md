# Data Plane and Node Management

> **See also**: [NodePool Rollouts](../../reference/nodepool-rollouts.md) for in-depth rollout mechanics and update strategies.

## NodePool - Key Fields

| Field | Purpose | Look at |
|-------|---------|---------|
| `spec.clusterName` | Immutable reference to the HostedCluster | `api/hypershift/v1beta1/nodepool_types.go` |
| `spec.release` | Release image (change triggers rollout, tagged `+rollout`) | Same file |
| `spec.platform` | Platform-specific machine config (AMI, instance type, etc.) | `aws.go`, `azure.go`, `kubevirt.go` in the api dir |
| `spec.replicas` / `spec.autoScaling` | Node count control | Same file |
| `spec.management.upgradeType` | `Replace` (default) or `InPlace` | Same file |
| `spec.management.autoRepair` | Enables MachineHealthCheck | Same file |
| `spec.config` | ConfigMap refs with MachineConfig (change triggers rollout) | Same file |

## Node Lifecycle

```mermaid
sequenceDiagram
    participant NPC as NodePool Controller
    participant CFG as ConfigGenerator
    participant TOK as Token Manager
    participant CAPI as ClusterAPI
    participant Cloud as Cloud Provider
    participant IGN as Ignition Server
    participant Node as Worker Node
    participant GC as Guest Cluster

    NPC->>CFG: Compute config hash
    CFG-->>NPC: hash = hashSimple(mcoConfig + version + ...)

    NPC->>TOK: reconcileTokenSecret(hash)
    TOK->>TOK: Creates token Secret in HCP ns<br/>(token UUID, release, config)
    TOK->>TOK: Creates userdata Secret<br/>(ignition stub with URL + token)

    NPC->>CAPI: Creates/updates MachineDeployment
    Note over CAPI: Spec.Template.Bootstrap.DataSecretName<br/>= userdata secret name

    CAPI->>CAPI: Creates MachineSet
    CAPI->>CAPI: Creates Machine(s)
    CAPI->>Cloud: Provisions instance<br/>(EC2, Azure VM, KubeVirt VM)

    Cloud->>Node: Instance boots with userdata
    Node->>IGN: GET /ignition<br/>Authorization: token UUID
    IGN->>IGN: Looks up payload in cache
    IGN-->>Node: Full ignition JSON

    Node->>Node: Applies ignition config
    Node->>Node: Starts kubelet
    Node->>GC: Joins the guest cluster

    Note over NPC: machine-approver approves node CSR
```

!!! tip "Explore yourself"
    The NodePool controller is split across several files. Read them in this order:

    1. `hypershift-operator/controllers/nodepool/nodepool_controller.go` - Main reconciler entry point, condition checks
    2. `hypershift-operator/controllers/nodepool/config.go` - `ConfigGenerator`: how config hash is computed for rollout detection
    3. `hypershift-operator/controllers/nodepool/token.go` - `Token`: token Secret and userdata Secret lifecycle
    4. `hypershift-operator/controllers/nodepool/capi.go` - `CAPI`: MachineDeployment, MachineSet, MachineHealthCheck, MachineTemplate creation

## ClusterAPI Integration

```mermaid
graph TD
    NP[NodePool] -->|creates| MD[MachineDeployment]
    NP -->|creates| MT[PlatformMachineTemplate<br/><i>e.g., AWSMachineTemplate</i>]
    NP -->|creates| MHC[MachineHealthCheck]

    MD -->|CAPI creates| MS[MachineSet]
    MS -->|CAPI creates| M1[Machine 1]
    MS -->|CAPI creates| M2[Machine 2]
    MS -->|CAPI creates| MN[Machine N]

    M1 -->|infra provider creates| I1[EC2 Instance / Azure VM / KubeVirt VM]

    MT -.->|referenced by| MD
    MHC -.->|monitors| M1
    MHC -.->|monitors| M2

    subgraph "CP Namespace (clusters-my-cluster)"
        MD
        MS
        M1
        M2
        MN
        MT
        MHC
    end
```

**Rollout detection**: `ConfigGenerator.Hash()` produces a new hash when config or version changes. New hash = new Secrets = new `DataSecretName` on MachineDeployment = CAPI rolling update.

!!! tip "Explore yourself"
    Platform-specific machine template builders:

    - `hypershift-operator/controllers/nodepool/aws.go` - `awsMachineTemplateSpec()`: AMI resolution, instance type, root volume, security groups
    - `hypershift-operator/controllers/nodepool/azure.go` - Azure VM config
    - `hypershift-operator/controllers/nodepool/kubevirt/kubevirt.go` - KubeVirt VM config
    - `hypershift-operator/controllers/nodepool/agent.go` - Agent/bare-metal label selectors
    - `hypershift-operator/controllers/nodepool/gcp.go` - GCP machine config
    - `hypershift-operator/controllers/nodepool/openstack.go` - OpenStack config

## Auto-scaling

> **See also**: [Resource-Based Control Plane Autoscaling](../../how-to/resource-based-control-plane-autoscaling.md) for detailed autoscaling configuration.

- **Manual**: `nodePool.spec.replicas` propagates to `MachineDeployment.Spec.Replicas`
- **Cluster Autoscaler**: `nodePool.spec.autoScaling.min/max` becomes CAPI annotations on MachineDeployment
- **Scale-from-zero** (AWS only): capacity annotations (`vCPU`, `memoryMb`, `GPU`) in `hypershift-operator/controllers/nodepool/scale_from_zero.go`
- **Karpenter** (alternative): provisions nodes directly based on pending pods, bypassing MachineDeployments. See `karpenter-operator/controllers/`

!!! tip "Explore yourself"
    Karpenter integration files:

    - `karpenter-operator/controllers/karpenter/karpenter_controller.go` - Main reconciler
    - `karpenter-operator/controllers/karpenterignition/karpenterignition_controller.go` - Ignition for Karpenter nodes
    - `api/karpenter/v1beta1/` - HyperShift Karpenter API types

## Auto-repair

```mermaid
flowchart LR
    MHC[MachineHealthCheck] -->|detects NodeReady=False<br/>for 8-16 min| UNHEALTHY[Machine marked<br/>unhealthy]
    UNHEALTHY -->|MaxUnhealthy=2<br/>prevents cascade| DELETE[Deletes Machine]
    DELETE -->|CAPI| NEW[Creates replacement<br/>Machine]
```

!!! tip "Explore yourself"
    MHC creation is in `hypershift-operator/controllers/nodepool/capi.go`, function `reconcileMachineHealthCheck()` (~line 649). Note the different timeouts for cloud (8 min) vs Agent/None (16 min) platforms.

---
