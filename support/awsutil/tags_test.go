package awsutil

import "testing"

func TestClusterTag(t *testing.T) {
	if got, want := ClusterTag("infra-id"), "kubernetes.io/cluster/infra-id"; got != want {
		t.Fatalf("ClusterTag() = %q, want %q", got, want)
	}
}
