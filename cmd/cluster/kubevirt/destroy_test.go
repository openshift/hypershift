package kubevirt

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
)

// The KubeVirt destroy path is a thin wrapper around none.DestroyCluster; the
// destroy logic itself is covered by cmd/cluster/none. These tests only assert
// that the command is wired to that delegation.
func TestNewDestroyCommand(t *testing.T) {
	t.Run("When command is created, it should be invoked as 'kubevirt'", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		opts := &core.DestroyOptions{}
		cmd := NewDestroyCommand(opts)
		g.Expect(cmd.Use).To(Equal("kubevirt"))
	})

	// t.Setenv forbids t.Parallel, so this subtest runs serially.
	t.Run("When the destroy path fails, it should propagate the error instead of swallowing it", func(t *testing.T) {
		g := NewGomegaWithT(t)
		t.Setenv("FAKE_CLIENT", "true")

		// No HostedCluster exists and no infra ID is set, so the delegated
		// destroy path fails. RunE must return that error so the CLI exits
		// non-zero rather than logging and reporting success.
		opts := &core.DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			Log:       log.Log,
		}
		cmd := NewDestroyCommand(opts)
		cmd.SetContext(t.Context())
		g.Expect(cmd.RunE(cmd, nil)).To(HaveOccurred())
	})
}
