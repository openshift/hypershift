package k8sutil

const (
	// DebugDeploymentsAnnotation contains a comma separated list of deployment names which should always be scaled to 0
	// for development.
	DebugDeploymentsAnnotation = "hypershift.openshift.io/debug-deployments"
	// EnableHostedClustersAnnotationScopingEnv is the env var that enables annotation-based scoping for hosted clusters.
	EnableHostedClustersAnnotationScopingEnv = "ENABLE_HOSTEDCLUSTERS_ANNOTATION_SCOPING"
	// HostedClustersScopeAnnotationEnv is the env var that specifies the scope annotation value to match.
	HostedClustersScopeAnnotationEnv = "HOSTEDCLUSTERS_SCOPE_ANNOTATION"
	// HostedClustersScopeAnnotation is the annotation key used to scope hosted clusters to specific operators.
	HostedClustersScopeAnnotation = "hypershift.openshift.io/scope"
	// HostedClusterAnnotation is the annotation key that links a resource to its owning HostedCluster (namespace/name).
	HostedClusterAnnotation = "hypershift.openshift.io/cluster"

	// GCPLabelCluster is the GCP resource label key used to identify the HostedCluster name.
	GCPLabelCluster = "hypershift-openshift-io-cluster"
	// GCPLabelInfraID is the GCP resource label key used to identify the infrastructure ID.
	GCPLabelInfraID = "hypershift-openshift-io-infra-id"
)
