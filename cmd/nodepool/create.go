package nodepool

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift/cmd/nodepool/agent"
	"github.com/openshift/hypershift/cmd/nodepool/aws"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/cmd/nodepool/kubevirt"
	"github.com/openshift/hypershift/cmd/nodepool/none"
)

// The following lines are needed in order to validate that any platform implementing PlatformOptions satisfy the interface
var _ core.PlatformOptions = &aws.AWSPlatformCreateOptions{}
var _ core.PlatformOptions = &kubevirt.KubevirtPlatformCreateOptions{}
var _ core.PlatformOptions = &agent.AgentPlatformCreateOptions{}
var _ core.PlatformOptions = &none.NonePlatformCreateOptions{}

func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "nodepool",
		Short:        "Create a HyperShift NodePool",
		SilenceUsage: true,
	}

	opts := core.NewCreateNodePoolOptions(cmd, 2)

	// All the flags added here would be included only in NodePool create command
	// In order to include flag also in Cluster create command, add the flag in `cmd/nodepool/core/create.go`
	cmd.PersistentFlags().StringVar(&opts.Name, "name", opts.Name, "The name of the NodePool")
	cmd.PersistentFlags().StringVar(&opts.Namespace, "namespace", opts.Namespace, "The namespace in which to create the NodePool")
	cmd.PersistentFlags().StringVar(&opts.ClusterName, "cluster-name", opts.ClusterName, "The name of the HostedCluster nodes in this pool will join")
	cmd.PersistentFlags().StringVar(&opts.ReleaseImage, "release-image", opts.ReleaseImage, "The release image for nodes. If empty, defaults to the same release image as the HostedCluster.")
	cmd.PersistentFlags().BoolVar(&opts.Render, "render", false, "Render output as YAML to stdout instead of applying")

	cmd.MarkPersistentFlagRequired("namespace")
	cmd.MarkPersistentFlagRequired("cluster-name")

	cmd.AddCommand(kubevirt.NewCreateCommand(opts))
	cmd.AddCommand(aws.NewCreateCommand(opts))
	cmd.AddCommand(agent.NewCreateCommand(opts))
	cmd.AddCommand(none.NewCreateCommand(opts))

	return cmd
}
