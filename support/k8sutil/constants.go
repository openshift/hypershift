package k8sutil

const (
	// DebugDeploymentsAnnotation contains a comma separated list of deployment names which should always be scaled to 0
	// for development.
	DebugDeploymentsAnnotation = "hypershift.openshift.io/debug-deployments"
	// HostedClusterAnnotation is the annotation key that links a resource to its owning HostedCluster (namespace/name).
	HostedClusterAnnotation = "hypershift.openshift.io/cluster"

	// ReferencedResourceAnnotationPrefix is the prefix of the annotation keys
	// that the HyperShift operator stamps on a resource in the HostedCluster
	// namespace when that resource is referenced by the HostedCluster spec. The
	// suffix is the HostedCluster's name. Controllers watching a namespace can
	// use the prefix to react to content changes of referenced resources, whose
	// names are user-chosen and therefore not statically known.
	ReferencedResourceAnnotationPrefix = "referenced-resource.hypershift.openshift.io/"

	// GCPLabelCluster is the GCP resource label key used to identify the HostedCluster name.
	GCPLabelCluster = "hypershift-openshift-io-cluster"
	// GCPLabelInfraID is the GCP resource label key used to identify the infrastructure ID.
	GCPLabelInfraID = "hypershift-openshift-io-infra-id"
)
