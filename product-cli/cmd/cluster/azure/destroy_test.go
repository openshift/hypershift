package azure

import (
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/cluster/core"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestNewDestroyCommand(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		verify func(t *testing.T, opts *core.DestroyOptions)
	}{
		"When Azure destroy command is created, it should have 'azure' as use": {
			verify: func(t *testing.T, opts *core.DestroyOptions) {
				g := NewWithT(t)
				cmd := NewDestroyCommand(opts)
				g.Expect(cmd.Use).To(Equal("azure"))
			},
		},
		"When Azure destroy command is created, it should default location to eastus": {
			verify: func(t *testing.T, opts *core.DestroyOptions) {
				g := NewWithT(t)
				cmd := NewDestroyCommand(opts)
				g.Expect(opts.AzurePlatform.Location).To(Equal("eastus"))
				g.Expect(cmd.Flag("location").DefValue).To(Equal("eastus"))
			},
		},
		"When Azure destroy command is created, it should mark azure-creds as required": {
			verify: func(t *testing.T, opts *core.DestroyOptions) {
				g := NewWithT(t)
				cmd := NewDestroyCommand(opts)

				azureCredsFlag := cmd.Flag("azure-creds")
				g.Expect(azureCredsFlag).ToNot(BeNil())
				g.Expect(azureCredsFlag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(azureCredsFlag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		"When Azure destroy command is created, it should mark dns-zone-rg-name as required": {
			verify: func(t *testing.T, opts *core.DestroyOptions) {
				g := NewWithT(t)
				cmd := NewDestroyCommand(opts)

				dnsZoneFlag := cmd.Flag("dns-zone-rg-name")
				g.Expect(dnsZoneFlag).ToNot(BeNil())
				g.Expect(dnsZoneFlag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(dnsZoneFlag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		"When Azure destroy command is created, it should default preserve-resource-group to false": {
			verify: func(t *testing.T, opts *core.DestroyOptions) {
				g := NewWithT(t)
				cmd := NewDestroyCommand(opts)
				g.Expect(opts.AzurePlatform.PreserveResourceGroup).To(BeFalse())
				g.Expect(cmd.Flag("preserve-resource-group").DefValue).To(Equal("false"))
			},
		},
		"When Azure destroy command is created, it should register exactly the expected flags": {
			verify: func(t *testing.T, opts *core.DestroyOptions) {
				g := NewWithT(t)
				cmd := NewDestroyCommand(opts)
				expectedFlags := []string{
					"azure-creds",
					"dns-zone-rg-name",
					"location",
					"preserve-resource-group",
					"resource-group-name",
				}
				var actualFlags []string
				cmd.Flags().VisitAll(func(f *pflag.Flag) {
					actualFlags = append(actualFlags, f.Name)
				})
				sort.Strings(actualFlags)
				g.Expect(actualFlags).To(Equal(expectedFlags))
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
