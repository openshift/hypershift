// Package labels defines shared label key constants used across HyperShift components.
package labels

const (
	// KubeletConfigConfigMapLabel marks ConfigMaps carrying KubeletConfig data.
	// Set by the nodepool controller, consumed by HCCO for mirroring into the hosted cluster.
	KubeletConfigConfigMapLabel = "hypershift.openshift.io/kubeletconfig-config"

	// NTOMirroredConfigLabel marks ConfigMaps mirrored from the HCP namespace
	// into the hosted cluster. Used by HCCO for orphan detection and by
	// ValidatingAdmissionPolicies to protect them from guest-side mutation.
	NTOMirroredConfigLabel = "hypershift.openshift.io/mirrored-config"
)
