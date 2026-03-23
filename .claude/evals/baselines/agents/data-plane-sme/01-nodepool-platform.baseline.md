Now I have a comprehensive understanding of the entire spot/preemptible instance landscape in HyperShift. Here is my complete analysis and guidance:

---

# Spot/Preemptible Instance Support in HyperShift NodePools: Complete Analysis & Guidance

## 1. Current State Assessment

### What Exists Today

**AWS (annotation-based, partially complete):**
- Spot enabled via annotation `hypershift.openshift.io/enable-spot` — a boolean presence check with no configuration knobs
- `isSpotEnabled()` in `hypershift-operator/controllers/nodepool/aws.go` checks annotation existence
- When spot is enabled:
  - Tag `aws-node-termination-handler/managed` is added to instances (for NTH)
  - Label `hypershift.openshift.io/interruptible-instance` is added to CAPI MachineDeployment/MachineSet templates
  - A dedicated spot `MachineHealthCheck` is created with 100% `maxUnhealthy` and 8-minute unhealthy timeout
  - AWS Node Termination Handler (NTH) deployed as a CPOv2 component, triggered by SQS queue URL annotation on `HostedCluster`
- **Critical gap**: The `SpotMarketOptions` field on the CAPI `AWSMachineTemplateSpec` is **never set** — CAPI-provider-aws supports it (`SpotMarketOptions{MaxPrice}`) but HyperShift doesn't populate it. The instances are not actually launched as spot; only the monitoring/tagging infrastructure is in place.

**Azure (not implemented):**
- CAPI provider has `SpotVMOptions{MaxPrice, EvictionPolicy}` on `AzureMachineSpec`
- Azure also supports `ScaleSetPriority: "Spot"` for VMSS-based pools
- HyperShift's `azureMachineTemplateSpec()` sets `SpotVMOptions` to `nil`

**Other platforms:**
- KubeVirt, OpenStack, PowerVS, Agent: No spot/preemptible concept

### Key Observation
There is an explicit `TODO` in the codebase at `aws.go:42`:
```go
// TODO: Replace with API based configuration.
```

This is the right time to graduate spot from an annotation to a proper API field.

---

## 2. Proposed API Changes

### 2.1 Platform-Agnostic `MarketType` Field (NodePool-Level)

Add a platform-agnostic field to `NodePoolSpec` that expresses the *intent* — "I want interruptible/preemptible capacity" — separate from platform-specific pricing controls.

```go
// api/hypershift/v1beta1/nodepool_types.go

// InstanceMarketType describes the purchasing model for node instances.
// +kubebuilder:validation:Enum=OnDemand;Spot
type InstanceMarketType string

const (
    // InstanceMarketTypeOnDemand uses standard on-demand instances.
    InstanceMarketTypeOnDemand InstanceMarketType = "OnDemand"

    // InstanceMarketTypeSpot uses spot/preemptible instances that may be
    // interrupted by the cloud provider with short notice.
    InstanceMarketTypeSpot InstanceMarketType = "Spot"
)
```

Add to `NodePoolSpec`:

```go
type NodePoolSpec struct {
    // ...existing fields...

    // marketType specifies the instance purchasing model for this NodePool.
    // "OnDemand" (default) uses standard pricing with guaranteed capacity.
    // "Spot" uses discounted, interruptible capacity that may be reclaimed
    // by the cloud provider with as little as 2 minutes notice.
    //
    // When set to "Spot":
    // - Nodes may be terminated at any time by the cloud provider
    // - A dedicated MachineHealthCheck is created for rapid replacement
    // - Platform-specific interruption handlers are enabled when available
    // - Upgrade behavior is adjusted to account for potential interruptions
    //
    // Not all platforms support spot instances. Setting this to "Spot" on
    // an unsupported platform will result in a validation error condition.
    //
    // Supported platforms: AWS, Azure.
    // +optional
    // +kubebuilder:default:=OnDemand
    // +kubebuilder:validation:Enum=OnDemand;Spot
    // +kubebuilder:validation:XValidation:rule="self == oldSelf", message="MarketType is immutable"
    MarketType InstanceMarketType `json:"marketType,omitempty"`
}
```

**Rationale for immutability**: Changing an existing pool from on-demand to spot (or vice versa) would require replacing every machine. This is better expressed by creating a new NodePool. Immutability prevents accidental mass-replacement and keeps the upgrade semantics clean.

### 2.2 Platform-Specific Spot Configuration

**AWS — extend `AWSNodePoolPlatform`:**

```go
// api/hypershift/v1beta1/aws.go

// AWSSpotMarketOptions configures AWS-specific spot instance behavior.
type AWSSpotMarketOptions struct {
    // maxPrice defines the maximum hourly price you are willing to pay for
    // a Spot Instance. If you specify a max price, your instances will be
    // interrupted more frequently than if you do not specify one.
    // If not specified, the on-demand price is used as the default maximum.
    // Format: decimal string (e.g. "0.05")
    // +optional
    // +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]+)?$`
    MaxPrice *string `json:"maxPrice,omitempty"`
}

type AWSNodePoolPlatform struct {
    // ...existing fields...

    // spotMarketOptions configures AWS-specific spot instance behavior.
    // Only valid when the NodePool marketType is set to "Spot".
    // If marketType is "Spot" and this field is omitted, spot instances
    // are requested with default options (on-demand price cap).
    // +optional
    SpotMarketOptions *AWSSpotMarketOptions `json:"spotMarketOptions,omitempty"`
}
```

**Azure — extend `AzureNodePoolPlatform`:**

```go
// api/hypershift/v1beta1/azure.go

// AzureSpotEvictionPolicy defines the eviction policy for Azure Spot VMs.
// +kubebuilder:validation:Enum=Deallocate;Delete
type AzureSpotEvictionPolicy string

const (
    // AzureSpotEvictionPolicyDeallocate stops the VM but retains the disk.
    AzureSpotEvictionPolicyDeallocate AzureSpotEvictionPolicy = "Deallocate"

    // AzureSpotEvictionPolicyDelete deletes both the VM and its disk.
    AzureSpotEvictionPolicyDelete AzureSpotEvictionPolicy = "Delete"
)

// AzureSpotVMOptions configures Azure-specific spot VM behavior.
type AzureSpotVMOptions struct {
    // maxPrice defines the maximum price you are willing to pay for a
    // Spot VM. -1 means the VM won't be evicted for price reasons.
    // +optional
    MaxPrice *resource.Quantity `json:"maxPrice,omitempty"`

    // evictionPolicy defines what happens when a Spot VM is evicted.
    // "Delete" (default) deletes the VM and disk.
    // "Deallocate" stops the VM but retains the disk.
    // +optional
    // +kubebuilder:default:=Delete
    EvictionPolicy *AzureSpotEvictionPolicy `json:"evictionPolicy,omitempty"`
}

type AzureNodePoolPlatform struct {
    // ...existing fields...

    // spotVMOptions configures Azure-specific spot VM behavior.
    // Only valid when the NodePool marketType is set to "Spot".
    // +optional
    SpotVMOptions *AzureSpotVMOptions `json:"spotVMOptions,omitempty"`
}
```

### 2.3 New Status Condition

```go
// api/hypershift/v1beta1/nodepool_types.go

const (
    // NodePoolSpotInterruptionHandlerReadyConditionType reports whether
    // the platform-specific interruption handler is deployed and operational.
    NodePoolSpotInterruptionHandlerReadyConditionType = "SpotInterruptionHandlerReady"

    // NodePoolValidMarketTypeConditionType reports whether the configured
    // marketType is supported on the current platform.
    NodePoolValidMarketTypeConditionType = "ValidMarketType"
)
```

### 2.4 CEL Validation Rules

Add cross-field validation on `NodePoolSpec`:

```go
// +kubebuilder:validation:XValidation:rule="self.marketType != 'Spot' || self.platform.type in ['AWS', 'Azure']", 
//   message="Spot market type is only supported on AWS and Azure platforms"
// +kubebuilder:validation:XValidation:rule="!has(self.platform.aws) || !has(self.platform.aws.spotMarketOptions) || self.marketType == 'Spot'",
//   message="spotMarketOptions can only be set when marketType is Spot"
// +kubebuilder:validation:XValidation:rule="!has(self.platform.azure) || !has(self.platform.azure.spotVMOptions) || self.marketType == 'Spot'",
//   message="spotVMOptions can only be set when marketType is Spot"
```

### 2.5 Backward Compatibility

During the migration period, update `isSpotEnabled()` to check both:

```go
func isSpotEnabled(nodePool *hyperv1.NodePool) bool {
    // New API takes precedence
    if nodePool.Spec.MarketType == hyperv1.InstanceMarketTypeSpot {
        return true
    }
    // Legacy annotation support (deprecated)
    if nodePool.Annotations == nil {
        return false
    }
    _, ok := nodePool.Annotations[AnnotationEnableSpot]
    return ok
}
```

Emit a deprecation event/condition when the annotation is detected, guiding users to migrate to `marketType: Spot`.

---

## 3. Controller Changes

### 3.1 AWS Machine Template — Actually Set SpotMarketOptions

The most critical fix: **currently `awsMachineTemplateSpec()` never populates `SpotMarketOptions`**. Fix this:

```go
// hypershift-operator/controllers/nodepool/aws.go

func awsMachineTemplateSpec(...) (*capiaws.AWSMachineTemplateSpec, error) {
    // ...existing code...

    spec := &capiaws.AWSMachineTemplateSpec{
        Template: capiaws.AWSMachineTemplateResource{
            Spec: capiaws.AWSMachineSpec{
                // ...existing fields...
            },
        },
    }

    // Configure spot instances
    if isSpotEnabled(nodePool) {
        spotOpts := &capiaws.SpotMarketOptions{}
        if nodePool.Spec.Platform.AWS != nil &&
           nodePool.Spec.Platform.AWS.SpotMarketOptions != nil &&
           nodePool.Spec.Platform.AWS.SpotMarketOptions.MaxPrice != nil {
            spotOpts.MaxPrice = nodePool.Spec.Platform.AWS.SpotMarketOptions.MaxPrice
        }
        spec.Template.Spec.SpotMarketOptions = spotOpts
    }

    return spec, nil
}
```

**This is the single most important change.** Without it, spot instances are never actually requested from AWS even with the annotation present. The current implementation only sets up the *monitoring* infrastructure (NTH, MHC) but the actual instances are still launched as on-demand.

### 3.2 Azure Machine Template — Set SpotVMOptions

```go
// hypershift-operator/controllers/nodepool/azure.go

func azureMachineTemplateSpec(...) (*capiazure.AzureMachineTemplateSpec, error) {
    // ...existing code...

    if isSpotEnabled(nodePool) {
        spotOpts := &capiazure.SpotVMOptions{}
        if nodePool.Spec.Platform.Azure != nil &&
           nodePool.Spec.Platform.Azure.SpotVMOptions != nil {
            if nodePool.Spec.Platform.Azure.SpotVMOptions.MaxPrice != nil {
                spotOpts.MaxPrice = nodePool.Spec.Platform.Azure.SpotVMOptions.MaxPrice
            }
            if nodePool.Spec.Platform.Azure.SpotVMOptions.EvictionPolicy != nil {
                spotOpts.EvictionPolicy = (*capiazure.SpotEvictionPolicy)(
                    nodePool.Spec.Platform.Azure.SpotVMOptions.EvictionPolicy,
                )
            }
        }
        spec.Template.Spec.SpotVMOptions = spotOpts
    }

    return spec, nil
}
```

### 3.3 Platform Validation in Reconciler

Add a condition signal in the NodePool reconciler to validate the `marketType` against the platform:

```go
// hypershift-operator/controllers/nodepool/nodepool_controller.go

func (r *NodePoolReconciler) validateMarketType(nodePool *hyperv1.NodePool) {
    if nodePool.Spec.MarketType != hyperv1.InstanceMarketTypeSpot {
        // Not spot, always valid
        meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
            Type:   string(hyperv1.NodePoolValidMarketTypeConditionType),
            Status: metav1.ConditionTrue,
            Reason: "OnDemand",
        })
        return
    }

    switch nodePool.Spec.Platform.Type {
    case hyperv1.AWSPlatform, hyperv1.AzurePlatform:
        meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
            Type:    string(hyperv1.NodePoolValidMarketTypeConditionType),
            Status:  metav1.ConditionTrue,
            Reason:  "SpotSupported",
            Message: fmt.Sprintf("Spot instances are supported on %s", nodePool.Spec.Platform.Type),
        })
    default:
        meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
            Type:    string(hyperv1.NodePoolValidMarketTypeConditionType),
            Status:  metav1.ConditionFalse,
            Reason:  "SpotNotSupported",
            Message: fmt.Sprintf("Spot instances are not supported on platform %s", nodePool.Spec.Platform.Type),
        })
    }
}
```

### 3.4 Generalize `interruptibleInstanceLabel` Across Platforms

The existing `interruptibleInstanceLabel = "hypershift.openshift.io/interruptible-instance"` is already platform-agnostic. Ensure it's applied in the common CAPI path (already done in `capi.go`), and extend the spot MHC to be created for any platform with `marketType: Spot`:

```go
// hypershift-operator/controllers/nodepool/capi.go

// In the Reconcile() method, change the spot MHC guard from:
//   if isSpotEnabled(nodePool) { ... create spot MHC ... }
// to use the generalized check (isSpotEnabled already updated above)
// No change needed if isSpotEnabled is the single source of truth.
```

---

## 4. Instance Interruption Handling

### 4.1 Interruption Flow Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     Management Cluster                          │
│                                                                 │
│  ┌──────────────┐   ┌───────────────────┐   ┌────────────────┐ │
│  │   NodePool    │──▶│  CAPI Machine     │──▶│  Cloud Provider│ │
│  │  Controller   │   │  Deployment/Set   │   │  (AWS/Azure)   │ │
│  └──────────────┘   └───────────────────┘   └────────┬───────┘ │
│         │                                            │         │
│         ▼                                            │         │
│  ┌──────────────┐   ┌───────────────────┐            │         │
│  │  Spot MHC    │──▶│  Machine          │◀───────────┘         │
│  │  (100% max   │   │  (interruptible   │                      │
│  │   unhealthy) │   │   label)          │                      │
│  └──────────────┘   └───────────────────┘                      │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │              Hosted Control Plane Namespace               │   │
│  │                                                          │   │
│  │  ┌─────────────────────────────────────────────────────┐ │   │
│  │  │ Interruption Handler (platform-specific)            │ │   │
│  │  │                                                     │ │   │
│  │  │  AWS: NTH (SQS ◀── EventBridge ◀── EC2 events)    │ │   │
│  │  │  Azure: Scheduled Events API polling               │ │   │
│  │  │                                                     │ │   │
│  │  │  Actions on interruption notice:                    │ │   │
│  │  │  1. Taint node (NoSchedule/NoExecute)              │ │   │
│  │  │  2. Cordon node                                    │ │   │
│  │  │  3. Drain pods (respecting PDBs)                   │ │   │
│  │  │  4. CAPI detects NotReady → MHC replaces machine   │ │   │
│  │  └─────────────────────────────────────────────────────┘ │   │
│  └──────────────────────────────────────────────────────────┘   │
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │              Hosted Cluster (Guest)                       │   │
│  │                                                          │   │
│  │  Worker Node (spot)                                      │   │
│  │  ├── Tainted: NoSchedule (rebalance-recommendation)     │   │
│  │  ├── Cordoned on interruption notice                    │   │
│  │  └── Pods drained before termination                    │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### 4.2 AWS Interruption Pipeline (Existing, needs wiring)

The NTH component already exists in `control-plane-operator/controllers/hostedcontrolplane/v2/awsnodeterminationhandler/`. It:
1. Watches an SQS queue (specified via `HostedCluster` annotation)
2. Receives EC2 spot interruption warnings and rebalance recommendations from EventBridge
3. Applies taints (`aws-node-termination-handler/rebalance-recommendation`) to affected nodes
4. Cordons and drains the node

**What needs to change**: Currently NTH requires a manually-created SQS queue and manual annotation on `HostedCluster`. For production readiness:

- **Auto-provision SQS queue**: The `hypershift-operator` should create the SQS queue, EventBridge rule, and IAM permissions when any NodePool in the cluster has `marketType: Spot`. Store the queue URL in the `HostedCluster` status.
- **Lifecycle management**: Delete SQS queue and EventBridge rules when no NodePools with `marketType: Spot` remain.

### 4.3 Azure Interruption Handler (New)

Azure Spot VMs receive eviction notices via the [Azure Scheduled Events API](https://learn.microsoft.com/en-us/azure/virtual-machines/linux/scheduled-events) — a metadata endpoint polled from within the VM. Azure CAPI provider already sets `Interruptible: true` on the Machine status when spot is detected, which allows CAPI's built-in machine health checks to handle replacement.

For HyperShift:
1. Deploy a `DaemonSet` in the guest cluster (or a per-node sidecar via MachineConfig) that polls `http://169.254.169.254/metadata/scheduledevents`
2. On `Preempt` event: taint + cordon + drain the node
3. CAPI MHC detects `NotReady` → replaces the machine

Alternatively, leverage CAPI's native `Interruptible` status field — CAPI-provider-azure already sets this. Configure the spot MHC to respond to the Machine going `NotReady` (which it already does with the 8-minute timeout).

**Recommendation**: Start with the CAPI-native approach (rely on MHC + `Interruptible` status) and add the in-VM DaemonSet as a follow-up for faster drain response. This keeps the data plane slim.

---

## 5. Data Plane Upgrade Flow for Spot Instances

### 5.1 Problem Statement

Spot instances introduce unique upgrade challenges:

| Challenge | Impact on Upgrades |
|---|---|
| **Random interruptions** | A node mid-upgrade could be terminated, wasting upgrade work |
| **Capacity unavailable** | New spot instances may not be available in the requested AZ/type |
| **Higher churn rate** | More machine replacements ⇒ more ignition fetches, more bootstrap events |
| **Surge capacity** | Spot surge nodes may also be interrupted during rolling update |

### 5.2 Replace Strategy Adjustments

For `UpgradeType: Replace` (the default and most common):

**A) Increase `maxSurge` default for spot pools:**

The default `maxSurge: 1, maxUnavailable: 0` for on-demand is too conservative for spot. A single surge node could be interrupted, stalling the entire upgrade.

```go
// hypershift-operator/controllers/nodepool/capi.go
// In reconcileMachineDeployment():

if isSpotEnabled(nodePool) {
    strategy := nodePool.Spec.Management.Replace
    if strategy != nil && strategy.RollingUpdate != nil {
        // User-specified values, respect them
        machineDeployment.Spec.Strategy.RollingUpdate = &capiv1.MachineRollingUpdateDeployment{
            MaxUnavailable: strategy.RollingUpdate.MaxUnavailable,
            MaxSurge:       strategy.RollingUpdate.MaxSurge,
        }
    } else {
        // Spot-optimized defaults: allow more surge and unavailability
        // to handle interruptions during upgrades
        machineDeployment.Spec.Strategy.RollingUpdate = &capiv1.MachineRollingUpdateDeployment{
            MaxSurge:       ptr.To(intstr.FromInt(2)),
            MaxUnavailable: ptr.To(intstr.FromInt(1)),
        }
    }
}
```

**Rationale**: With `maxSurge: 2`, if one surge node gets interrupted during upgrade, the other can still make progress. With `maxUnavailable: 1`, the upgrade can continue even if one old node is interrupted.

**B) Condition to warn about upgrade progress with spot:**

```go
const (
    NodePoolSpotUpgradeProgressConditionType = "SpotUpgradeProgress"
)

// Set during upgrades on spot pools to provide visibility:
meta.SetStatusCondition(&nodePool.Status.Conditions, metav1.Condition{
    Type:    NodePoolSpotUpgradeProgressConditionType,
    Status:  metav1.ConditionTrue,
    Reason:  "UpgradeInProgress",
    Message: "Upgrade may take longer than expected due to spot instance interruptions",
})
```

### 5.3 InPlace Strategy Adjustments

For `UpgradeType: InPlace`:

InPlace upgrades are actually **better suited** for spot instances in some ways — they don't need surge capacity. However, there's a risk: a node being upgraded in-place gets interrupted, and the replacement node starts fresh (no in-place state), requiring a full bootstrap.

**Adjustments needed in the in-place upgrader** (`control-plane-operator/hostedclusterconfigoperator/controllers/inplaceupgrader/`):

```go
// In reconcileInPlaceUpgrade():

// Before selecting nodes to upgrade, filter out nodes that are
// recently created (< 5 minutes old) — they may be spot replacements
// that haven't stabilized yet.
func isNodeStable(node *corev1.Node) bool {
    if node.CreationTimestamp.IsZero() {
        return false
    }
    return time.Since(node.CreationTimestamp.Time) > 5*time.Minute
}

func getNodesToUpgrade(nodes []*corev1.Node, targetConfig string, maxUnavailable int) []*corev1.Node {
    // Filter to stable nodes only for spot pools
    var stableNodes []*corev1.Node
    for _, n := range nodes {
        if isNodeStable(n) {
            stableNodes = append(stableNodes, n)
        }
    }
    capacity := getCapacity(stableNodes, targetConfig, maxUnavailable)
    availableCandidates := getAvailableCandidates(stableNodes, targetConfig, capacity)
    alreadyUnavailableNodes := getAlreadyUnavailableCandidates(nodes, targetConfig)
    return append(availableCandidates, alreadyUnavailableNodes...)
}
```

**Additionally**: When a spot node is replaced during an in-place upgrade, the new node arrives at the *old* config version (from the ignition/userData). The in-place upgrader will detect this (`currentConfig != targetConfig`) and schedule it for upgrade in the next reconcile cycle. No special handling is needed — the existing reconcile loop handles this correctly.

### 5.4 NodeDrainTimeout Considerations

For spot instances, the default drain timeout should be shorter than the interruption notice period:

| Cloud | Interruption Notice | Recommended NodeDrainTimeout |
|---|---|---|
| AWS | 2 minutes | 90 seconds |
| Azure | 30 seconds | 20 seconds |

Add guidance in the API docs and consider defaulting:

```go
// In validateManagement or reconcile:
if isSpotEnabled(nodePool) && nodePool.Spec.NodeDrainTimeout == nil {
    switch nodePool.Spec.Platform.Type {
    case hyperv1.AWSPlatform:
        // AWS gives 2 minutes warning; drain must complete before that
        nodePool.Spec.NodeDrainTimeout = &metav1.Duration{Duration: 90 * time.Second}
    case hyperv1.AzurePlatform:
        nodePool.Spec.NodeDrainTimeout = &metav1.Duration{Duration: 20 * time.Second}
    }
}
```

> **Note**: Don't mutate the spec directly. Instead, propagate these as effective defaults to the Machine without changing the user's spec. Set them when building the Machine template if the user hasn't explicitly set `nodeDrainTimeout`.

---

## 6. Spot MachineHealthCheck Improvements

The current spot MHC has the right idea but needs refinements:

```go
func (c *CAPI) reconcileSpotMachineHealthCheck(ctx context.Context, mhc *capiv1.MachineHealthCheck) error {
    // 100% maxUnhealthy is correct — spot can lose all nodes at once
    // (e.g., AZ-wide capacity reclamation)
    maxUnhealthy := intstr.FromString("100%")
    
    // Shorter timeout for spot: 5 minutes (was 8 minutes)
    // Spot nodes that go NotReady are almost certainly terminated, not
    // temporarily unhealthy. Faster detection = faster replacement.
    timeOut := 5 * time.Minute
    
    // Spot instances may take longer to acquire replacement capacity.
    // Keep startup timeout generous.
    nodeStartupTimeout := 20 * time.Minute

    mhc.Spec = capiv1.MachineHealthCheckSpec{
        ClusterName: c.capiClusterName,
        Selector: metav1.LabelSelector{
            MatchLabels: map[string]string{
                interruptibleInstanceLabel: "",
            },
        },
        UnhealthyConditions: []capiv1.UnhealthyCondition{
            {
                Type:    corev1.NodeReady,
                Status:  corev1.ConditionFalse,
                Timeout: metav1.Duration{Duration: timeOut},
            },
            {
                Type:    corev1.NodeReady,
                Status:  corev1.ConditionUnknown,
                Timeout: metav1.Duration{Duration: timeOut},
            },
        },
        MaxUnhealthy: &maxUnhealthy,
        NodeStartupTimeout: &metav1.Duration{Duration: nodeStartupTimeout},
    }

    return nil
}
```

**Key**: The regular MHC uses `maxUnhealthy: 2`, which would block remediation if more than 2 nodes are interrupted simultaneously. The spot MHC correctly uses `100%` to allow remediation of all nodes.

---

## 7. CAPI Integration Details

### 7.1 CAPI's Native Interruptible Support

CAPI providers already support `Interruptible` in machine status:

```go
// vendor/sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2/awsmachine_types.go
type AWSMachineStatus struct {
    Interruptible bool `json:"interruptible,omitempty"`
}
```

CAPI uses this status field to:
1. Mark the Machine as potentially reclaimable
2. Allow MachineHealthChecks to use `Interruptible` as a signal

HyperShift should leverage this rather than building parallel systems:
- The `interruptibleInstanceLabel` on the MachineDeployment template is the correct HyperShift-side mechanism
- CAPI provider sets `Interruptible: true` in Machine status based on `SpotMarketOptions` being set
- The spot MHC selects on the label → fast remediation

### 7.2 Machine Template Hash and Spot

When `SpotMarketOptions` is added to the template spec, the template hash changes, triggering a rolling update. This is **correct and desired** — transitioning from on-demand to spot requires new instances. Since `marketType` is immutable, this only happens on initial NodePool creation.

---

## 8. Recommendations Summary

| # | Recommendation | Priority | Rationale |
|---|---|---|---|
| 1 | **Add `marketType` field to NodePoolSpec** | P0 | Graduate from annotation to proper API. Platform-agnostic intent. |
| 2 | **Actually set `SpotMarketOptions` on AWS template** | P0 | Current code never requests spot instances from AWS. This is a bug. |
| 3 | **Add `AWSSpotMarketOptions` to AWSNodePoolPlatform** | P1 | Allow `maxPrice` configuration per cloud best practices. |
| 4 | **Add `AzureSpotVMOptions` to AzureNodePoolPlatform** | P1 | Enable Azure spot with eviction policy control. |
| 5 | **Set `SpotVMOptions` on Azure template when spot** | P1 | Wire Azure spot to CAPI provider. |
| 6 | **Adjust default maxSurge/maxUnavailable for spot** | P1 | Prevent upgrade stalls from interrupted surge nodes. |
| 7 | **Add `ValidMarketType` condition** | P1 | Surface validation errors for unsupported platform + spot combos. |
| 8 | **Propagate shorter drain timeout for spot** | P2 | Must drain before cloud reclaims instance. |
| 9 | **Auto-provision NTH SQS queue** | P2 | Remove manual setup requirement for AWS spot. |
| 10 | **Add stability check in in-place upgrader** | P2 | Avoid upgrading newly-replaced spot nodes that haven't stabilized. |
| 11 | **Keep annotation backward compat** | P1 | Don't break existing E2E tests and early adopters. |
| 12 | **Deprecation warning for annotation** | P2 | Guide users to new API field. |

---

## 9. Scaling & Bottleneck Considerations

1. **SQS queue throughput**: A single SQS queue per HostedCluster is sufficient — AWS delivers at most one interruption event per instance. For clusters with thousands of spot nodes, consider SQS long-polling configuration (already default in NTH).

2. **MHC remediation storms**: If an entire AZ loses spot capacity, all nodes in that AZ go `NotReady` simultaneously. The spot MHC with `100% maxUnhealthy` will attempt to replace them all at once. CAPI MachineDeployment's `maxSurge` limits how many new machines are created. This is correct — no thundering herd on the cloud API.

3. **Ignition server load**: Mass spot replacement means many nodes bootstrapping simultaneously. The ignition server should handle this (it's stateless, serves from cache), but monitor ignition endpoint latency during large-scale interruptions.

4. **Control plane resource footprint**: NTH is lightweight (~50Mi memory). No additional data plane components are needed — all monitoring runs in the hosted control plane namespace. This keeps the data plane slim per our design principles.

5. **Multi-AZ spread**: Consider documenting that spot NodePools should spread across multiple AZs (via multiple NodePools or subnet configuration) to reduce the blast radius of AZ-level capacity reclamation.

---

## 10. Implementation Phases

**Phase 1 — Fix the Bug + API (Target: immediate)**
- Fix `awsMachineTemplateSpec()` to set `SpotMarketOptions` when spot is enabled
- Add `marketType` field to `NodePoolSpec`
- Add `AWSSpotMarketOptions` to `AWSNodePoolPlatform`
- Backward compat: `isSpotEnabled()` checks both annotation and API field
- Add `ValidMarketType` condition
- Unit tests for all of the above

**Phase 2 — Azure Support + Upgrade Improvements**
- Add `AzureSpotVMOptions` to `AzureNodePoolPlatform`
- Wire `SpotVMOptions` in `azureMachineTemplateSpec()`
- Adjust default rolling update parameters for spot
- Propagate effective drain timeouts
- Unit + E2E tests for Azure spot

**Phase 3 — Operational Maturity**
- Auto-provision SQS queue for AWS
- In-place upgrader stability improvements
- `SpotInterruptionHandlerReady` condition
- Deprecation warnings for annotation
- Documentation and upgrade guides
