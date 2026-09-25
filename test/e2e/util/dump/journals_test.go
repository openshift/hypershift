package dump

import (
	"testing"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestDumpJournals(t *testing.T) {
	t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	err := DumpJournals(t, t.Context(), &v1beta1.HostedCluster{}, t.TempDir(), "", "/nonexistent/kubeconfig")
	if err == nil {
		t.Fatal("expected invalid kubeconfig to stop journal dumping")
	}
}
