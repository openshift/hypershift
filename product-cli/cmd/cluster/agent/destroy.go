package agent

import (
	"github.com/openshift/hypershift/cmd/cluster/agent"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"

	"github.com/spf13/cobra"
)

func NewDestroyCommand(opts *core.DestroyOptions, clientProviders ...*core.ClientProvider) *cobra.Command {
	clientProvider := core.ResolveClientProvider(clientProviders...)
	cmd := &cobra.Command{
		Use:          "agent",
		Short:        "Destroys a HostedCluster and its associated infrastructure on Agent",
		SilenceUsage: true,
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := clientProvider.ControllerRuntimeClientFor(opts.Kubeconfig)
		if err != nil {
			return err
		}
		if err := agent.DestroyCluster(cmd.Context(), opts, client); err != nil {
			log.Log.Error(err, "Failed to destroy cluster")
			return err
		}

		return nil
	}

	return cmd
}
