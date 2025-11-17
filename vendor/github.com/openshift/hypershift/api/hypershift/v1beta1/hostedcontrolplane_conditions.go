package v1beta1

// "Condition values may change back and forth, but some condition transitions may be monotonic, depending on the resource and condition type.
// However, conditions are observations and not, themselves, state machines."
// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

// Conditions for HostedControlPlane.
const (
	// HostedControlPlaneAvailable indicates whether the HostedControlPlane has a healthy control plane.
	// When True, the control plane is operational and serving requests.
	// When False, the control plane is experiencing issues that prevent normal operation.
	HostedControlPlaneAvailable ConditionType = "Available"

	// HostedControlPlaneDegraded indicates whether the HostedControlPlane is encountering
	// an error that may require user intervention to resolve.
	// When True, the control plane is degraded and experiencing issues.
	// When False, the control plane is operating normally without degradation.
	HostedControlPlaneDegraded ConditionType = "Degraded"

	// EtcdSnapshotRestored indicates whether an etcd snapshot has been restored.
	// When True, an etcd snapshot has been successfully restored.
	// When False, no etcd snapshot restoration has occurred or restoration failed.
	EtcdSnapshotRestored ConditionType = "EtcdSnapshotRestored"

	// CVOScaledDown indicates whether the Cluster Version Operator has been scaled down.
	// When True, the CVO is scaled down (typically during maintenance operations).
	// When False, the CVO is running normally.
	CVOScaledDown ConditionType = "CVOScaledDown"

	// DataPlaneToControlPlaneConnectivity indicates whether the data plane can successfully
	// reach the control plane components.
	// When True, data plane nodes have healthy connectivity to control plane services.
	// When False, there are network connectivity issues preventing data plane from reaching the control plane.
	// A failure here may indicate network policy issues, firewall rules, or infrastructure problems.
	DataPlaneToControlPlaneConnectivity ConditionType = "DataPlaneToControlPlaneConnectivity"

	// ControlPlaneToDataPlaneConnectivity indicates whether the control plane can successfully
	// reach the data plane components.
	// When True, control plane has healthy connectivity to data plane nodes.
	// When False, there are network connectivity issues preventing control plane from reaching the data plane.
	// A failure here may indicate network policy issues, firewall rules, or infrastructure problems.
	ControlPlaneToDataPlaneConnectivity ConditionType = "ControlPlaneToDataPlaneConnectivity"
)
