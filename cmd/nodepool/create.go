package nodepool

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/agent"
	"github.com/openshift/hypershift/cmd/nodepool/aws"
	"github.com/openshift/hypershift/cmd/nodepool/azure"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/cmd/nodepool/kubevirt"
	"github.com/openshift/hypershift/cmd/nodepool/openstack"
	"github.com/openshift/hypershift/cmd/nodepool/powervs"

	"github.com/spf13/cobra"
)

// The following lines are needed in order to validate that any platform implementing PlatformOptions satisfy the interface
var _ core.PlatformOptions = &aws.AWSPlatformCreateOptions{}
var _ core.PlatformOptions = &kubevirt.KubevirtPlatformCreateOptions{}
var _ core.PlatformOptions = &agent.AgentPlatformCreateOptions{}
var _ core.PlatformOptions = &openstack.OpenStackPlatformCreateOptions{}

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "nodepool",
		Short:        "Create a HyperShift NodePool",
		SilenceUsage: true,
	}

	opts := &core.CreateNodePoolOptions{
		Name:            "example",
		Namespace:       "clusters",
		ClusterName:     "example",
		NodeCount:       2,
		ReleaseImage:    "",
		NodeUpgradeType: hyperv1.UpgradeTypeReplace,
		Arch:            "amd64",
	}

	cmd.PersistentFlags().StringVar(&opts.Name, "name", opts.Name, "The name of the NodePool")
	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace in which to create the NodePool")
	cmd.PersistentFlags().Int32Var(&opts.NodeCount, "node-count", opts.NodeCount, "The number of nodes to create in the NodePool")
	cmd.PersistentFlags().StringVar(&opts.ClusterName, "cluster-name", opts.ClusterName, "The name of the HostedCluster nodes in this pool will join")
	cmd.PersistentFlags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The release image for nodes. If empty, defaults to the same release image as the HostedCluster.")
	cmd.PersistentFlags().Var(&opts.NodeUpgradeType, "node-upgrade-type", "The NodePool upgrade strategy for how nodes should behave when upgraded. Supported options: Replace, InPlace")
	cmd.PersistentFlags().StringVar(&opts.Arch, "arch", opts.Arch, "The processor architecture for the NodePool (e.g. arm64, amd64)")

	cmd.PersistentFlags().BoolVar(&opts.Render, "render", false, "Render output as YAML to stdout instead of applying")
	cmd.PersistentFlags().BoolVar(&opts.AutoRepair, "auto-repair", opts.AutoRepair, "Enables machine auto-repair with machine health checks.")

	cmd.AddCommand(kubevirt.NewCreateCommand(opts))
	cmd.AddCommand(aws.NewCreateCommand(opts))
	cmd.AddCommand(agent.NewCreateCommand(opts))
	cmd.AddCommand(azure.NewCreateCommand(opts))
	cmd.AddCommand(powervs.NewCreateCommand(opts))
	cmd.AddCommand(openstack.NewCreateCommand(opts))

	return cmd
}
