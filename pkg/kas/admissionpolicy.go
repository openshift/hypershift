package kas

const (
	// AdmissionPolicyNameConfig is the ValidatingAdmissionPolicy name for cluster configuration resources.
	AdmissionPolicyNameConfig = "config"
	// AdmissionPolicyNameMirror is the ValidatingAdmissionPolicy name for mirrored resources.
	AdmissionPolicyNameMirror = "mirror"
	// AdmissionPolicyNameICSP is the ValidatingAdmissionPolicy name for ImageContentSourcePolicy resources.
	AdmissionPolicyNameICSP = "icsp"
	// AdmissionPolicyNameInfra is the ValidatingAdmissionPolicy name for infrastructure resources.
	AdmissionPolicyNameInfra = "infra"
	// AdmissionPolicyNameNTOMirroredConfigs is the ValidatingAdmissionPolicy name for NTO mirrored ConfigMaps.
	AdmissionPolicyNameNTOMirroredConfigs = "ntomirroredconfigmaps"
	// AdmissionPolicyNameRBAC is the ValidatingAdmissionPolicy name for managed RBAC resources.
	AdmissionPolicyNameRBAC = "managed-rbac"
)
