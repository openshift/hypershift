package controlplane

const (
	// ControlPlaneComponentFinalizer is the finalizer added to control plane component resources to prevent premature deletion.
	ControlPlaneComponentFinalizer = "hypershift.openshift.io/component-finalizer"

	// KarpenterComponentName is the component name for the Karpenter controller.
	KarpenterComponentName = "karpenter"
	// KarpenterOperatorComponentName is the component name for the Karpenter operator.
	KarpenterOperatorComponentName = "karpenter-operator"
)

// CAPIComponents lists the Cluster API component names managed by the control plane.
var CAPIComponents = []string{"cluster-api", "capi-provider"}
