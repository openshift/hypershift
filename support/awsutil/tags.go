package awsutil

const (
	HypershiftClusterNameTagKey = "hypershift.openshift.io/cluster-name"
	HypershiftInfraIDTagKey     = "hypershift.openshift.io/infra-id"
	HypershiftSourceTagKey      = "hypershift.openshift.io/source"
	HypershiftProwJobIDTagKey   = "hypershift.openshift.io/prow-job-id"
)

// ClusterTag returns the Kubernetes cluster ownership tag key for the given infrastructure ID.
func ClusterTag(infraID string) string {
	return "kubernetes.io/cluster/" + infraID
}
