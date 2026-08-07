package openstack

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"
)

func TestNewDestroyCommand(t *testing.T) {
	tests := map[string]struct {
		expectedUse   string
		expectedShort string
	}{
		"When command is created it should have correct use and short description": {
			expectedUse:   "openstack",
			expectedShort: "Destroys a HostedCluster and its associated infrastructure on OpenStack",
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

func TestDestroyPlatformSpecifics(t *testing.T) {
	t.Run("When called it should be a no-op and return nil", func(t *testing.T) {
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
