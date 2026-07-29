package kubevirt

import (
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
			expectedUse:   "kubevirt",
			expectedShort: "Destroys a HostedCluster and its associated infrastructure on Kubevirt platform",
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
