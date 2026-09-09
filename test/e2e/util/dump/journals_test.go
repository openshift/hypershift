package dump

import (
	"testing"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"

	"go.uber.org/zap"
)

func TestSetupSSHKey(t *testing.T) {
	t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	_, err := setupSSHKey(t.Context(), &v1beta1.HostedCluster{})
	if err == nil {
		t.Fatal("expected invalid kubeconfig to stop SSH key setup")
	}
}

func TestSetupBastion(t *testing.T) {
	t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	_, err := setupBastion(t, t.Context(), &v1beta1.HostedCluster{}, "", zap.NewNop(), zap.NewNop())
	if err == nil {
		t.Fatal("expected invalid kubeconfig to stop bastion setup")
	}
}
