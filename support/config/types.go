package config

const (
	// PodSafeToEvictLocalVolumesKey is an annotation used by the CA operator which makes sure
	// all the pods annotated with it and the picking the desired local volumes that are safe to evict, could be drained properly.
	PodSafeToEvictLocalVolumesKey = "cluster-autoscaler.kubernetes.io/safe-to-evict-local-volumes"
)
