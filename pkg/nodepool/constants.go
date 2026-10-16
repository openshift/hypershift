package nodepool

const (
	// PerformanceProfileConfigMapLabel marks ConfigMaps containing performance profile configuration for a NodePool.
	PerformanceProfileConfigMapLabel = "hypershift.openshift.io/performanceprofile-config"
	// NodeTuningGeneratedPerformanceProfileStatusLabel marks ConfigMaps with NTO-generated performance profile status.
	NodeTuningGeneratedPerformanceProfileStatusLabel = "hypershift.openshift.io/nto-generated-performance-profile-status"

	// QualifiedNameMaxLength is the maximal name length allowed for k8s objects.
	QualifiedNameMaxLength = 63
)
