package scheduler

const (
	ControlPlaneTaint                 = "hypershift.openshift.io/control-plane"
	ControlPlaneServingComponentTaint = "hypershift.openshift.io/request-serving-component"
	OSDFleetManagerPairedNodesLabel   = "osd-fleet-manager.openshift.io/paired-nodes"
	GoMemLimitLabel                   = "hypershift.openshift.io/request-serving-gomemlimit"
)
