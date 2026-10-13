package cluster

import (
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestNewCreateCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T, cmd *cobra.Command)
	}{
		{
			name: "When create cluster command is created, it should have 'cluster' as use",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				g.Expect(cmd.Use).To(Equal("cluster"))
			},
		},
		{
			name: "When create cluster command is created, it should have subcommands for all platforms",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				var subcommandNames []string
				for _, sub := range cmd.Commands() {
					subcommandNames = append(subcommandNames, sub.Name())
				}

				g.Expect(subcommandNames).To(HaveLen(5))
				g.Expect(subcommandNames).To(ContainElement("agent"))
				g.Expect(subcommandNames).To(ContainElement("aws"))
				g.Expect(subcommandNames).To(ContainElement("azure"))
				g.Expect(subcommandNames).To(ContainElement("kubevirt"))
				g.Expect(subcommandNames).To(ContainElement("openstack"))
			},
		},
		{
			name: "When create cluster command is created, it should mark service-cidr and default-dual as mutually exclusive",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				serviceCIDRFlag := cmd.Flag("service-cidr")
				g.Expect(serviceCIDRFlag).ToNot(BeNil())
				g.Expect(serviceCIDRFlag.Annotations).To(HaveKey("cobra_annotation_mutually_exclusive"))
				g.Expect(serviceCIDRFlag.Annotations["cobra_annotation_mutually_exclusive"]).To(ContainElement("service-cidr default-dual"))

				defaultDualFlag := cmd.Flag("default-dual")
				g.Expect(defaultDualFlag).ToNot(BeNil())
				g.Expect(defaultDualFlag.Annotations).To(HaveKey("cobra_annotation_mutually_exclusive"))
				g.Expect(defaultDualFlag.Annotations["cobra_annotation_mutually_exclusive"]).To(ContainElement("service-cidr default-dual"))
			},
		},
		{
			name: "When create cluster command is created, it should mark cluster-cidr and default-dual as mutually exclusive",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				clusterCIDRFlag := cmd.Flag("cluster-cidr")
				g.Expect(clusterCIDRFlag).ToNot(BeNil())
				g.Expect(clusterCIDRFlag.Annotations).To(HaveKey("cobra_annotation_mutually_exclusive"))
				g.Expect(clusterCIDRFlag.Annotations["cobra_annotation_mutually_exclusive"]).To(ContainElement("cluster-cidr default-dual"))

				defaultDualFlag := cmd.Flag("default-dual")
				g.Expect(defaultDualFlag).ToNot(BeNil())
				g.Expect(defaultDualFlag.Annotations).To(HaveKey("cobra_annotation_mutually_exclusive"))
				g.Expect(defaultDualFlag.Annotations["cobra_annotation_mutually_exclusive"]).To(ContainElement("cluster-cidr default-dual"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := NewCreateCommands()
			tt.test(t, cmd)
		})
	}
}

func TestNewDestroyCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T, cmd *cobra.Command)
	}{
		{
			name: "When destroy cluster command is created, it should have 'cluster' as use",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)
				g.Expect(cmd.Use).To(Equal("cluster"))
			},
		},
		{
			name: "When destroy cluster command is created, it should have subcommands for all platforms",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				var subcommandNames []string
				for _, sub := range cmd.Commands() {
					subcommandNames = append(subcommandNames, sub.Name())
				}

				g.Expect(subcommandNames).To(HaveLen(5))
				g.Expect(subcommandNames).To(ContainElement("agent"))
				g.Expect(subcommandNames).To(ContainElement("aws"))
				g.Expect(subcommandNames).To(ContainElement("azure"))
				g.Expect(subcommandNames).To(ContainElement("kubevirt"))
				g.Expect(subcommandNames).To(ContainElement("openstack"))
			},
		},
		{
			name: "When destroy cluster command is created, it should default namespace to 'clusters'",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				nsFlag := cmd.Flag("namespace")
				g.Expect(nsFlag).ToNot(BeNil())
				g.Expect(nsFlag.DefValue).To(Equal("clusters"))
			},
		},
		{
			name: "When destroy cluster command is created, it should default cluster-grace-period to 10 minutes",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				gpFlag := cmd.Flag("cluster-grace-period")
				g.Expect(gpFlag).ToNot(BeNil())
				g.Expect(gpFlag.DefValue).To(Equal("10m0s"))
			},
		},
		{
			name: "When destroy cluster command is created, it should default destroy-cloud-resources to true",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				dcrFlag := cmd.Flag("destroy-cloud-resources")
				g.Expect(dcrFlag).ToNot(BeNil())
				g.Expect(dcrFlag.DefValue).To(Equal("true"))
			},
		},
		{
			name: "When destroy cluster command is created, it should mark 'name' as required",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				nameFlag := cmd.Flag("name")
				g.Expect(nameFlag).ToNot(BeNil())
				g.Expect(nameFlag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(nameFlag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When destroy cluster command is created, it should register exactly the expected persistent flags",
			test: func(t *testing.T, cmd *cobra.Command) {
				g := NewWithT(t)

				expectedFlags := []string{
					"cluster-grace-period",
					"destroy-cloud-resources",
					"infra-id",
					"name",
					"namespace",
				}
				var actualFlags []string
				cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
					actualFlags = append(actualFlags, f.Name)
				})
				sort.Strings(actualFlags)
				g.Expect(actualFlags).To(Equal(expectedFlags))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := NewDestroyCommands()
			tt.test(t, cmd)
		})
	}
}
