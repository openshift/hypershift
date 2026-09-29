package scheduler

const (
	// ControlPlaneTaint is the taint applied to nodes dedicated to hosting control plane pods.
	ControlPlaneTaint = "hypershift.openshift.io/control-plane"
	// ControlPlaneServingComponentTaint is the taint for nodes running request-serving control plane components.
	ControlPlaneServingComponentTaint = "hypershift.openshift.io/request-serving-component"
	// OSDFleetManagerPairedNodesLabel groups nodes into pairs for OSD fleet manager scheduling.
	OSDFleetManagerPairedNodesLabel = "osd-fleet-manager.openshift.io/paired-nodes"
	// GoMemLimitLabel specifies the GOMEMLIMIT value for request-serving pods on a node.
	GoMemLimitLabel = "hypershift.openshift.io/request-serving-gomemlimit"
)
