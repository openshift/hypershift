package config

const (
	// PodSafeToEvictLocalVolumesKey is an annotation used by the CA operator which makes sure
	// all the pods annotated with it and the picking the desired local volumes that are safe to evict, could be drained properly.
	PodSafeToEvictLocalVolumesKey = "cluster-autoscaler.kubernetes.io/safe-to-evict-local-volumes"

	// HCCOUser references the user used by the HostedClusterConfigOperator
	HCCOUser = "hosted-cluster-config"
	// HCCOUserAgent references the userAgent used by the HostedClusterConfigOperator
	HCCOUserAgent = "hosted-cluster-config-operator-manager"

	// PodTmpDirMountName is a name for a volume created in each pod by the CPO that gives the pods containers a place to mount and write temporary files to.
	PodTmpDirMountName = "tmp-dir"
	// PodTmpDirMountPath is the path that each container created by the CPO will mount the volume PodTmpDirMountName at.
	PodTmpDirMountPath = "/tmp"
)
