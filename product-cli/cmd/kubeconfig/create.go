package kubeconfig

import (
	"fmt"
	"os"

	hypershiftkubeconfig "github.com/openshift/hypershift/cmd/kubeconfig"

	"github.com/spf13/cobra"
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
		Short:        "Renders kubeconfigs for HostedCluster resources",
		Long:         hypershiftkubeconfig.Description,
		SilenceUsage: true,
	}

	opts := options{
		namespace: "clusters",
	}

	cmd.Flags().StringVar(&opts.namespace, "namespace", opts.namespace, "A HostedCluster namespace. Defaults to 'clusters'.")
	cmd.Flags().StringVar(&opts.name, "name", opts.name, "A HostedCluster name.")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := hypershiftkubeconfig.Render(cmd.Context(), opts.namespace, opts.name); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			return err
		}
		return nil
	}

	return cmd
}
