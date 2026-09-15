package openstack

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
)

func TestNewDestroyCommand(t *testing.T) {
	t.Parallel()

	t.Run("When command is created it should have correct use, short description and wire Run", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		opts := &core.DestroyOptions{}
		cmd := NewDestroyCommand(opts)
		g.Expect(cmd.Use).To(Equal("openstack"))
		g.Expect(cmd.Short).To(Equal("Destroys a HostedCluster and its associated infrastructure on OpenStack"))
		g.Expect(cmd.SilenceUsage).To(BeTrue())
		// OpenStack wires Run (not RunE) because it installs a SIGINT handler and
		// calls os.Exit on failure, so we assert on Run here.
		g.Expect(cmd.Run).ToNot(BeNil())
	})
}

func TestDestroyCluster(t *testing.T) {
	t.Parallel()

	// The command's Run handler calls os.Exit on failure, so it cannot be invoked
	// directly from a test. DestroyCluster is the function it delegates to, so we
	// exercise the failure path here instead.
	t.Run("When called without a reachable cluster it should return an error", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		opts := &core.DestroyOptions{
			Name:       "test-cluster",
			Namespace:  "clusters",
			Kubeconfig: "/nonexistent/kubeconfig/path",
			Log:        log.Log,
		}
		err := DestroyCluster(context.Background(), opts)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("unable to get kubernetes config"))
	})
}

func TestDestroyPlatformSpecifics(t *testing.T) {
	t.Parallel()

	t.Run("When called it should be a no-op and return nil", func(t *testing.T) {
		t.Parallel()
		g := NewGomegaWithT(t)
		opts := &core.DestroyOptions{
			Name:      "test-cluster",
			Namespace: "clusters",
			InfraID:   "test-infra",
		}
		err := destroyPlatformSpecifics(context.Background(), opts)
		g.Expect(err).ToNot(HaveOccurred())
	})
}
