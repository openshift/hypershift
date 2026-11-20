package awsutil

const (
	// ClusterTagOwnedValue marks resources owned exclusively by the tagged cluster.
	ClusterTagOwnedValue = "owned"

	HypershiftClusterNameTagKey = "hypershift.openshift.io/cluster-name"
	HypershiftInfraIDTagKey     = "hypershift.openshift.io/infra-id"
	HypershiftSourceTagKey      = "hypershift.openshift.io/source"
	HypershiftProwJobIDTagKey   = "hypershift.openshift.io/prow-job-id"
)

// ClusterTag returns the Kubernetes cluster ownership tag key for the given infrastructure ID.
func ClusterTag(infraID string) string {
	return "kubernetes.io/cluster/" + infraID
}
