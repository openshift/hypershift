package ntotuning

const (
	// ConfigKey is the ConfigMap data key holding a serialized Tuned or PerformanceProfile manifest.
	ConfigKey = "tuning"

	// TunedConfigMapLabel labels ConfigMaps in the hosted control plane namespace that hold
	// serialized Tuned manifests for NTO to mirror into the guest cluster.
	TunedConfigMapLabel = "hypershift.openshift.io/tuned-config"

	// PerformanceProfileConfigMapLabel labels ConfigMaps in the hosted control plane namespace
	// that hold serialized PerformanceProfile manifests for NTO to mirror into the guest cluster.
	PerformanceProfileConfigMapLabel = "hypershift.openshift.io/performanceprofile-config"
)
