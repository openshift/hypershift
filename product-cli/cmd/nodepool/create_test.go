package nodepool

import (
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/spf13/pflag"
)

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		verify func(g Gomega)
	}{
		{
			name: "When create nodepool command is created, it should have 'nodepool' as use",
			verify: func(g Gomega) {
				cmd := NewCreateCommand()
				g.Expect(cmd.Use).To(Equal("nodepool"))
			},
		},
		{
			name: "When create nodepool command is created, it should have subcommands for all platforms",
			verify: func(g Gomega) {
				cmd := NewCreateCommand()
				var names []string
				for _, sub := range cmd.Commands() {
					names = append(names, sub.Name())
				}
				sort.Strings(names)
				g.Expect(names).To(Equal([]string{"agent", "aws", "azure", "kubevirt", "openstack"}))
				g.Expect(cmd.Commands()).To(HaveLen(5))
			},
		},
		{
			name: "When create nodepool command is created, it should default replicas to 2",
			verify: func(g Gomega) {
				cmd := NewCreateCommand()
				g.Expect(cmd.Flag("replicas").DefValue).To(Equal("2"))
			},
		},
		{
			name: "When create nodepool command is created, it should default arch to amd64",
			verify: func(g Gomega) {
				cmd := NewCreateCommand()
				g.Expect(cmd.Flag("arch").DefValue).To(Equal("amd64"))
			},
		},
		{
			name: "When create nodepool command is created, it should default namespace to clusters",
			verify: func(g Gomega) {
				cmd := NewCreateCommand()
				g.Expect(cmd.Flag("namespace").DefValue).To(Equal("clusters"))
			},
		},
		{
			name: "When create nodepool command is created, it should default cluster-name to example",
			verify: func(g Gomega) {
				cmd := NewCreateCommand()
				g.Expect(cmd.Flag("cluster-name").DefValue).To(Equal("example"))
			},
		},
		{
			name: "When create nodepool command is created, it should mark 'name' as required",
			verify: func(g Gomega) {
				cmd := NewCreateCommand()
				nameFlag := cmd.PersistentFlags().Lookup("name")
				g.Expect(nameFlag).NotTo(BeNil())
				annotations := nameFlag.Annotations
				g.Expect(annotations).To(HaveKey("cobra_annotation_bash_completion_one_required_flag"))
			},
		},
		{
			name: "When create nodepool command is created, it should mark 'node-count' flag as deprecated",
			verify: func(g Gomega) {
				cmd := NewCreateCommand()
				nodeCountFlag := cmd.Flag("node-count")
				g.Expect(nodeCountFlag).NotTo(BeNil())
				g.Expect(nodeCountFlag.Deprecated).NotTo(BeEmpty())
			},
		},
		{
			name: "When create nodepool command is created, it should register exactly the expected persistent flags",
			verify: func(g Gomega) {
				cmd := NewCreateCommand()
				expectedFlags := []string{
					"arch",
					"auto-repair",
					"cluster-name",
					"name",
					"namespace",
					"node-count",
					"node-upgrade-type",
					"release-image",
					"render",
					"replicas",
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

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			tc.verify(g)
		})
	}
}
