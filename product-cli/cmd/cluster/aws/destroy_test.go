package aws

import (
	"sort"
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
		"When AWS destroy command is created, it should have 'aws' as use": {
			verify: func(t *testing.T, opts *core.DestroyOptions) {
				g := NewWithT(t)
				cmd := NewDestroyCommand(opts)
				g.Expect(cmd.Use).To(Equal("aws"))
			},
		},
		"When AWS destroy command is created, it should default region to us-east-1": {
			verify: func(t *testing.T, opts *core.DestroyOptions) {
				g := NewWithT(t)
				cmd := NewDestroyCommand(opts)
				g.Expect(opts.AWSPlatform.Region).To(Equal("us-east-1"))
				g.Expect(cmd.Flag("region").DefValue).To(Equal("us-east-1"))
			},
		},
		"When AWS destroy command is created, it should default preserveIAM to false": {
			verify: func(t *testing.T, opts *core.DestroyOptions) {
				g := NewWithT(t)
				cmd := NewDestroyCommand(opts)
				g.Expect(opts.AWSPlatform.PreserveIAM).To(BeFalse())
				g.Expect(cmd.Flag("preserve-iam").DefValue).To(Equal("false"))
			},
		},
		"When AWS destroy command is created, it should register exactly the expected flags": {
			verify: func(t *testing.T, opts *core.DestroyOptions) {
				g := NewWithT(t)
				cmd := NewDestroyCommand(opts)
				expectedFlags := []string{
					"aws-infra-grace-period",
					"base-domain",
					"base-domain-prefix",
					"preserve-iam",
					"region",
					"role-arn",
					"secret-creds",
					"sts-creds",
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
