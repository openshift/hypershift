package kubevirt

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
)

func TestNewDestroyCommand(t *testing.T) {
	t.Parallel()

	t.Run("When command is created it should have correct use, short description and wire RunE", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		opts := &core.DestroyOptions{}
		cmd := NewDestroyCommand(opts)
		g.Expect(cmd.Use).To(Equal("kubevirt"))
		g.Expect(cmd.Short).To(Equal("Destroys a HostedCluster and its associated infrastructure on Kubevirt platform"))
		g.Expect(cmd.SilenceUsage).To(BeTrue())
		g.Expect(cmd.RunE).ToNot(BeNil())
	})

	t.Run("When RunE runs without a reachable cluster it should return an error", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		opts := &core.DestroyOptions{
			Name:       "test-cluster",
			Namespace:  "clusters",
			Kubeconfig: "/nonexistent/kubeconfig/path",
			Log:        log.Log,
		}
		cmd := NewDestroyCommand(opts)
		err := cmd.RunE(cmd, nil)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unable to get kubernetes config"))
	})
}
