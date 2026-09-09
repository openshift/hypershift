package util

import (
	"testing"

	"github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
)

func TestCreateCluster(t *testing.T) {
	opts := &PlatformAgnosticOptions{RawCreateOptions: *core.DefaultOptions()}
	err := createCluster(t.Context(), &v1beta1.HostedCluster{}, opts, t.TempDir())
	if err == nil {
		t.Fatal("expected invalid cluster options to stop creation")
	}
}

func TestRenderCreate(t *testing.T) {
	opts := core.DefaultOptions()
	if err := renderCreate(t.Context(), opts, nil, "/nonexistent/output/manifests.yaml", "/nonexistent/render.log", "/nonexistent/create.log", core.ResolveClientProvider()); err == nil {
		t.Fatal("expected an invalid log path to stop rendering")
	}
}

func TestDestroyCluster(t *testing.T) {
	if err := destroyCluster(t.Context(), t, &v1beta1.HostedCluster{}, &PlatformAgnosticOptions{}, "/nonexistent"); err == nil {
		t.Fatal("expected an invalid log path to stop destruction")
	}
}
