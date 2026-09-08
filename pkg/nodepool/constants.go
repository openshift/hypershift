package nodepool

const (
	PerformanceProfileConfigMapLabel                 = "hypershift.openshift.io/performanceprofile-config"
	NodeTuningGeneratedPerformanceProfileStatusLabel = "hypershift.openshift.io/nto-generated-performance-profile-status"
	KubeletConfigConfigMapLabel                      = "hypershift.openshift.io/kubeletconfig-config"
	NTOMirroredConfigLabel                           = "hypershift.openshift.io/mirrored-config"

	// QualifiedNameMaxLength is the maximal name length allowed for k8s objects.
	QualifiedNameMaxLength = 63
)
