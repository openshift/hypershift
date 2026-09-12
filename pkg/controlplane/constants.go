package controlplane

const (
	ControlPlaneComponentFinalizer = "hypershift.openshift.io/component-finalizer"

	KarpenterComponentName         = "karpenter"
	KarpenterOperatorComponentName = "karpenter-operator"
)

var CAPIComponents = []string{"cluster-api", "capi-provider"}
