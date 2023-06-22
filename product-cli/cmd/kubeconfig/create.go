package kubeconfig

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	hyperShiftKubeConfig "github.com/openshift/hypershift/cmd/kubeconfig"
)

type options struct {
	namespace string
	name      string
}

// NewCreateCommand returns a command which can render kubeconfigs for HostedCluster
// resources.
func NewCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "kubeconfig",
		Short:        "Renders kubeconfigs for HostedCluster resources.",
		Long:         hyperShiftKubeConfig.Description,
		SilenceUsage: true,
	}

	opts := options{}

	cmd.Flags().StringVar(&opts.namespace, "namespace", opts.namespace, "A HostedCluster namespace. Will default to 'clusters' if a --name is supplied")
	cmd.Flags().StringVar(&opts.name, "name", opts.name, "A HostedCluster name")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if opts.name != "" && opts.namespace == "" {
			opts.namespace = "clusters"
		}
		if err := hyperShiftKubeConfig.Render(cmd.Context(), opts.namespace, opts.name); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "%s\n", err)
			return err
		}
		return nil
	}

	return cmd
}
