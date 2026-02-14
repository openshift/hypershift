package config

const (
	// PodSafeToEvictLocalVolumesKey is an annotation used by the CA operator which makes sure
	// all the pods annotated with it and the picking the desired local volumes that are safe to evict, could be drained properly.
	PodSafeToEvictLocalVolumesKey = "cluster-autoscaler.kubernetes.io/safe-to-evict-local-volumes"

	// HCCOUser references the user used by the HostedClusterConfigOperator
	HCCOUser = "hosted-cluster-config"
	// HCCOUserAgent references the userAgent used by the HostedClusterConfigOperator
	HCCOUserAgent = "hosted-cluster-config-operator-manager"

	// KASBootstrapContainerUser references the user used by the KAS bootstrap container
	KASBootstrapContainerUser = "kas-bootstrap-container"
)
