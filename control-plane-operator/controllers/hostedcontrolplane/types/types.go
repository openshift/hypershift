package types

const (
	// PodSafeToEvictKey is an annotation used by the CA operator which makes sure
	// all the pods annotated with it, could be drained properly.
	// source https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#what-types-of-pods-can-prevent-ca-from-removing-a-node
	PodSafeToEvictKey = "cluster-autoscaler.kubernetes.io/safe-to-evict"
	// PodSafeToEvictLocalVolumesKey is an annotation used by the CA operator which makes sure
	// all the pods annotated with it and the picking the desired local volumes that are safe to evict, could be drained properly.
	PodSafeToEvictLocalVolumesKey = "cluster-autoscaler.kubernetes.io/safe-to-evict-local-volumes"
)
