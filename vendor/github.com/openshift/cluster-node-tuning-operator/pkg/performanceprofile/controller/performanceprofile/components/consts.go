package components

const (
	// AssetsDir defines the directory with assets under the operator image
	AssetsDir = "/assets"
)

const (
	// ComponentNamePrefix defines the worker role for performance sensitive workflows
	// TODO: change it back to longer name once https://bugzilla.redhat.com/show_bug.cgi?id=1787907 fixed
	// ComponentNamePrefix = "worker-performance"
	ComponentNamePrefix = "performance"
	// MachineConfigRoleLabelKey is the label key to use as label and in MachineConfigSelector of MCP which targets the performance profile
	MachineConfigRoleLabelKey = "machineconfiguration.openshift.io/role"
	// NodeRoleLabelPrefix is the prefix for the role label of a node
	NodeRoleLabelPrefix = "node-role.kubernetes.io/"
)

const (
	// NamespaceNodeTuningOperator defines the tuned profiles namespace
	NamespaceNodeTuningOperator = "openshift-cluster-node-tuning-operator"
	// ProfileNamePerformance defines the performance tuned profile name
	ProfileNamePerformance = "openshift-node-performance"
)

const (
	// HugepagesSize2M contains the size of 2M hugepages
	HugepagesSize2M = "2M"
	// HugepagesSize1G contains the size of 1G hugepages
	HugepagesSize1G = "1G"
)
