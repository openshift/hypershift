package dump

import (
	"testing"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
)

func TestDumpHostedCluster(t *testing.T) {
	err := DumpHostedCluster(t.Context(), t, &v1beta1.HostedCluster{}, false, map[core.DumpGuestClusterPolicy]struct{}{}, t.TempDir(), "/nonexistent/kubeconfig")
	if err == nil {
		t.Fatal("expected invalid kubeconfig to stop dumping")
	}
}

func TestDumpMachineConsoleLogs(t *testing.T) {
	err := DumpMachineConsoleLogs(t.Context(), &v1beta1.HostedCluster{}, awsutil.AWSCredentialsOptions{}, t.TempDir(), "/nonexistent/kubeconfig")
	if err == nil {
		t.Fatal("expected invalid kubeconfig to stop collecting console logs")
	}
}
