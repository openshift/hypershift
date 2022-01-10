package nodepool

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/nodepool/aws"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/cmd/nodepool/kubevirt"
)

// The following lines are needed in order to validate that any platform implementing PlatformOptions satisfy the interface
var _ core.PlatformOptions = aws.AWSPlatformCreateOptions{}
var _ core.PlatformOptions = kubevirt.KubevirtPlatformCreateOptions{}

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "nodepool",
		Short:        "Create a HyperShift NodePool",
		SilenceUsage: true,
	}

	opts := core.CreateNodePoolOptions{
		Name:         "example",
		Namespace:    "clusters",
		ClusterName:  "example",
		NodeCount:    2,
		ReleaseImage: "",
	}

	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "The name of the NodePool")
	cmd.Flags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace in which to create the NodePool")
	cmd.Flags().Int32Var(&opts.NodeCount, "node-count", opts.NodeCount, "The number of nodes to create in the NodePool")
	cmd.Flags().StringVar(&opts.ClusterName, "cluster-name", opts.ClusterName, "The name of the HostedCluster nodes in this pool will join")
	cmd.Flags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The release image for nodes. If empty, defaults to the same release image as the HostedCluster.")

	cmd.Flags().BoolVar(&opts.Render, "render", false, "Render output as YAML to stdout instead of applying")

	cmd.AddCommand(kubevirt.NewCreateCommand(opts))
	cmd.AddCommand(aws.NewCreateCommand(opts))

	return cmd
}
