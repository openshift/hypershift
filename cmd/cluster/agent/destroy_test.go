package agent

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"
)

func TestNewDestroyCommand(t *testing.T) {
	tests := map[string]struct {
		expectedUse   string
		expectedShort string
	}{
		"When command is created it should have correct use and short description": {
			expectedUse:   "agent",
			expectedShort: "Destroys a HostedCluster and its associated infrastructure on Agent",
		},
	}

	for name, test := range tests {
		t.Run(name, func(tt *testing.T) {
			g := NewGomegaWithT(tt)
			opts := &core.DestroyOptions{}
			cmd := NewDestroyCommand(opts)
			g.Expect(cmd.Use).To(Equal(test.expectedUse))
			g.Expect(cmd.Short).To(Equal(test.expectedShort))
			g.Expect(cmd.SilenceUsage).To(BeTrue())
		})
	}
}

func TestDestroyOptions(t *testing.T) {
	t.Run("When DestroyOptions is created it should have correct zero values", func(t *testing.T) {
		g := NewGomegaWithT(t)
		opts := &DestroyOptions{}
		g.Expect(opts.Namespace).To(BeEmpty())
		g.Expect(opts.Name).To(BeEmpty())
		g.Expect(opts.ClusterGracePeriod).To(Equal(time.Duration(0)))
	})

	t.Run("When DestroyOptions fields are set it should retain the values", func(t *testing.T) {
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
