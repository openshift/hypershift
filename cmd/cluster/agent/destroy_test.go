package agent

import (
	"testing"
	"time"

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
		g.Expect(cmd.Use).To(Equal("agent"))
		g.Expect(cmd.Short).To(Equal("Destroys a HostedCluster and its associated infrastructure on Agent"))
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

func TestDestroyOptions(t *testing.T) {
	t.Parallel()

	t.Run("When DestroyOptions is created it should have correct zero values", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		opts := &DestroyOptions{}
		g.Expect(opts.Namespace).To(BeEmpty())
		g.Expect(opts.Name).To(BeEmpty())
		g.Expect(opts.ClusterGracePeriod).To(Equal(time.Duration(0)))
	})

	t.Run("When DestroyOptions fields are set it should retain the values", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		opts := &DestroyOptions{
			Namespace:          "clusters",
			Name:               "test-cluster",
			ClusterGracePeriod: 10 * time.Minute,
		}
		g.Expect(opts.Namespace).To(Equal("clusters"))
		g.Expect(opts.Name).To(Equal("test-cluster"))
		g.Expect(opts.ClusterGracePeriod).To(Equal(10 * time.Minute))
	})
}
