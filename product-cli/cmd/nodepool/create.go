package nodepool

import (
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/product-cli/cmd/nodepool/agent"
	"github.com/openshift/hypershift/product-cli/cmd/nodepool/aws"
	"github.com/openshift/hypershift/product-cli/cmd/nodepool/kubevirt"
	"github.com/openshift/hypershift/product-cli/cmd/nodepool/openstack"

	"github.com/spf13/cobra"
)

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "nodepool",
		Short:        "Create a Hosted Cluster NodePool",
		SilenceUsage: true,
	}

	opts := &core.CreateNodePoolOptions{
		Arch:            "amd64",
		ClusterName:     "example",
		Namespace:       "clusters",
		NodeCount:       2,
		NodeUpgradeType: "",
		ReleaseImage:    "",
	}

	cmd.PersistentFlags().StringVar(&opts.Name, "name", opts.Name, "The name of the NodePool.")
	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace in which to create the NodePool.")
	cmd.PersistentFlags().Int32Var(&opts.NodeCount, "node-count", opts.NodeCount, "The number of nodes to create in the NodePool.")
	cmd.PersistentFlags().StringVar(&opts.ClusterName, "cluster-name", opts.ClusterName, "The name of the HostedCluster nodes in this pool will join.")
	cmd.PersistentFlags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The release image for nodes; if this is empty, defaults to the same release image as the HostedCluster.")
	cmd.PersistentFlags().Var(&opts.NodeUpgradeType, "node-upgrade-type", "The NodePool upgrade strategy for how nodes should behave when upgraded. Supported options: Replace, InPlace")
	cmd.PersistentFlags().BoolVar(&opts.Render, "render", false, "Render output as YAML to stdout instead of applying.")
	cmd.PersistentFlags().BoolVar(&opts.AutoRepair, "auto-repair", opts.AutoRepair, "Enables machine auto-repair with machine health checks.")
	cmd.PersistentFlags().StringVar(&opts.Arch, "arch", opts.Arch, "The processor architecture for the NodePool (e.g. arm64, amd64)")

	_ = cmd.MarkPersistentFlagRequired("name")

	cmd.AddCommand(agent.NewCreateCommand(opts))
	cmd.AddCommand(aws.NewCreateCommand(opts))
	cmd.AddCommand(kubevirt.NewCreateCommand(opts))
	cmd.AddCommand(openstack.NewCreateCommand(opts))

	return cmd
}
