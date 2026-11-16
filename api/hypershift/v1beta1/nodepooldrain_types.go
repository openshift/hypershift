package v1beta1

const (
	// NodePoolDrainReadinessAnnotation opts a HostedCluster into the NodePool drain-readiness
	// signal when the management-cluster operator has ENABLE_NODEPOOL_DRAIN_READINESS=1.
	NodePoolDrainReadinessAnnotation = "hypershift.openshift.io/enable-nodepool-drain-readiness"
	// NodePoolUIDAnnotation identifies the NodePool incarnation associated with a CAPI Machine.
	NodePoolUIDAnnotation = "hypershift.openshift.io/nodePoolUID"

	// HostedControlPlaneNodePoolDrainUpToDate is an HCP condition set by the HCCO nodepooldrain
	// controller. True when per-NodePool drain readiness was successfully (re)computed from the
	// guest cluster. False (reason GuestUnreachable) when it has not refreshed within the grace
	// window, signaling consumers to treat NodePoolDrain data as stale (fail-open).
	HostedControlPlaneNodePoolDrainUpToDate = "NodePoolDrainUpToDate"
)

// NodePoolDrainState is the observed drain state for a NodePool.
type NodePoolDrainState string

const (
	// NodePoolDrainStateBlocked means a structural PDB blocker was found.
	NodePoolDrainStateBlocked NodePoolDrainState = "Blocked"
	// NodePoolDrainStateDrainable means no structural PDB blocker was found.
	NodePoolDrainStateDrainable NodePoolDrainState = "Drainable"
)

// NodePoolDrain reports whether a NodePool's nodes can currently be drained during an upgrade.
//
// It carries no customer workload identifiers (namespace/PDB/pod names). The NodePool UID
// distinguishes recreated pools with the same name. A generic in-guest Event reports transitions.
type NodePoolDrain struct {
	// nodePoolName is the NodePool these nodes belong to, from the node label
	// hypershift.openshift.io/nodePool.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	NodePoolName string `json:"nodePoolName,omitempty"`

	// nodePoolUID is the UID of the NodePool incarnation that owns these nodes.
	// Consumers must treat absent or mismatched UIDs as unverified.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	NodePoolUID string `json:"nodePoolUID,omitempty"`

	// state is Blocked when a structurally blocking customer PodDisruptionBudget would prevent
	// eviction of a drain candidate Pod on this pool's nodes; otherwise it is Drainable.
	// +required
	// +kubebuilder:validation:Enum=Blocked;Drainable
	State NodePoolDrainState `json:"state,omitempty"`

	// blockingCount is the number of PodDisruptionBudgets contributing to state=Blocked.
	// +optional
	BlockingCount *int32 `json:"blockingCount,omitempty"`

	// reason is a bounded machine-readable reason, e.g. BlockedByPodDisruptionBudget or Drainable.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	Reason string `json:"reason,omitempty"`
}
