package nodepool

const (
	// PerformanceProfileConfigMapLabel marks ConfigMaps containing performance profile configuration for a NodePool.
	PerformanceProfileConfigMapLabel = "hypershift.openshift.io/performanceprofile-config"
	// NodeTuningGeneratedPerformanceProfileStatusLabel marks ConfigMaps with NTO-generated performance profile status.
	NodeTuningGeneratedPerformanceProfileStatusLabel = "hypershift.openshift.io/nto-generated-performance-profile-status"
	// KubeletConfigConfigMapLabel marks ConfigMaps containing kubelet configuration for a NodePool.
	KubeletConfigConfigMapLabel = "hypershift.openshift.io/kubeletconfig-config"
	// NTOMirroredConfigLabel marks ConfigMaps mirrored by the Node Tuning Operator into the hosted cluster.
	NTOMirroredConfigLabel = "hypershift.openshift.io/mirrored-config"

	// QualifiedNameMaxLength is the maximal name length allowed for k8s objects.
	QualifiedNameMaxLength = 63
)
