package openstack

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"

	"github.com/spf13/pflag"
)

func TestNewDestroyCommand(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		verify func(t *testing.T, opts *core.DestroyOptions)
	}{
		"When OpenStack destroy command is created, it should have 'openstack' as use": {
			verify: func(t *testing.T, opts *core.DestroyOptions) {
				g := NewWithT(t)
				cmd := NewDestroyCommand(opts)
				g.Expect(cmd.Use).To(Equal("openstack"))
			},
		},
		"When OpenStack destroy command is created, it should register no platform-specific flags": {
			verify: func(t *testing.T, opts *core.DestroyOptions) {
				g := NewWithT(t)
				cmd := NewDestroyCommand(opts)
				var actualFlags []string
				cmd.Flags().VisitAll(func(f *pflag.Flag) {
					actualFlags = append(actualFlags, f.Name)
				})
				g.Expect(actualFlags).To(BeEmpty())
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			opts := &core.DestroyOptions{}
			test.verify(t, opts)
		})
	}
}
